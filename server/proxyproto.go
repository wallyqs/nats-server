// Copyright 2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// PROXY protocol v2 constants
const (
	// Protocol signature (12 bytes)
	proxyProtoV2Sig = "\x0D\x0A\x0D\x0A\x00\x0D\x0A\x51\x55\x49\x54\x0A"

	// Version and command byte format: version(4 bits) | command(4 bits)
	proxyProtoV2VerMask = 0xF0
	proxyProtoV2Ver     = 0x20 // Version 2

	// Commands
	proxyProtoCmdMask  = 0x0F
	proxyProtoCmdLocal = 0x00 // LOCAL command (health check, use original connection)
	proxyProtoCmdProxy = 0x01 // PROXY command (proxied connection)

	// Address family and protocol byte format: family(4 bits) | protocol(4 bits)
	proxyProtoFamilyMask    = 0xF0
	proxyProtoFamilyUnspec  = 0x00 // Unspecified
	proxyProtoFamilyInet    = 0x10 // IPv4
	proxyProtoFamilyInet6   = 0x20 // IPv6
	proxyProtoFamilyUnix    = 0x30 // Unix socket
	proxyProtoProtoMask     = 0x0F
	proxyProtoProtoUnspec   = 0x00 // Unspecified
	proxyProtoProtoStream   = 0x01 // TCP/STREAM
	proxyProtoProtoDatagram = 0x02 // UDP/DGRAM

	// Address sizes
	proxyProtoAddrSizeIPv4 = 12 // 4 (src IP) + 4 (dst IP) + 2 (src port) + 2 (dst port)
	proxyProtoAddrSizeIPv6 = 36 // 16 (src IP) + 16 (dst IP) + 2 (src port) + 2 (dst port)

	// Header sizes
	proxyProtoV2HeaderSize = 16 // Fixed header: 12 (sig) + 1 (ver/cmd) + 1 (fam/proto) + 2 (addr len)

	// Timeout for reading PROXY protocol header
	proxyProtoReadTimeout = 5 * time.Second
)

var (
	// Errors
	errProxyProtoInvalidSig     = errors.New("invalid PROXY protocol signature")
	errProxyProtoInvalidVersion = errors.New("invalid PROXY protocol version")
	errProxyProtoInvalidFamily  = errors.New("unsupported address family")
	errProxyProtoInvalidProto   = errors.New("unsupported protocol")
	errProxyProtoAddrTooLong    = errors.New("address data too long")
	errProxyProtoReadTimeout    = errors.New("timeout reading PROXY protocol header")
)

// proxyProtoAddr contains the address information extracted from PROXY protocol header
type proxyProtoAddr struct {
	srcIP   net.IP
	srcPort uint16
	dstIP   net.IP
	dstPort uint16
}

// String implements net.Addr interface
func (p *proxyProtoAddr) String() string {
	return net.JoinHostPort(p.srcIP.String(), fmt.Sprintf("%d", p.srcPort))
}

// Network implements net.Addr interface
func (p *proxyProtoAddr) Network() string {
	if p.srcIP.To4() != nil {
		return "tcp4"
	}
	return "tcp6"
}

// proxyConn wraps a net.Conn to override RemoteAddr() with the address
// extracted from the PROXY protocol header
type proxyConn struct {
	net.Conn
	remoteAddr net.Addr
}

// RemoteAddr returns the original client address extracted from PROXY protocol
func (pc *proxyConn) RemoteAddr() net.Addr {
	return pc.remoteAddr
}

// readProxyProtoV2Header reads and parses a PROXY protocol v2 header from the connection.
// If the command is LOCAL (health check), it returns nil for addr and no error.
// If the command is PROXY, it returns the parsed address information.
// The connection must be fresh (no data read yet).
func readProxyProtoV2Header(conn net.Conn) (*proxyProtoAddr, error) {
	// Set read deadline to prevent hanging on slow/malicious clients
	if err := conn.SetReadDeadline(time.Now().Add(proxyProtoReadTimeout)); err != nil {
		return nil, err
	}

	// Read fixed header (16 bytes)
	header := make([]byte, proxyProtoV2HeaderSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil, errProxyProtoReadTimeout
		}
		return nil, fmt.Errorf("failed to read PROXY protocol header: %w", err)
	}

	// Clear read deadline after successful header read
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return nil, err
	}

	// Validate signature (first 12 bytes)
	if string(header[:12]) != proxyProtoV2Sig {
		return nil, errProxyProtoInvalidSig
	}

	// Parse version and command
	verCmd := header[12]
	version := verCmd & proxyProtoV2VerMask
	command := verCmd & proxyProtoCmdMask

	if version != proxyProtoV2Ver {
		return nil, errProxyProtoInvalidVersion
	}

	// Parse address family and protocol
	famProto := header[13]
	family := famProto & proxyProtoFamilyMask
	protocol := famProto & proxyProtoProtoMask

	// Parse address length (big-endian uint16)
	addrLen := binary.BigEndian.Uint16(header[14:16])

	// Handle LOCAL command (health check)
	if command == proxyProtoCmdLocal {
		// For LOCAL, we should skip the address data if any
		if addrLen > 0 {
			// Discard the address data
			if err := discardBytes(conn, int(addrLen)); err != nil {
				return nil, fmt.Errorf("failed to discard LOCAL command address data: %w", err)
			}
		}
		return nil, nil // nil addr indicates LOCAL command
	}

	// Handle PROXY command
	if command != proxyProtoCmdProxy {
		return nil, fmt.Errorf("unknown PROXY protocol command: 0x%02x", command)
	}

	// Validate protocol (we only support STREAM/TCP)
	if protocol != proxyProtoProtoStream {
		return nil, errProxyProtoInvalidProto
	}

	// Parse address data based on family
	var addr *proxyProtoAddr
	var err error

	switch family {
	case proxyProtoFamilyInet:
		addr, err = parseIPv4Addr(conn, addrLen)
	case proxyProtoFamilyInet6:
		addr, err = parseIPv6Addr(conn, addrLen)
	case proxyProtoFamilyUnspec:
		// UNSPEC family with PROXY command is valid but rare
		// Just skip the address data
		if addrLen > 0 {
			if err := discardBytes(conn, int(addrLen)); err != nil {
				return nil, fmt.Errorf("failed to discard UNSPEC address data: %w", err)
			}
		}
		return nil, nil
	default:
		return nil, errProxyProtoInvalidFamily
	}

	if err != nil {
		return nil, err
	}

	return addr, nil
}

// parseIPv4Addr parses IPv4 address data from PROXY protocol header
func parseIPv4Addr(conn net.Conn, addrLen uint16) (*proxyProtoAddr, error) {
	// IPv4: 4 (src IP) + 4 (dst IP) + 2 (src port) + 2 (dst port) = 12 bytes minimum
	if addrLen < proxyProtoAddrSizeIPv4 {
		return nil, fmt.Errorf("IPv4 address data too short: %d bytes", addrLen)
	}

	// Read address data
	addrData := make([]byte, addrLen)
	if _, err := io.ReadFull(conn, addrData); err != nil {
		return nil, fmt.Errorf("failed to read IPv4 address data: %w", err)
	}

	addr := &proxyProtoAddr{
		srcIP:   net.IP(addrData[0:4]),
		dstIP:   net.IP(addrData[4:8]),
		srcPort: binary.BigEndian.Uint16(addrData[8:10]),
		dstPort: binary.BigEndian.Uint16(addrData[10:12]),
	}

	return addr, nil
}

// parseIPv6Addr parses IPv6 address data from PROXY protocol header
func parseIPv6Addr(conn net.Conn, addrLen uint16) (*proxyProtoAddr, error) {
	// IPv6: 16 (src IP) + 16 (dst IP) + 2 (src port) + 2 (dst port) = 36 bytes minimum
	if addrLen < proxyProtoAddrSizeIPv6 {
		return nil, fmt.Errorf("IPv6 address data too short: %d bytes", addrLen)
	}

	// Read address data
	addrData := make([]byte, addrLen)
	if _, err := io.ReadFull(conn, addrData); err != nil {
		return nil, fmt.Errorf("failed to read IPv6 address data: %w", err)
	}

	addr := &proxyProtoAddr{
		srcIP:   net.IP(addrData[0:16]),
		dstIP:   net.IP(addrData[16:32]),
		srcPort: binary.BigEndian.Uint16(addrData[32:34]),
		dstPort: binary.BigEndian.Uint16(addrData[34:36]),
	}

	return addr, nil
}

// discardBytes reads and discards n bytes from the connection
func discardBytes(conn net.Conn, n int) error {
	if n <= 0 {
		return nil
	}
	// Limit to prevent excessive memory allocation
	if n > 65535 {
		return errProxyProtoAddrTooLong
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(conn, buf)
	return err
}

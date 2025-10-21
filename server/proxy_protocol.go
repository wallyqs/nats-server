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
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	proxyV1HeaderPrefix = "PROXY"
	proxyV2HeaderPrefix = "\x0D\x0A\x0D\x0A\x00\x0D\x0A\x51\x55\x49\x54\x0A"
	proxyV2HeaderLen    = 16
	proxyV1MaxLen       = 108
	proxyTimeout        = 5 * time.Second

	// PROXY protocol v2 constants
	proxyProtoV2VerMask = 0xF0
	proxyProtoV2Ver     = 0x20 // Version 2

	// Commands
	proxyProtoCmdMask  = 0x0F
	proxyProtoCmdLocal = 0x00 // LOCAL command (health check)
	proxyProtoCmdProxy = 0x01 // PROXY command

	// Address family
	proxyProtoFamilyMask   = 0xF0
	proxyProtoFamilyUnspec = 0x00
	proxyProtoFamilyInet   = 0x10 // IPv4
	proxyProtoFamilyInet6  = 0x20 // IPv6

	// Protocol
	proxyProtoProtoMask     = 0x0F
	proxyProtoProtoUnspec   = 0x00
	proxyProtoProtoStream   = 0x01 // TCP/STREAM
	proxyProtoProtoDatagram = 0x02 // UDP/DGRAM
)

var (
	// Predefined errors for better error handling
	errProxyProtoInvalidSig     = errors.New("invalid PROXY protocol signature")
	errProxyProtoInvalidVersion = errors.New("invalid PROXY protocol version")
	errProxyProtoInvalidFamily  = errors.New("unsupported address family")
	errProxyProtoInvalidProto   = errors.New("unsupported protocol")
	errProxyProtoAddrTooLong    = errors.New("address data too long")
	errProxyProtoReadTimeout    = errors.New("timeout reading PROXY protocol header")
	errProxyProtoInvalidV1      = errors.New("invalid PROXY v1 header")
)

type ProxyProtocolInfo struct {
	SrcIP    net.IP
	DestIP   net.IP
	SrcPort  uint16
	DestPort uint16
}

// String implements net.Addr interface
func (p *ProxyProtocolInfo) String() string {
	return net.JoinHostPort(p.SrcIP.String(), fmt.Sprintf("%d", p.SrcPort))
}

// Network implements net.Addr interface
func (p *ProxyProtocolInfo) Network() string {
	if p.SrcIP.To4() != nil {
		return "tcp4"
	}
	return "tcp6"
}

type proxyConn struct {
	net.Conn
	proxyInfo *ProxyProtocolInfo
}

func (pc *proxyConn) RemoteAddr() net.Addr {
	if pc.proxyInfo != nil && pc.proxyInfo.SrcIP != nil {
		return pc.proxyInfo
	}
	return pc.Conn.RemoteAddr()
}

func (pc *proxyConn) LocalAddr() net.Addr {
	if pc.proxyInfo != nil && pc.proxyInfo.DestIP != nil {
		return &net.TCPAddr{
			IP:   pc.proxyInfo.DestIP,
			Port: int(pc.proxyInfo.DestPort),
		}
	}
	return pc.Conn.LocalAddr()
}

func parseProxyProtocol(conn net.Conn) (net.Conn, error) {
	connWithTimeout := &timeoutConn{Conn: conn, timeout: proxyTimeout}
	reader := bufio.NewReader(connWithTimeout)

	// Peek at the first byte to determine protocol version
	firstByte, err := reader.Peek(1)
	if err != nil {
		return nil, fmt.Errorf("failed to peek first byte: %w", err)
	}

	var proxyInfo *ProxyProtocolInfo

	if firstByte[0] == 'P' {
		// PROXY v1 (text-based)
		proxyInfo, _, err = parseProxyV1(reader)
	} else if firstByte[0] == '\x0D' {
		// PROXY v2 (binary)
		proxyInfo, _, err = parseProxyV2(reader)
	} else {
		return nil, errProxyProtoInvalidSig
	}

	if err != nil {
		return nil, err
	}

	// Create a buffered reader with any remaining data
	remainingReader := &readerWithUnread{
		Reader: reader,
		conn:   conn,
	}

	// Wrap the connection
	wrappedConn := &bufferedConn{
		Conn:   conn,
		reader: remainingReader,
	}

	return &proxyConn{
		Conn:      wrappedConn,
		proxyInfo: proxyInfo,
	}, nil
}

func parseProxyV1(reader *bufio.Reader) (*ProxyProtocolInfo, int, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil, 0, errProxyProtoReadTimeout
		}
		return nil, 0, fmt.Errorf("failed to read PROXY v1 line: %w", err)
	}

	line = strings.TrimSpace(line)
	if len(line) > proxyV1MaxLen {
		return nil, 0, errProxyProtoAddrTooLong
	}

	parts := strings.Split(line, " ")

	if len(parts) < 2 || parts[0] != "PROXY" {
		return nil, 0, errProxyProtoInvalidV1
	}

	if len(parts) < 6 {
		// Handle UNKNOWN connections or malformed headers
		if len(parts) >= 2 && parts[1] == "UNKNOWN" {
			return &ProxyProtocolInfo{}, len(line) + 1, nil
		}
		return nil, 0, errProxyProtoInvalidV1
	}

	protocol := parts[1]
	if protocol != "TCP4" && protocol != "TCP6" {
		// UNKNOWN connection, return empty proxy info
		return &ProxyProtocolInfo{}, len(line) + 1, nil
	}

	srcIP := net.ParseIP(parts[2])
	if srcIP == nil {
		return nil, 0, fmt.Errorf("invalid source IP: %s", parts[2])
	}

	destIP := net.ParseIP(parts[3])
	if destIP == nil {
		return nil, 0, fmt.Errorf("invalid destination IP: %s", parts[3])
	}

	srcPort, err := strconv.ParseUint(parts[4], 10, 16)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid source port: %s", parts[4])
	}

	destPort, err := strconv.ParseUint(parts[5], 10, 16)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid destination port: %s", parts[5])
	}

	return &ProxyProtocolInfo{
		SrcIP:    srcIP,
		DestIP:   destIP,
		SrcPort:  uint16(srcPort),
		DestPort: uint16(destPort),
	}, len(line) + 1, nil
}

func parseProxyV2(reader *bufio.Reader) (*ProxyProtocolInfo, int, error) {
	header := make([]byte, proxyV2HeaderLen)
	_, err := io.ReadFull(reader, header)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil, 0, errProxyProtoReadTimeout
		}
		return nil, 0, fmt.Errorf("failed to read PROXY v2 header: %w", err)
	}

	// Verify signature
	if string(header[:12]) != proxyV2HeaderPrefix {
		return nil, 0, errProxyProtoInvalidSig
	}

	// Parse version and command
	versionCommand := header[12]
	version := versionCommand & proxyProtoV2VerMask
	command := versionCommand & proxyProtoCmdMask

	if version != proxyProtoV2Ver {
		return nil, 0, errProxyProtoInvalidVersion
	}

	// Parse family and protocol
	familyProtocol := header[13]
	family := familyProtocol & proxyProtoFamilyMask
	protocol := familyProtocol & proxyProtoProtoMask

	// Parse length
	length := binary.BigEndian.Uint16(header[14:16])

	// Limit address data size to prevent excessive memory allocation
	if length > 65535 {
		return nil, 0, errProxyProtoAddrTooLong
	}

	// Read the address information
	addressInfo := make([]byte, length)
	if length > 0 {
		_, err = io.ReadFull(reader, addressInfo)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to read PROXY v2 address info: %w", err)
		}
	}

	totalBytes := proxyV2HeaderLen + int(length)

	// Handle LOCAL command (health checks)
	if command == proxyProtoCmdLocal {
		return &ProxyProtocolInfo{}, totalBytes, nil
	}

	// Handle PROXY command
	if command != proxyProtoCmdProxy {
		return nil, 0, fmt.Errorf("unsupported PROXY v2 command: 0x%02x", command)
	}

	// Validate protocol (we only support STREAM/TCP)
	if protocol != proxyProtoProtoStream && protocol != proxyProtoProtoUnspec {
		return nil, 0, errProxyProtoInvalidProto
	}

	// Parse address information based on family
	switch family {
	case proxyProtoFamilyInet: // IPv4
		if length < 12 {
			return nil, 0, fmt.Errorf("insufficient IPv4 address data: %d bytes", length)
		}
		return &ProxyProtocolInfo{
			SrcIP:    net.IP(addressInfo[0:4]),
			DestIP:   net.IP(addressInfo[4:8]),
			SrcPort:  binary.BigEndian.Uint16(addressInfo[8:10]),
			DestPort: binary.BigEndian.Uint16(addressInfo[10:12]),
		}, totalBytes, nil

	case proxyProtoFamilyInet6: // IPv6
		if length < 36 {
			return nil, 0, fmt.Errorf("insufficient IPv6 address data: %d bytes", length)
		}
		return &ProxyProtocolInfo{
			SrcIP:    net.IP(addressInfo[0:16]),
			DestIP:   net.IP(addressInfo[16:32]),
			SrcPort:  binary.BigEndian.Uint16(addressInfo[32:34]),
			DestPort: binary.BigEndian.Uint16(addressInfo[34:36]),
		}, totalBytes, nil

	case proxyProtoFamilyUnspec:
		// UNSPEC family with PROXY command is valid but rare
		return &ProxyProtocolInfo{}, totalBytes, nil

	default:
		// Unsupported family
		return nil, 0, errProxyProtoInvalidFamily
	}
}

// timeoutConn wraps a connection with a read timeout
type timeoutConn struct {
	net.Conn
	timeout time.Duration
}

func (tc *timeoutConn) Read(b []byte) (int, error) {
	tc.Conn.SetReadDeadline(time.Now().Add(tc.timeout))
	return tc.Conn.Read(b)
}

// readerWithUnread allows reading remaining data from the buffered reader
type readerWithUnread struct {
	io.Reader
	conn net.Conn
}

// bufferedConn wraps a connection with a buffered reader for any unread data
type bufferedConn struct {
	net.Conn
	reader io.Reader
}

func (bc *bufferedConn) Read(b []byte) (int, error) {
	return bc.reader.Read(b)
}

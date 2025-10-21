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
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProxyProtocolV1(t *testing.T) {
	tests := []struct {
		name        string
		header      string
		expectError bool
		expectedSrc string
		expectedDst string
		srcPort     int
		dstPort     int
	}{
		{
			name:        "Valid TCP4",
			header:      "PROXY TCP4 192.168.1.1 192.168.1.2 1234 4222\r\n",
			expectError: false,
			expectedSrc: "192.168.1.1",
			expectedDst: "192.168.1.2",
			srcPort:     1234,
			dstPort:     4222,
		},
		{
			name:        "Valid TCP6",
			header:      "PROXY TCP6 2001:db8::1 2001:db8::2 1234 4222\r\n",
			expectError: false,
			expectedSrc: "2001:db8::1",
			expectedDst: "2001:db8::2",
			srcPort:     1234,
			dstPort:     4222,
		},
		{
			name:        "Unknown connection",
			header:      "PROXY UNKNOWN\r\n",
			expectError: false,
		},
		{
			name:        "Invalid protocol",
			header:      "PROXY INVALID 192.168.1.1 192.168.1.2 1234 4222\r\n",
			expectError: false, // UNKNOWN connections are valid
		},
		{
			name:        "Invalid header",
			header:      "INVALID HEADER\r\n",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.header + "CONNECT {}\r\n"
			conn := &mockConn{
				readData: []byte(data),
			}

			wrappedConn, err := parseProxyProtocol(conn)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			proxyConn, ok := wrappedConn.(*proxyConn)
			if !ok {
				t.Fatal("Expected proxyConn")
			}

			if tt.expectedSrc != "" {
				remoteAddr := proxyConn.RemoteAddr()
				if proxyInfo, ok := remoteAddr.(*ProxyProtocolInfo); ok {
					if proxyInfo.SrcIP.String() != tt.expectedSrc {
						t.Errorf("Expected source IP %s, got %s", tt.expectedSrc, proxyInfo.SrcIP.String())
					}
					if int(proxyInfo.SrcPort) != tt.srcPort {
						t.Errorf("Expected source port %d, got %d", tt.srcPort, proxyInfo.SrcPort)
					}
				} else {
					t.Errorf("Expected ProxyProtocolInfo remote address, got %T", remoteAddr)
				}

				localAddr := proxyConn.LocalAddr().(*net.TCPAddr)
				if localAddr.IP.String() != tt.expectedDst {
					t.Errorf("Expected destination IP %s, got %s", tt.expectedDst, localAddr.IP.String())
				}
				if localAddr.Port != tt.dstPort {
					t.Errorf("Expected destination port %d, got %d", tt.dstPort, localAddr.Port)
				}
			}

			// Test that we can still read the CONNECT message
			buf := make([]byte, 1024)
			n, err := wrappedConn.Read(buf)
			if err != nil {
				t.Fatalf("Failed to read from wrapped connection: %v", err)
			}

			if !bytes.HasPrefix(buf[:n], []byte("CONNECT")) {
				t.Error("Expected to read CONNECT message after PROXY header")
			}
		})
	}
}

func TestProxyProtocolV2(t *testing.T) {
	tests := []struct {
		name        string
		buildHeader func() []byte
		expectError bool
		expectedSrc string
		expectedDst string
		srcPort     int
		dstPort     int
	}{
		{
			name: "Valid IPv4 PROXY",
			buildHeader: func() []byte {
				header := make([]byte, 28)
				copy(header[0:12], []byte("\x0D\x0A\x0D\x0A\x00\x0D\x0A\x51\x55\x49\x54\x0A"))
				header[12] = 0x21                             // Version 2, PROXY command
				header[13] = 0x11                             // IPv4, TCP
				binary.BigEndian.PutUint16(header[14:16], 12) // Length
				copy(header[16:20], net.ParseIP("192.168.1.1").To4())
				copy(header[20:24], net.ParseIP("192.168.1.2").To4())
				binary.BigEndian.PutUint16(header[24:26], 1234) // Source port
				binary.BigEndian.PutUint16(header[26:28], 4222) // Dest port
				return header
			},
			expectError: false,
			expectedSrc: "192.168.1.1",
			expectedDst: "192.168.1.2",
			srcPort:     1234,
			dstPort:     4222,
		},
		{
			name: "Valid IPv6 PROXY",
			buildHeader: func() []byte {
				header := make([]byte, 52)
				copy(header[0:12], []byte("\x0D\x0A\x0D\x0A\x00\x0D\x0A\x51\x55\x49\x54\x0A"))
				header[12] = 0x21                             // Version 2, PROXY command
				header[13] = 0x21                             // IPv6, TCP
				binary.BigEndian.PutUint16(header[14:16], 36) // Length
				copy(header[16:32], net.ParseIP("2001:db8::1").To16())
				copy(header[32:48], net.ParseIP("2001:db8::2").To16())
				binary.BigEndian.PutUint16(header[48:50], 1234) // Source port
				binary.BigEndian.PutUint16(header[50:52], 4222) // Dest port
				return header
			},
			expectError: false,
			expectedSrc: "2001:db8::1",
			expectedDst: "2001:db8::2",
			srcPort:     1234,
			dstPort:     4222,
		},
		{
			name: "Valid LOCAL command",
			buildHeader: func() []byte {
				header := make([]byte, 16)
				copy(header[0:12], []byte("\x0D\x0A\x0D\x0A\x00\x0D\x0A\x51\x55\x49\x54\x0A"))
				header[12] = 0x20                            // Version 2, LOCAL command
				header[13] = 0x00                            // UNSPEC
				binary.BigEndian.PutUint16(header[14:16], 0) // Length
				return header
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := tt.buildHeader()
			data := append(header, []byte("CONNECT {}\r\n")...)
			conn := &mockConn{
				readData: data,
			}

			wrappedConn, err := parseProxyProtocol(conn)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			proxyConn, ok := wrappedConn.(*proxyConn)
			if !ok {
				t.Fatal("Expected proxyConn")
			}

			if tt.expectedSrc != "" {
				remoteAddr := proxyConn.RemoteAddr()
				if proxyInfo, ok := remoteAddr.(*ProxyProtocolInfo); ok {
					if proxyInfo.SrcIP.String() != tt.expectedSrc {
						t.Errorf("Expected source IP %s, got %s", tt.expectedSrc, proxyInfo.SrcIP.String())
					}
					if int(proxyInfo.SrcPort) != tt.srcPort {
						t.Errorf("Expected source port %d, got %d", tt.srcPort, proxyInfo.SrcPort)
					}
				} else {
					t.Errorf("Expected ProxyProtocolInfo remote address, got %T", remoteAddr)
				}

				localAddr := proxyConn.LocalAddr().(*net.TCPAddr)
				if localAddr.IP.String() != tt.expectedDst {
					t.Errorf("Expected destination IP %s, got %s", tt.expectedDst, localAddr.IP.String())
				}
				if localAddr.Port != tt.dstPort {
					t.Errorf("Expected destination port %d, got %d", tt.dstPort, localAddr.Port)
				}
			}

			// Test that we can still read the CONNECT message
			buf := make([]byte, 1024)
			n, err := wrappedConn.Read(buf)
			if err != nil {
				t.Fatalf("Failed to read from wrapped connection: %v", err)
			}

			if !bytes.HasPrefix(buf[:n], []byte("CONNECT")) {
				t.Error("Expected to read CONNECT message after PROXY header")
			}
		})
	}
}

func TestProxyProtocolIntegration(t *testing.T) {
	opts := DefaultOptions()
	opts.Host = "127.0.0.1"
	opts.Port = -1
	opts.ProxyProtocol = true

	s := RunServer(opts)
	defer s.Shutdown()

	// Create a connection that will send PROXY protocol header
	addr := fmt.Sprintf("%s:%d", opts.Host, s.Addr().(*net.TCPAddr).Port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Send PROXY v1 header
	proxyHeader := "PROXY TCP4 192.168.1.100 192.168.1.1 12345 4222\r\n"
	_, err = conn.Write([]byte(proxyHeader))
	if err != nil {
		t.Fatalf("Failed to write PROXY header: %v", err)
	}

	// Now send NATS CONNECT
	connectMsg := "CONNECT {\"verbose\":false,\"pedantic\":false}\r\n"
	_, err = conn.Write([]byte(connectMsg))
	if err != nil {
		t.Fatalf("Failed to write CONNECT: %v", err)
	}

	// Read INFO response
	reader := bufio.NewReader(conn)
	info, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read INFO: %v", err)
	}

	if !bytes.HasPrefix([]byte(info), []byte("INFO")) {
		t.Errorf("Expected INFO response, got: %s", info)
	}

	// Send PING to verify connection is working
	_, err = conn.Write([]byte("PING\r\n"))
	if err != nil {
		t.Fatalf("Failed to write PING: %v", err)
	}

	// Read PONG
	pong, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read PONG: %v", err)
	}

	if !bytes.HasPrefix([]byte(pong), []byte("PONG")) {
		t.Errorf("Expected PONG response, got: %s", pong)
	}
}

func TestProxyProtocolWithNatsClient(t *testing.T) {
	opts := DefaultOptions()
	opts.Host = "127.0.0.1"
	opts.Port = -1
	opts.ProxyProtocol = true

	s := RunServer(opts)
	defer s.Shutdown()

	// Create a custom dialer that sends PROXY protocol header
	addr := fmt.Sprintf("%s:%d", opts.Host, s.Addr().(*net.TCPAddr).Port)

	// Test with a raw connection first to ensure PROXY protocol works
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Send PROXY v2 header
	header := make([]byte, 28)
	copy(header[0:12], []byte("\x0D\x0A\x0D\x0A\x00\x0D\x0A\x51\x55\x49\x54\x0A"))
	header[12] = 0x21                             // Version 2, PROXY command
	header[13] = 0x11                             // IPv4, TCP
	binary.BigEndian.PutUint16(header[14:16], 12) // Length
	copy(header[16:20], net.ParseIP("10.0.0.100").To4())
	copy(header[20:24], net.ParseIP("10.0.0.1").To4())
	binary.BigEndian.PutUint16(header[24:26], 54321) // Source port
	binary.BigEndian.PutUint16(header[26:28], 4222)  // Dest port

	_, err = conn.Write(header)
	if err != nil {
		t.Fatalf("Failed to write PROXY header: %v", err)
	}

	// Continue with NATS protocol
	connectMsg := "CONNECT {\"verbose\":false,\"pedantic\":false}\r\n"
	_, err = conn.Write([]byte(connectMsg))
	if err != nil {
		t.Fatalf("Failed to write CONNECT: %v", err)
	}

	// Read INFO
	reader := bufio.NewReader(conn)
	info, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read INFO: %v", err)
	}

	if !bytes.HasPrefix([]byte(info), []byte("INFO")) {
		t.Errorf("Expected INFO response, got: %s", info)
	}

	conn.Close()
}

// mockConn implements net.Conn for testing
type mockConn struct {
	readData []byte
	readPos  int
	written  []byte
}

func (m *mockConn) Read(b []byte) (int, error) {
	if m.readPos >= len(m.readData) {
		return 0, net.ErrClosed
	}

	n := copy(b, m.readData[m.readPos:])
	m.readPos += n
	return n, nil
}

func (m *mockConn) Write(b []byte) (int, error) {
	m.written = append(m.written, b...)
	return len(b), nil
}

func (m *mockConn) Close() error { return nil }
func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4222}
}
func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
}
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestProxyProtocolConfigParsing(t *testing.T) {
	conf := `
		proxy_protocol: true
		port: 4222
	`

	opts := &Options{}
	err := opts.ProcessConfigString(conf)
	if err != nil {
		t.Fatalf("Failed to process config: %v", err)
	}

	if !opts.ProxyProtocol {
		t.Error("Expected ProxyProtocol to be true")
	}

	if opts.Port != 4222 {
		t.Errorf("Expected port 4222, got %d", opts.Port)
	}
}

// Additional edge case tests

func TestProxyProtocolV2InvalidSignature(t *testing.T) {
	// Create invalid signature
	header := make([]byte, 16)
	copy(header, "INVALID_SIG\x00") // Wrong signature
	header[12] = 0x21                // version 2, PROXY command
	header[13] = 0x11                // IPv4, STREAM
	binary.BigEndian.PutUint16(header[14:], 12)

	conn := &mockConn{readData: header}
	_, err := parseProxyProtocol(conn)
	if err == nil {
		t.Fatal("Expected error for invalid signature")
	}
	if err != errProxyProtoInvalidSig {
		t.Errorf("Expected errProxyProtoInvalidSig, got: %v", err)
	}
}

func TestProxyProtocolV2InvalidVersion(t *testing.T) {
	header := make([]byte, 16)
	copy(header, proxyV2HeaderPrefix)
	header[12] = 0x11 // version 1 (wrong), PROXY command
	header[13] = 0x11 // IPv4, STREAM
	binary.BigEndian.PutUint16(header[14:], 12)

	conn := &mockConn{readData: header}
	_, err := parseProxyProtocol(conn)
	if err == nil {
		t.Fatal("Expected error for invalid version")
	}
	if err != errProxyProtoInvalidVersion {
		t.Errorf("Expected errProxyProtoInvalidVersion, got: %v", err)
	}
}

func TestProxyProtocolV2UnsupportedProtocol(t *testing.T) {
	header := make([]byte, 16)
	copy(header, proxyV2HeaderPrefix)
	header[12] = 0x21 // version 2, PROXY command
	header[13] = 0x12 // IPv4, DGRAM (UDP - not supported)
	binary.BigEndian.PutUint16(header[14:], 12)

	// Add address data
	addrData := make([]byte, 12)
	fullData := append(header, addrData...)

	conn := &mockConn{readData: fullData}
	_, err := parseProxyProtocol(conn)
	if err == nil {
		t.Fatal("Expected error for unsupported protocol (UDP)")
	}
	if err != errProxyProtoInvalidProto {
		t.Errorf("Expected errProxyProtoInvalidProto, got: %v", err)
	}
}

func TestProxyProtocolV2TruncatedHeader(t *testing.T) {
	// Only 10 bytes instead of 16
	header := make([]byte, 10)
	copy(header, proxyV2HeaderPrefix[:10])

	conn := &mockConn{readData: header}
	_, err := parseProxyProtocol(conn)
	if err == nil {
		t.Fatal("Expected error for truncated header")
	}
}

func TestProxyProtocolV2ShortIPv4AddressData(t *testing.T) {
	header := make([]byte, 16)
	copy(header, proxyV2HeaderPrefix)
	header[12] = 0x21 // version 2, PROXY command
	header[13] = 0x11 // IPv4, STREAM
	binary.BigEndian.PutUint16(header[14:], 12)

	// Only provide 8 bytes instead of 12
	addrData := make([]byte, 8)
	fullData := append(header, addrData...)

	conn := &mockConn{readData: fullData}
	_, err := parseProxyProtocol(conn)
	if err == nil {
		t.Fatal("Expected error for short address data")
	}
}

func TestProxyProtocolV2ShortIPv6AddressData(t *testing.T) {
	header := make([]byte, 16)
	copy(header, proxyV2HeaderPrefix)
	header[12] = 0x21 // version 2, PROXY command
	header[13] = 0x21 // IPv6, STREAM
	binary.BigEndian.PutUint16(header[14:], 36)

	// Only provide 20 bytes instead of 36
	addrData := make([]byte, 20)
	fullData := append(header, addrData...)

	conn := &mockConn{readData: fullData}
	_, err := parseProxyProtocol(conn)
	if err == nil {
		t.Fatal("Expected error for short IPv6 address data")
	}
}

func TestProxyProtocolV2UnsupportedFamily(t *testing.T) {
	header := make([]byte, 16)
	copy(header, proxyV2HeaderPrefix)
	header[12] = 0x21 // version 2, PROXY command
	header[13] = 0x31 // Unix socket (0x30), STREAM - not supported
	binary.BigEndian.PutUint16(header[14:], 0)

	conn := &mockConn{readData: header}
	_, err := parseProxyProtocol(conn)
	if err == nil {
		t.Fatal("Expected error for unsupported family (Unix socket)")
	}
	if err != errProxyProtoInvalidFamily {
		t.Errorf("Expected errProxyProtoInvalidFamily, got: %v", err)
	}
}

func TestProxyProtocolV1TooLong(t *testing.T) {
	// Create a v1 header that's too long (> 108 bytes)
	longHeader := "PROXY TCP4 192.168.1.1 192.168.1.2 1234 4222" + strings.Repeat(" extra", 20) + "\r\n"

	conn := &mockConn{readData: []byte(longHeader)}
	_, err := parseProxyProtocol(conn)
	if err == nil {
		t.Fatal("Expected error for v1 header too long")
	}
	if err != errProxyProtoAddrTooLong {
		t.Errorf("Expected errProxyProtoAddrTooLong, got: %v", err)
	}
}

func TestProxyProtocolInfoNetworkMethod(t *testing.T) {
	tests := []struct {
		name           string
		info           *ProxyProtocolInfo
		expectedNet    string
		expectedString string
	}{
		{
			name: "IPv4 family",
			info: &ProxyProtocolInfo{
				SrcIP:  net.ParseIP("192.168.1.1"),
				SrcPort: 1234,
				Family: proxyProtoFamilyInet,
			},
			expectedNet:    "tcp4",
			expectedString: "192.168.1.1:1234",
		},
		{
			name: "IPv6 family",
			info: &ProxyProtocolInfo{
				SrcIP:  net.ParseIP("2001:db8::1"),
				SrcPort: 5678,
				Family: proxyProtoFamilyInet6,
			},
			expectedNet:    "tcp6",
			expectedString: "[2001:db8::1]:5678",
		},
		{
			name: "UNSPEC family",
			info: &ProxyProtocolInfo{
				Family: proxyProtoFamilyUnspec,
			},
			expectedNet:    "tcp",
			expectedString: "unknown",
		},
		{
			name: "Unix socket family",
			info: &ProxyProtocolInfo{
				Family: proxyProtoFamilyUnix,
			},
			expectedNet:    "unix",
			expectedString: "unknown",
		},
		{
			name: "UNSPEC family with IPv4 IP (UNKNOWN v1 connection)",
			info: &ProxyProtocolInfo{
				SrcIP:  net.ParseIP("10.0.0.1"),
				SrcPort: 9999,
				Family: proxyProtoFamilyUnspec,
			},
			expectedNet:    "tcp",
			expectedString: "10.0.0.1:9999",
		},
		{
			name: "UNSPEC family with IPv6 IP (UNKNOWN v1 connection)",
			info: &ProxyProtocolInfo{
				SrcIP:  net.ParseIP("fe80::1"),
				SrcPort: 8888,
				Family: proxyProtoFamilyUnspec,
			},
			expectedNet:    "tcp",
			expectedString: "[fe80::1]:8888",
		},
		{
			name: "UNSPEC family no IP (LOCAL v2 command)",
			info: &ProxyProtocolInfo{
				Family: proxyProtoFamilyUnspec,
			},
			expectedNet:    "tcp",
			expectedString: "unknown",
		},
		{
			name: "Invalid/unknown family value",
			info: &ProxyProtocolInfo{
				SrcIP:  net.ParseIP("192.168.1.1"),
				SrcPort: 1234,
				Family: 0xFF, // Invalid family value
			},
			expectedNet:    "unknown",
			expectedString: "192.168.1.1:1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network := tt.info.Network()
			if network != tt.expectedNet {
				t.Errorf("Network() = %q, want %q", network, tt.expectedNet)
			}

			str := tt.info.String()
			if str != tt.expectedString {
				t.Errorf("String() = %q, want %q", str, tt.expectedString)
			}
		})
	}
}

// TestProxyProtocolByteCountTracking verifies that parseProxyV1 and parseProxyV2
// return accurate byte counts even when errors occur, which is critical for
// callers tracking offsets into memory buffers.
func TestProxyProtocolByteCountTracking(t *testing.T) {
	tests := []struct {
		name          string
		data          []byte
		expectedBytes int
		expectError   bool
	}{
		{
			name:          "V2 truncated header (5 bytes)",
			data:          []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00},
			expectedBytes: 5,
			expectError:   true,
		},
		{
			name: "V2 invalid signature (full 16 bytes)",
			data: func() []byte {
				header := make([]byte, 16)
				copy(header, "INVALID_SIG\x00")
				header[12] = 0x21
				header[13] = 0x11
				binary.BigEndian.PutUint16(header[14:], 12)
				return header
			}(),
			expectedBytes: 16,
			expectError:   true,
		},
		{
			name: "V2 invalid version (full 16 bytes)",
			data: func() []byte {
				header := make([]byte, 16)
				copy(header, proxyV2HeaderPrefix)
				header[12] = 0x11 // version 1 (wrong)
				header[13] = 0x11
				binary.BigEndian.PutUint16(header[14:], 12)
				return header
			}(),
			expectedBytes: 16,
			expectError:   true,
		},
		{
			name: "V2 truncated IPv4 address data (header + 5 bytes instead of 12)",
			data: func() []byte {
				header := make([]byte, 16)
				copy(header, proxyV2HeaderPrefix)
				header[12] = 0x21 // version 2, PROXY command
				header[13] = 0x11 // IPv4, STREAM
				binary.BigEndian.PutUint16(header[14:], 12)
				// Add only 5 bytes of address data (should be 12)
				return append(header, []byte{1, 2, 3, 4, 5}...)
			}(),
			expectedBytes: 21, // 16 header + 5 bytes read
			expectError:   true,
		},
		{
			name: "V2 short IPv4 address data (header + full 8 bytes, need 12)",
			data: func() []byte {
				header := make([]byte, 16)
				copy(header, proxyV2HeaderPrefix)
				header[12] = 0x21 // version 2, PROXY command
				header[13] = 0x11 // IPv4, STREAM
				binary.BigEndian.PutUint16(header[14:], 8)
				// Add 8 bytes of address data (need 12 for IPv4)
				return append(header, []byte{192, 168, 1, 1, 10, 0, 0, 1}...)
			}(),
			expectedBytes: 24, // 16 header + 8 bytes
			expectError:   true,
		},
		{
			name:          "V1 invalid header format",
			data:          []byte("INVALID HEADER\r\n"),
			expectedBytes: 16,
			expectError:   true,
		},
		{
			name:          "V1 incomplete header",
			data:          []byte("PROXY TCP4 192.168.1.1\r\n"),
			expectedBytes: 24,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bufio.NewReader(bytes.NewReader(tt.data))

			var info *ProxyProtocolInfo
			var bytesRead int
			var err error

			// Determine which parser to use based on first byte
			firstByte, _ := reader.Peek(1)
			if len(firstByte) > 0 {
				if firstByte[0] == 'P' {
					info, bytesRead, err = parseProxyV1(reader)
				} else {
					info, bytesRead, err = parseProxyV2(reader)
				}
			}

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none, info=%+v", info)
			}

			if bytesRead != tt.expectedBytes {
				t.Errorf("Expected %d bytes consumed, got %d", tt.expectedBytes, bytesRead)
			}
		})
	}
}

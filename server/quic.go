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
	"context"
	"net"
	"strconv"
	"time"

	"github.com/quic-go/quic-go"
)

// srvQUIC holds information for QUIC server
type srvQUIC struct {
	listener    *quic.Listener
	listenerErr error
}

// startQUICServer starts the QUIC server.
func (s *Server) startQUICServer() {
	s.mu.Lock()
	opts := s.opts
	s.mu.Unlock()

	o := &opts.QUIC

	if o.Port == 0 {
		return
	}

	hp := net.JoinHostPort(o.Host, strconv.Itoa(o.Port))

	s.Noticef("Starting QUIC server on %s", hp)

	// QUIC requires TLS
	tlsConfig := o.TLSConfig
	if tlsConfig == nil {
		s.Fatalf("QUIC requires TLS configuration")
		return
	}

	// Clone TLS config to avoid modifying the original
	config := tlsConfig.Clone()

	// Set ALPN for NATS protocol
	config.NextProtos = []string{"nats"}

	// Listen for QUIC connections
	listener, err := quic.ListenAddr(hp, config, nil)
	if err != nil {
		s.Fatalf("Error listening for QUIC connections on %s: %v", hp, err)
		return
	}

	s.mu.Lock()
	s.quic.listener = listener
	s.quic.listenerErr = err

	// Resolve advertise address
	if o.Advertise != "" {
		s.opts.QUIC.Advertise = o.Advertise
	} else {
		// Use the listener address if no advertise address is specified
		if o.Host == "" || o.Host == "0.0.0.0" || o.Host == "::" {
			// Get local IP if listening on all interfaces
			if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
				if localAddr := conn.LocalAddr().(*net.UDPAddr); localAddr != nil {
					s.opts.QUIC.Advertise = net.JoinHostPort(localAddr.IP.String(), strconv.Itoa(o.Port))
				}
				conn.Close()
			}
		} else {
			s.opts.QUIC.Advertise = hp
		}
	}
	s.mu.Unlock()

	s.Noticef("Listening for QUIC client connections on %s", hp)
	if s.opts.QUIC.Advertise != hp {
		s.Noticef("Advertise address for QUIC client connections is %s", s.opts.QUIC.Advertise)
	}

	// Start accepting QUIC connections in a goroutine
	s.startGoRoutine(func() {
		defer s.grWG.Done()
		s.acceptQUICConnections(listener)
	})
}

// acceptQUICConnections accepts QUIC connections and creates clients
func (s *Server) acceptQUICConnections(listener *quic.Listener) {
	for {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			select {
			case <-s.quitCh:
				return
			default:
			}

			s.Errorf("Error accepting QUIC connection: %v", err)
			continue
		}

		// Create client in goroutine
		if !s.startGoRoutine(func() {
			s.reloadMu.RLock()
			s.createQUICClient(conn)
			s.reloadMu.RUnlock()
			s.grWG.Done()
		}) {
			conn.CloseWithError(0, "server shutdown")
		}
	}
}

// createQUICClient creates a new NATS client from a QUIC connection
func (s *Server) createQUICClient(conn quic.Connection) {
	// Open a bidirectional stream for the NATS protocol
	stream, err := conn.AcceptStream(context.Background())
	if err != nil {
		s.Errorf("Error accepting QUIC stream: %v", err)
		conn.CloseWithError(0, "failed to accept stream")
		return
	}

	// Wrap the QUIC stream to implement net.Conn interface
	netConn := &quicStreamConn{
		stream: stream,
		conn:   conn,
	}

	// Create a standard NATS client using the wrapped connection
	s.createClient(netConn)
}

// quicStreamConn wraps a QUIC stream to implement net.Conn
type quicStreamConn struct {
	stream quic.Stream
	conn   quic.Connection
}

func (qsc *quicStreamConn) Read(b []byte) (int, error) {
	return qsc.stream.Read(b)
}

func (qsc *quicStreamConn) Write(b []byte) (int, error) {
	return qsc.stream.Write(b)
}

func (qsc *quicStreamConn) Close() error {
	return qsc.stream.Close()
}

func (qsc *quicStreamConn) LocalAddr() net.Addr {
	return qsc.conn.LocalAddr()
}

func (qsc *quicStreamConn) RemoteAddr() net.Addr {
	return qsc.conn.RemoteAddr()
}

func (qsc *quicStreamConn) SetDeadline(t time.Time) error {
	return qsc.stream.SetDeadline(t)
}

func (qsc *quicStreamConn) SetReadDeadline(t time.Time) error {
	return qsc.stream.SetReadDeadline(t)
}

func (qsc *quicStreamConn) SetWriteDeadline(t time.Time) error {
	return qsc.stream.SetWriteDeadline(t)
}

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
		// Check if server is shutting down before accepting
		if !s.isRunning() {
			break
		}

		conn, err := listener.Accept(context.Background())
		if err != nil {
			// Check if we're shutting down - this is the most common case
			// when the listener is closed
			if !s.isRunning() {
				break
			}

			// Check for lame duck mode
			if s.isLameDuckMode() {
				s.ldmCh <- true
				<-s.quitCh
				break
			}

			// Check quit channel
			select {
			case <-s.quitCh:
				break
			default:
			}

			// If we're still running, this might be a real error
			if s.isRunning() {
				s.Errorf("Error accepting QUIC connection: %v", err)
				// Brief pause to avoid tight loop on persistent errors
				time.Sleep(10 * time.Millisecond)
			}
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
	
	s.Debugf("QUIC accept loop exiting..")
	s.done <- true
}

// createQUICClient creates a new NATS client from a QUIC connection
func (s *Server) createQUICClient(conn quic.Connection) {
	// Accept stream without timeout to avoid blocking under high load
	stream, err := conn.AcceptStream(context.Background())
	if err != nil {
		s.Errorf("Error accepting QUIC stream: %v", err)
		conn.CloseWithError(0, "failed to accept stream")
		return
	}

	// Create QUIC-native client directly without wrapper overhead
	s.createQUICClientDirect(conn, stream)
}

// createQUICClientDirect creates a QUIC client without wrapper overhead
func (s *Server) createQUICClientDirect(conn quic.Connection, stream quic.Stream) {
	// Snapshot server options.
	opts := s.getOpts()

	maxPay := int32(opts.MaxPayload)
	maxSubs := int32(opts.MaxSubs)
	// For system, maxSubs of 0 means unlimited, so re-adjust here.
	if maxSubs == 0 {
		maxSubs = -1
	}
	now := time.Now()

	// Create QUIC-native client with direct stream access
	c := &quicClient{
		client: client{
			srv:   s,
			nc:    &quicStreamConn{stream: stream, conn: conn},
			opts:  defaultOpts,
			mpay:  maxPay,
			msubs: maxSubs,
			start: now,
			last:  now,
		},
		quicConn: conn,
		quicStream: stream,
	}

	c.registerWithAccount(s.globalAccount())

	var info Info
	var authRequired bool

	s.mu.Lock()
	// Grab JSON info string
	info = s.copyInfo()
	if s.nonceRequired() {
		// Nonce handling
		var raw [nonceLen]byte
		nonce := raw[:]
		s.generateNonce(nonce)
		info.Nonce = string(nonce)
	}
	c.nonce = []byte(info.Nonce)
	authRequired = info.AuthRequired

	// Check to see if we have auth_required set but we also have a no_auth_user.
	if info.AuthRequired && opts.NoAuthUser != _EMPTY_ && opts.NoAuthUser != s.sysAccOnlyNoAuthUser {
		info.AuthRequired = false
	}

	s.totalClients++
	s.mu.Unlock()

	// Grab lock
	c.mu.Lock()
	if authRequired {
		c.flags.set(expectConnect)
	}

	// Initialize QUIC client
	c.initQUICClient()

	c.Debugf("QUIC client connection created")

	// Generate INFO json for QUIC
	infoBytes := c.generateClientInfoJSON(info)
	c.sendProtoNow(infoBytes)

	// Unlock to register
	c.mu.Unlock()

	// Register with the server
	s.mu.Lock()
	c.cid = s.gcid
	s.gcid++
	s.clients[c.cid] = &c.client
	s.mu.Unlock()

	// Set the Ping timer
	c.setPingTimer()

	// Start QUIC-optimized read/write loops using server's goroutine manager
	s.startGoRoutine(func() { c.quicReadLoop() })
	s.startGoRoutine(func() { c.quicWriteLoop() })
}

// quicClient extends client with QUIC-specific optimizations
type quicClient struct {
	client
	quicConn   quic.Connection
	quicStream quic.Stream
}

// initQUICClient initializes QUIC-specific client state
func (c *quicClient) initQUICClient() {
	c.initClient()
	// Additional QUIC-specific initialization can go here
}


// quicReadLoop optimized read loop for QUIC streams
func (c *quicClient) quicReadLoop() {
	// Use the existing readLoop but with QUIC stream directly
	c.readLoop(nil)
}

// quicWriteLoop optimized write loop for QUIC streams
func (c *quicClient) quicWriteLoop() {
	// Use the existing writeLoop but with QUIC stream directly
	c.writeLoop()
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

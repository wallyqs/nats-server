// Copyright 2017 Apcera Inc. All rights reserved.

// A Go client for the NATS messaging system (https://nats.io).
package server

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
)

const defaultBufSize = 32768

// bufioWriter is a patched bufio.Writer used to avoid escaping
// pub command payloads due to internal interface conversion.
type bufioWriter struct {
	err error
	buf []byte
	n   int
	wr  io.Writer

	// conn is the concrete type of the writer
	// when we are connected, in order to be able prevent
	// escaping pub payloads via bufio.Write()
	// https://github.com/golang/go/issues/5492
	conn *net.TCPConn

	// pending is the concrete type of the writer
	// when we are reconnecting.
	pending *bytes.Buffer

	// sconn represents the secure connection and concrete type
	// of the bufio.Writer.
	sconn *tls.Conn
}

// Rest is based from commit at:
// https://github.com/golang/go/blob/ae238688d2813e83f16050408487ea34ba1c2fff/src/bufio/bufio.go#L516-L724

// NewBufioWriterSize returns a new Writer whose buffer has at least the specified
// size. If the argument io.Writer is already a Writer with large enough
// size, it returns the underlying Writer.
func NewBufioWriterSize(w io.Writer, size int) *bufioWriter {
	// Is it already a Writer?
	b, ok := w.(*bufioWriter)
	if ok && len(b.buf) >= size {
		return b
	}
	if size <= 0 {
		// 32K bytes by default
		size = defaultBufSize
	}

	// Grab the concrete type of the TCP connection
	// to bypass the interface.
	var bwr *bufioWriter
	if conn, ok := w.(*net.TCPConn); ok {
		bwr = &bufioWriter{
			buf:  make([]byte, size),
			wr:   w,
			conn: conn,
		}
	} else if buffer, ok := w.(*bytes.Buffer); ok {
		bwr = &bufioWriter{
			buf:     make([]byte, size),
			wr:      w,
			pending: buffer,
		}
	} else if sconn, ok := w.(*tls.Conn); ok {
		bwr = &bufioWriter{
			buf:   make([]byte, size),
			wr:    w,
			sconn: sconn,
		}
	}

	return bwr
}

// Reset discards any unflushed buffered data, clears any error, and
// resets b to write its output to w.
func (b *bufioWriter) Reset(w io.Writer) {
	b.err = nil
	b.n = 0
	b.wr = w
}

// Flush writes any buffered data to the underlying io.Writer.
func (b *bufioWriter) Flush() error {
	if b.err != nil {
		return b.err
	}
	if b.n == 0 {
		return nil
	}
	// Avoid interface and use concrete type directly.
	// n, err := b.wr.Write(b.buf[0:b.n])
	var n int
	var err error
	if b.conn != nil {
		n, err = b.conn.Write(b.buf[0:b.n])
	} else if b.pending != nil {
		n, err = b.pending.Write(b.buf[0:b.n])
	} else if b.sconn != nil {
		n, err = b.sconn.Write(b.buf[0:b.n])
	}

	if n < b.n && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		if n > 0 && n < b.n {
			copy(b.buf[0:b.n-n], b.buf[n:b.n])
		}
		b.n -= n
		b.err = err
		return err
	}
	b.n = 0
	return nil
}

// Available returns how many bytes are unused in the buffer.
func (b *bufioWriter) Available() int { return len(b.buf) - b.n }

// Buffered returns the number of bytes that have been written into the current buffer.
func (b *bufioWriter) Buffered() int { return b.n }

// Write writes the contents of p into the buffer.
// It returns the number of bytes written.
// If nn < len(p), it also returns an error explaining
// why the write is short.
func (b *bufioWriter) Write(p []byte) (nn int, err error) {
	for len(p) > b.Available() && b.err == nil {
		var n int
		// Avoid using Write() to interface here to prevent escaping.
		if b.Buffered() == 0 {
			// Large write, empty buffer.
			// Write directly from p to avoid copy.
			// n, b.err = b.wr.Write(p)

			// Write using concrete type here instead.
			if b.conn != nil {
				n, b.err = b.conn.Write(p)
			} else if b.pending != nil {
				n, b.err = b.pending.Write(p)
			} else if b.sconn != nil {
				n, b.err = b.sconn.Write(p)
			}
		} else {
			n = copy(b.buf[b.n:], p)
			b.n += n
			b.Flush()
		}
		nn += n
		p = p[n:]
	}
	if b.err != nil {
		return nn, b.err
	}
	n := copy(b.buf[b.n:], p)
	b.n += n
	nn += n
	return nn, nil
}

// WriteString writes a string.
// It returns the number of bytes written.
// If the count is less than len(s), it also returns an error explaining
// why the write is short.
func (b *bufioWriter) WriteString(s string) (int, error) {
	nn := 0
	for len(s) > b.Available() && b.err == nil {
		n := copy(b.buf[b.n:], s)
		b.n += n
		nn += n
		s = s[n:]
		b.Flush()
	}
	if b.err != nil {
		return nn, b.err
	}
	n := copy(b.buf[b.n:], s)
	b.n += n
	nn += n
	return nn, nil
}

// Copyright 2012-2014 Apcera Inc. All rights reserved.

package test

import (
	"testing"
	"time"

	"github.com/nats-io/gnatsd/server"
)

func runPedanticServer() *server.Server {
	opts := DefaultTestOptions

	opts.NoLog = false
	opts.Trace = true

	opts.Port = PROTO_TEST_PORT
	return RunServer(&opts)
}

func TestPedanticSub(t *testing.T) {
	s := runPedanticServer()
	defer s.Shutdown()

	c := createClientConn(t, "localhost", PROTO_TEST_PORT)
	defer c.Close()

	send := sendCommand(t, c)
	expect := expectCommand(t, c)
	doConnect(t, c, false, true, false)

	// Ping should still be same
	send("PING\r\n")
	expect(pongRe)

	// Test malformed subjects for SUB
	// Sub can contain wildcards, but
	// subject must still be legit.

	// Empty terminal token
	send("SUB foo. 1\r\n")
	expect(errRe)

	// Empty beginning token
	send("SUB .foo. 1\r\n")
	expect(errRe)

	// Empty middle token
	send("SUB foo..bar 1\r\n")
	expect(errRe)

	// Bad non-terminal FWC
	send("SUB foo.>.bar 1\r\n")
	buf := expect(errRe)

	// Check that itr is 'Invalid Subject'
	matches := errRe.FindAllSubmatch(buf, -1)
	if len(matches) != 1 {
		t.Fatal("Wanted one overall match")
	}
	if string(matches[0][1]) != "'Invalid Subject'" {
		t.Fatalf("Expected 'Invalid Subject', got %s", string(matches[0][1]))
	}
}

func TestPedanticPub(t *testing.T) {
	s := runPedanticServer()
	defer s.Shutdown()

	c := createClientConn(t, "localhost", PROTO_TEST_PORT)
	defer c.Close()

	send := sendCommand(t, c)
	expect := expectCommand(t, c)
	doConnect(t, c, false, true, false)

	// Ping should still be same
	send("PING\r\n")
	expect(pongRe)

	// Test malformed subjects for PUB
	// PUB subjects can not have wildcards
	// This will error in pedantic mode
	send("PUB foo.* 2\r\n")
	expect(errRe)

	send("PUB foo.> 2\r\n")
	expect(errRe)

	send("PUB foo. 2\r\n")
	expect(errRe)

	send("PUB .foo 2\r\n")
	expect(errRe)

	send("PUB foo..* 2\r\n")
	expect(errRe)
}

func TestPedanticPubMsgNotSent(t *testing.T) {
	s := runProtoServer()
	defer s.Shutdown()

	c := createClientConn(t, "localhost", PROTO_TEST_PORT)
	defer c.Close()

	send := sendCommand(t, c)
	expect := expectCommand(t, c)
	doConnect(t, c, false, true, false)

	send("SUB > 90\r\n")
	send("PUB > 11\r\n")
	expect(errRe)

	// Protocol line with enough bytes to conform
	// to the msg payload from the bad pub command.
	send("SUB hello 5\r\n")
	send("PUB hello 2\r\nok\r\n")

	// Wait for responses
	time.Sleep(250 * time.Millisecond)

	expectMsgs := expectMsgsCommand(t, expect)
	matches := expectMsgs(2)
	checkMsg(t, matches[0], "hello", "90", "", "2", "ok")
	checkMsg(t, matches[1], "hello", "5", "", "2", "ok")
}

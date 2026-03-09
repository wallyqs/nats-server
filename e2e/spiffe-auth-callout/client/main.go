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

// Package main is an example NATS client that authenticates using a
// SPIFFE X509-SVID certificate. It connects to a NATS server with auth
// callout enabled, demonstrates publish/subscribe, and shows the
// permissions granted based on its SPIFFE identity.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

func main() {
	var (
		natsURL  = flag.String("nats", "tls://localhost:4222", "NATS server URL")
		caFile   = flag.String("ca", "../../test/configs/certs/svid/ca.pem", "CA certificate file")
		certFile = flag.String("cert", "../../test/configs/certs/svid/client-a.pem", "Client SVID certificate")
		keyFile  = flag.String("key", "../../test/configs/certs/svid/client-a.key", "Client SVID private key")
		subject  = flag.String("subject", "demo.hello", "Subject to publish/subscribe to")
	)
	flag.Parse()

	// Connect using only the X509-SVID certificate — no username, password, or nkey.
	// The NATS server extracts the SPIFFE ID from the certificate and passes it
	// to the auth callout service, which decides what permissions to grant.
	nc, err := nats.Connect(*natsURL,
		nats.RootCAs(*caFile),
		nats.ClientCert(*certFile, *keyFile),
		nats.Name("spiffe-e2e-client"),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Printf("NATS error: %v", err)
		}),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	log.Printf("Connected to %s", nc.ConnectedUrl())

	// Subscribe to the subject to receive messages.
	sub, err := nc.Subscribe(*subject, func(m *nats.Msg) {
		log.Printf("Received on [%s]: %s", m.Subject, string(m.Data))
		if m.Reply != "" {
			m.Respond([]byte("got it!"))
		}
	})
	if err != nil {
		log.Fatalf("Failed to subscribe to %s: %v", *subject, err)
	}
	defer sub.Unsubscribe()
	log.Printf("Subscribed to [%s]", *subject)

	// Publish a message.
	msg := fmt.Sprintf("hello from SPIFFE client at %s", time.Now().Format(time.RFC3339))
	if err := nc.Publish(*subject, []byte(msg)); err != nil {
		log.Fatalf("Failed to publish: %v", err)
	}
	log.Printf("Published to [%s]: %s", *subject, msg)

	// Demonstrate request-reply.
	log.Println("Sending request...")
	resp, err := nc.Request(*subject, []byte("ping"), 2*time.Second)
	if err != nil {
		log.Printf("Request failed (this is expected if no other subscriber): %v", err)
	} else {
		log.Printf("Got reply: %s", string(resp.Data))
	}

	// Try publishing to a subject we shouldn't have access to (should fail silently
	// or get a permissions violation depending on server config).
	if err := nc.Publish("secret.data", []byte("should not work")); err != nil {
		log.Printf("Publish to secret.data failed: %v", err)
	} else {
		log.Println("Published to [secret.data] (server may reject via permissions)")
	}
	nc.Flush()

	// Wait so we can see any async permission errors.
	time.Sleep(500 * time.Millisecond)

	log.Println("Client running. Press Ctrl+C to exit.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("Shutting down client")

	// If you want to test that different SPIFFE IDs get different permissions,
	// run this client with client-b's certificate:
	//
	//   go run . -cert ../../test/configs/certs/svid/client-b.pem \
	//            -key ../../test/configs/certs/svid/client-b.key
	//
	// client-b has a different SPIFFE ID (spiffe://localhost/my-nats-service/user-b)
	// and will receive different permissions from the auth callout service.
	_ = os.Stderr // suppress unused import
}

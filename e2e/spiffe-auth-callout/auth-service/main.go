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

// Package main implements a NATS auth callout service that maps SPIFFE
// identities to NATS permissions. It subscribes to $SYS.REQ.USER.AUTH,
// extracts the SPIFFE ID from the authorization request, looks up
// permissions from a JSON policy file, and returns a signed user JWT.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// issuerSeed is the account nkey seed used to sign authorization responses.
// In production this would come from a secure secret store.
// This matches the issuer configured in the NATS server auth_callout block.
const issuerSeed = "SAANDLKMXL6CUS3CP52WIXBEDN6YJ545GDKC65U5JZPPV6WH6ESWUA6YAI"

// Policy describes the permissions granted to a specific SPIFFE identity.
type Policy struct {
	Account     string      `json:"account"`
	Name        string      `json:"name"`
	Permissions Permissions `json:"permissions"`
}

// Permissions holds publish and subscribe permission lists.
type Permissions struct {
	Pub SubjectList `json:"pub"`
	Sub SubjectList `json:"sub"`
}

// SubjectList holds an allow list of NATS subjects.
type SubjectList struct {
	Allow []string `json:"allow"`
}

func main() {
	var (
		natsURL    = flag.String("nats", "tls://localhost:4222", "NATS server URL")
		policyFile = flag.String("policies", "policies.json", "Path to the SPIFFE-to-permissions policy file")
		caFile     = flag.String("ca", "../../test/configs/certs/svid/ca.pem", "CA certificate file")
		certFile   = flag.String("cert", "../../test/configs/certs/svid/client-a.pem", "Client certificate file")
		keyFile    = flag.String("key", "../../test/configs/certs/svid/client-a.key", "Client key file")
	)
	flag.Parse()

	// Load policies from file.
	policies, err := loadPolicies(*policyFile)
	if err != nil {
		log.Fatalf("Failed to load policies: %v", err)
	}
	log.Printf("Loaded %d SPIFFE policies", len(policies))
	for id := range policies {
		log.Printf("  - %s", id)
	}

	// Load the issuer signing key.
	issuerKP, err := nkeys.FromSeed([]byte(issuerSeed))
	if err != nil {
		log.Fatalf("Failed to load issuer key: %v", err)
	}

	// Connect to NATS as the auth service user.
	nc, err := nats.Connect(*natsURL,
		nats.UserInfo("auth", "auth-secret"),
		nats.RootCAs(*caFile),
		nats.ClientCert(*certFile, *keyFile),
		nats.Name("spiffe-auth-callout-service"),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()
	log.Printf("Connected to NATS at %s", nc.ConnectedUrl())

	// Subscribe to the auth callout subject.
	sub, err := nc.Subscribe("$SYS.REQ.USER.AUTH", func(m *nats.Msg) {
		handleAuthRequest(m, policies, issuerKP)
	})
	if err != nil {
		log.Fatalf("Failed to subscribe to auth callout: %v", err)
	}
	defer sub.Unsubscribe()

	log.Println("Auth callout service is running. Waiting for requests...")

	// Wait for shutdown signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("Shutting down auth callout service")
}

func handleAuthRequest(m *nats.Msg, policies map[string]*Policy, issuerKP nkeys.KeyPair) {
	// Decode the authorization request claims from the message.
	rc, err := jwt.DecodeAuthorizationRequestClaims(string(m.Data))
	if err != nil {
		log.Printf("ERROR: failed to decode auth request: %v", err)
		respondWithError(m, "", "", "failed to decode request", issuerKP)
		return
	}

	userNkey := rc.UserNkey
	serverID := rc.Server.ID
	ci := &rc.ClientInformation

	log.Printf("Auth request: user=%q host=%s server=%s",
		ci.User, ci.Host, rc.Server.Name)

	// Extract the SPIFFE ID. The server sets it as the user field when
	// no other identity is provided. We also check the tags.
	spiffeID := ""
	if strings.HasPrefix(ci.User, "spiffe://") {
		spiffeID = ci.User
	}

	// Also check tags for SPIFFE IDs.
	for _, tag := range rc.Tags {
		if strings.HasPrefix(tag, "spiffe-id:") {
			tagID := strings.TrimPrefix(tag, "spiffe-id:")
			if spiffeID == "" {
				spiffeID = tagID
			}
			log.Printf("  SPIFFE ID (tag): %s", tagID)
		}
	}

	if spiffeID == "" {
		log.Printf("DENY: no SPIFFE identity found for client %s", ci.Host)
		respondWithError(m, userNkey, serverID, "no SPIFFE identity", issuerKP)
		return
	}

	log.Printf("  SPIFFE ID: %s", spiffeID)

	// Look up the policy for this SPIFFE ID.
	policy, ok := policies[spiffeID]
	if !ok {
		log.Printf("DENY: no policy for SPIFFE ID %s", spiffeID)
		respondWithError(m, userNkey, serverID,
			fmt.Sprintf("no policy for SPIFFE ID: %s", spiffeID), issuerKP)
		return
	}

	// Build the user JWT with the permissions from the policy.
	uc := jwt.NewUserClaims(userNkey)
	uc.Name = policy.Name
	uc.Audience = policy.Account

	for _, subj := range policy.Permissions.Pub.Allow {
		uc.Pub.Allow.Add(subj)
	}
	for _, subj := range policy.Permissions.Sub.Allow {
		uc.Sub.Allow.Add(subj)
	}

	// Allow service responses for request-reply patterns.
	uc.Resp = &jwt.ResponsePermission{
		MaxMsgs: 1,
		Expires: 5 * time.Second,
	}

	userJWT, err := uc.Encode(issuerKP)
	if err != nil {
		log.Printf("ERROR: failed to encode user JWT: %v", err)
		respondWithError(m, userNkey, serverID, "internal error", issuerKP)
		return
	}

	log.Printf("ALLOW: %s -> account=%s name=%s pub=%v sub=%v",
		spiffeID, policy.Account, policy.Name,
		policy.Permissions.Pub.Allow, policy.Permissions.Sub.Allow)

	// Build and send the authorization response.
	cr := jwt.NewAuthorizationResponseClaims(userNkey)
	cr.Audience = serverID
	cr.Jwt = userJWT

	token, err := cr.Encode(issuerKP)
	if err != nil {
		log.Printf("ERROR: failed to encode auth response: %v", err)
		return
	}
	m.Respond([]byte(token))
}

func respondWithError(m *nats.Msg, userNkey, serverID, errMsg string, issuerKP nkeys.KeyPair) {
	if userNkey == "" {
		userNkey = "unknown"
	}
	cr := jwt.NewAuthorizationResponseClaims(userNkey)
	cr.Audience = serverID
	cr.Error = errMsg
	token, err := cr.Encode(issuerKP)
	if err != nil {
		log.Printf("ERROR: failed to encode error response: %v", err)
		return
	}
	m.Respond([]byte(token))
}

func loadPolicies(path string) (map[string]*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading policy file: %w", err)
	}
	var policies map[string]*Policy
	if err := json.Unmarshal(data, &policies); err != nil {
		return nil, fmt.Errorf("parsing policy file: %w", err)
	}
	return policies, nil
}

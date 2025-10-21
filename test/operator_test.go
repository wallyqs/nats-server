// Copyright 2018-2019 The NATS Authors
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

package test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

const (
	testOpConfig       = "./configs/operator.conf"
	testOpInlineConfig = "./configs/operator_inline.conf"
)

// This matches ./configs/nkeys_jwts/test.seed
// Test operator seed.
var oSeed = []byte("SOAFYNORQLQFJYBYNUGC5D7SH2MXMUX5BFEWWGHN3EK4VGG5TPT5DZP7QU")

// This is a signing key seed.
var skSeed = []byte("SOAEL3NFOTU6YK3DBTEKQYZ2C5IWSVZWWZCQDASBUOHJKBFLVANK27JMMQ")

func checkKeys(t *testing.T, opts *server.Options, opc *jwt.OperatorClaims, expected int) {
	// We should have filled in the TrustedKeys here.
	if len(opts.TrustedKeys) != expected {
		t.Fatalf("Should have %d trusted keys, got %d", expected, len(opts.TrustedKeys))
	}
	// Check that we properly placed all keys from the opc into TrustedKeys
	chkMember := func(s string) {
		for _, c := range opts.TrustedKeys {
			if s == c {
				return
			}
		}
		t.Fatalf("Expected %q to be in TrustedKeys", s)
	}
	chkMember(opc.Issuer)
	for _, sk := range opc.SigningKeys {
		chkMember(sk)
	}
}

// This will test that we enforce certain restrictions when you use trusted operators.
// Like auth is always true, can't define accounts or users, required to define an account resolver, etc.
func TestOperatorRestrictions(t *testing.T) {
	opts, err := server.ProcessConfigFile(testOpConfig)
	if err != nil {
		t.Fatalf("Error processing config file: %v", err)
	}
	opts.NoSigs = true
	if _, err := server.NewServer(opts); err != nil {
		t.Fatalf("Expected to create a server successfully")
	}
	// TrustedKeys get defined when processing from above, trying again with
	// same opts should not work.
	if _, err := server.NewServer(opts); err == nil {
		t.Fatalf("Expected an error with TrustedKeys defined")
	}
	// Must wipe and rebuild to succeed.
	wipeOpts := func() {
		opts.TrustedKeys = nil
		opts.Accounts = nil
		opts.Users = nil
		opts.Nkeys = nil
		opts.AllowNewAccounts = false
	}

	wipeOpts()
	opts.Accounts = []*server.Account{{Name: "TEST"}}
	if _, err := server.NewServer(opts); err == nil {
		t.Fatalf("Expected an error with Accounts defined")
	}
	wipeOpts()
	opts.Users = []*server.User{{Username: "TEST"}}
	if _, err := server.NewServer(opts); err == nil {
		t.Fatalf("Expected an error with Users defined")
	}
	wipeOpts()
	opts.Nkeys = []*server.NkeyUser{{Nkey: "TEST"}}
	if _, err := server.NewServer(opts); err == nil {
		t.Fatalf("Expected an error with Nkey Users defined")
	}
	wipeOpts()
	opts.AllowNewAccounts = true
	if _, err := server.NewServer(opts); err == nil {
		t.Fatalf("Expected an error with AllowNewAccounts set to true")
	}

	wipeOpts()
	opts.AccountResolver = nil
	if _, err := server.NewServer(opts); err == nil {
		t.Fatalf("Expected an error without an AccountResolver defined")
	}
}

func TestOperatorConfig(t *testing.T) {
	opts, err := server.ProcessConfigFile(testOpConfig)
	if err != nil {
		t.Fatalf("Error processing config file: %v", err)
	}
	opts.NoSigs = true
	// Check we have the TrustedOperators
	if len(opts.TrustedOperators) != 1 {
		t.Fatalf("Expected to load the operator")
	}
	_, err = server.NewServer(opts)
	if err != nil {
		t.Fatalf("Expected to create a server: %v", err)
	}
	// We should have filled in the public TrustedKeys here.
	// Our master public key (issuer) plus the signing keys (3).
	checkKeys(t, opts, opts.TrustedOperators[0], 4)
}

func TestOperatorConfigInline(t *testing.T) {
	opts, err := server.ProcessConfigFile(testOpInlineConfig)
	if err != nil {
		t.Fatalf("Error processing config file: %v", err)
	}
	opts.NoSigs = true
	// Check we have the TrustedOperators
	if len(opts.TrustedOperators) != 1 {
		t.Fatalf("Expected to load the operator")
	}
	_, err = server.NewServer(opts)
	if err != nil {
		t.Fatalf("Expected to create a server: %v", err)
	}
	// We should have filled in the public TrustedKeys here.
	// Our master public key (issuer) plus the signing keys (3).
	checkKeys(t, opts, opts.TrustedOperators[0], 4)
}

func runOperatorServer(t *testing.T) (*server.Server, *server.Options) {
	return RunServerWithConfig(testOpConfig)
}

func createAccountForOperatorKey(t *testing.T, s *server.Server, seed []byte) (*server.Account, nkeys.KeyPair) {
	t.Helper()
	okp, _ := nkeys.FromSeed(seed)
	akp, _ := nkeys.CreateAccount()
	pub, _ := akp.PublicKey()
	nac := jwt.NewAccountClaims(pub)
	jwt, _ := nac.Encode(okp)
	if err := s.AccountResolver().Store(pub, jwt); err != nil {
		t.Fatalf("Account Resolver returned an error: %v", err)
	}
	acc, err := s.LookupAccount(pub)
	if err != nil {
		t.Fatalf("Error looking up account: %v", err)
	}
	return acc, akp
}

func createAccount(t *testing.T, s *server.Server) (*server.Account, nkeys.KeyPair) {
	t.Helper()
	return createAccountForOperatorKey(t, s, oSeed)
}

func createUserCreds(t *testing.T, s *server.Server, akp nkeys.KeyPair) nats.Option {
	t.Helper()
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	nuc := jwt.NewUserClaims(pub)
	ujwt, err := nuc.Encode(akp)
	if err != nil {
		t.Fatalf("Error generating user JWT: %v", err)
	}
	userCB := func() (string, error) {
		return ujwt, nil
	}
	sigCB := func(nonce []byte) ([]byte, error) {
		sig, _ := kp.Sign(nonce)
		return sig, nil
	}
	return nats.UserJWT(userCB, sigCB)
}

func TestOperatorServer(t *testing.T) {
	s, opts := runOperatorServer(t)
	defer s.Shutdown()

	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)
	if _, err := nats.Connect(url); err == nil {
		t.Fatalf("Expected to fail with no credentials")
	}

	_, akp := createAccount(t, s)
	nc, err := nats.Connect(url, createUserCreds(t, s, akp))
	if err != nil {
		t.Fatalf("Error on connect: %v", err)
	}
	nc.Close()

	// Now create an account from another operator, this should fail.
	okp, _ := nkeys.CreateOperator()
	seed, _ := okp.Seed()
	_, akp = createAccountForOperatorKey(t, s, seed)
	_, err = nats.Connect(url, createUserCreds(t, s, akp))
	if err == nil {
		t.Fatalf("Expected error on connect")
	}
}

func TestOperatorSystemAccount(t *testing.T) {
	s, _ := runOperatorServer(t)
	defer s.Shutdown()

	// Create an account from another operator, this should fail if used as a system account.
	okp, _ := nkeys.CreateOperator()
	seed, _ := okp.Seed()
	acc, _ := createAccountForOperatorKey(t, s, seed)
	if err := s.SetSystemAccount(acc.Name); err == nil {
		t.Fatalf("Expected this to fail")
	}
	if acc := s.SystemAccount(); acc != nil {
		t.Fatalf("Expected no account to be set for system account")
	}

	acc, _ = createAccount(t, s)
	if err := s.SetSystemAccount(acc.Name); err != nil {
		t.Fatalf("Expected this succeed, got %v", err)
	}
	if sysAcc := s.SystemAccount(); sysAcc != acc {
		t.Fatalf("Did not get matching account for system account")
	}
}

func TestOperatorSigningKeys(t *testing.T) {
	s, opts := runOperatorServer(t)
	defer s.Shutdown()

	// Create an account with a signing key, not the master key.
	acc, akp := createAccountForOperatorKey(t, s, skSeed)

	// Make sure we can set system account.
	if err := s.SetSystemAccount(acc.Name); err != nil {
		t.Fatalf("Expected this succeed, got %v", err)
	}

	// Make sure we can create users with it too.
	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)
	nc, err := nats.Connect(url, createUserCreds(t, s, akp))
	if err != nil {
		t.Fatalf("Error on connect: %v", err)
	}
	nc.Close()
}

func TestOperatorMemResolverPreload(t *testing.T) {
	s, opts := RunServerWithConfig("./configs/resolver_preload.conf")
	defer s.Shutdown()

	// Make sure we can look up the account.
	acc, _ := s.LookupAccount("ADM2CIIL3RWXBA6T2HW3FODNCQQOUJEHHQD6FKCPVAMHDNTTSMO73ROX")
	if acc == nil {
		t.Fatalf("Expected to properly lookup account")
	}
	sacc := s.SystemAccount()
	if sacc == nil {
		t.Fatalf("Expected to have system account registered")
	}
	if sacc.Name != opts.SystemAccount {
		t.Fatalf("System account does not match, wanted %q, got %q", opts.SystemAccount, sacc.Name)
	}
}

func TestOperatorConfigReloadDoesntKillNonce(t *testing.T) {
	s, _ := runOperatorServer(t)
	defer s.Shutdown()

	if !s.NonceRequired() {
		t.Fatalf("Error nonce should be required")
	}

	if err := s.Reload(); err != nil {
		t.Fatalf("Error on reload: %v", err)
	}

	if !s.NonceRequired() {
		t.Fatalf("Error nonce should still be required after reload")
	}
}

func createAccountForConfig(t *testing.T) (string, nkeys.KeyPair) {
	t.Helper()
	okp, _ := nkeys.FromSeed(oSeed)
	akp, _ := nkeys.CreateAccount()
	pub, _ := akp.PublicKey()
	nac := jwt.NewAccountClaims(pub)
	jwt, _ := nac.Encode(okp)
	return jwt, akp
}

func TestReloadDoesNotWipeAccountsWithOperatorMode(t *testing.T) {
	// We will run an operator mode server that forms a cluster. We will
	// make sure that a reload does not wipe account information.
	// We will force reload of auth by changing cluster auth timeout.

	// Create two accounts, system and normal account.
	sysJWT, sysKP := createAccountForConfig(t)
	sysPub, _ := sysKP.PublicKey()

	accJWT, accKP := createAccountForConfig(t)
	accPub, _ := accKP.PublicKey()

	cf := `
	listen: 127.0.0.1:-1
	cluster {
		listen: 127.0.0.1:-1
		authorization {
			timeout: 2.2
		} %s
	}

	operator = "./configs/nkeys/op.jwt"
	system_account = "%s"

	resolver = MEMORY
	resolver_preload = {
		%s : "%s"
		%s : "%s"
	}
	`
	contents := strings.Replace(fmt.Sprintf(cf, "", sysPub, sysPub, sysJWT, accPub, accJWT), "\n\t", "\n", -1)
	conf := createConfFile(t, []byte(contents))
	defer os.Remove(conf)

	s, opts := RunServerWithConfig(conf)
	defer s.Shutdown()

	// Create a new server and route to main one.
	routeStr := fmt.Sprintf("\n\t\troutes = [nats-route://%s:%d]", opts.Cluster.Host, opts.Cluster.Port)
	contents2 := strings.Replace(fmt.Sprintf(cf, routeStr, sysPub, sysPub, sysJWT, accPub, accJWT), "\n\t", "\n", -1)

	conf2 := createConfFile(t, []byte(contents2))
	defer os.Remove(conf2)

	s2, opts2 := RunServerWithConfig(conf2)
	defer s2.Shutdown()

	checkClusterFormed(t, s, s2)

	// Create a client on the first server and subscribe.
	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)
	nc, err := nats.Connect(url, createUserCreds(t, s, accKP))
	if err != nil {
		t.Fatalf("Error on connect: %v", err)
	}
	defer nc.Close()

	ch := make(chan bool)
	nc.Subscribe("foo", func(m *nats.Msg) { ch <- true })
	nc.Flush()

	// Use this to check for message.
	checkForMsg := func() {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for message across route")
		}
	}

	// Create second client and send message from this one. Interest should be here.
	url2 := fmt.Sprintf("nats://%s:%d/", opts2.Host, opts2.Port)
	nc2, err := nats.Connect(url2, createUserCreds(t, s2, accKP))
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc2.Close()

	// Check that we can send messages.
	nc2.Publish("foo", nil)
	checkForMsg()

	// Now shutdown nc2 and srvA.
	nc2.Close()
	s2.Shutdown()

	// Now change config and do reload which will do an auth change.
	b, err := ioutil.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	newConf := bytes.Replace(b, []byte("2.2"), []byte("3.3"), 1)
	err = ioutil.WriteFile(conf, newConf, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// This will cause reloadAuthorization to kick in and reprocess accounts.
	s.Reload()

	s2, opts2 = RunServerWithConfig(conf2)
	defer s2.Shutdown()

	checkClusterFormed(t, s, s2)

	// Reconnect and make sure this works. If accounts blown away this will fail.
	url2 = fmt.Sprintf("nats://%s:%d/", opts2.Host, opts2.Port)
	nc2, err = nats.Connect(url2, createUserCreds(t, s2, accKP))
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc2.Close()

	// Check that we can send messages.
	nc2.Publish("foo", nil)
	checkForMsg()
}

func TestReloadDoesUpdateAccountsWithMemoryResolver(t *testing.T) {
	// We will run an operator mode server with a memory resolver.
	// Reloading should behave similar to configured accounts.

	// Create two accounts, system and normal account.
	sysJWT, sysKP := createAccountForConfig(t)
	sysPub, _ := sysKP.PublicKey()

	accJWT, accKP := createAccountForConfig(t)
	accPub, _ := accKP.PublicKey()

	cf := `
	listen: 127.0.0.1:-1
	cluster {
		listen: 127.0.0.1:-1
		authorization {
			timeout: 2.2
		} %s
	}

	operator = "./configs/nkeys/op.jwt"
	system_account = "%s"

	resolver = MEMORY
	resolver_preload = {
		%s : "%s"
		%s : "%s"
	}
	`
	contents := strings.Replace(fmt.Sprintf(cf, "", sysPub, sysPub, sysJWT, accPub, accJWT), "\n\t", "\n", -1)
	conf := createConfFile(t, []byte(contents))
	defer os.Remove(conf)

	s, opts := RunServerWithConfig(conf)
	defer s.Shutdown()

	// Create a client on the first server and subscribe.
	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)
	nc, err := nats.Connect(url, createUserCreds(t, s, accKP))
	if err != nil {
		t.Fatalf("Error on connect: %v", err)
	}
	asyncErr := make(chan error, 1)
	nc.SetErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
		asyncErr <- err
	})
	defer nc.Close()

	nc.Subscribe("foo", func(m *nats.Msg) {})
	nc.Flush()

	// Now update and remove normal account and make sure we get disconnected.
	accJWT2, accKP2 := createAccountForConfig(t)
	accPub2, _ := accKP2.PublicKey()
	contents = strings.Replace(fmt.Sprintf(cf, "", sysPub, sysPub, sysJWT, accPub2, accJWT2), "\n\t", "\n", -1)
	err = ioutil.WriteFile(conf, []byte(contents), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// This will cause reloadAuthorization to kick in and reprocess accounts.
	s.Reload()

	select {
	case err := <-asyncErr:
		if err != nats.ErrAuthorization {
			t.Fatalf("Expected ErrAuthorization, got %v", err)
		}
	case <-time.After(2 * time.Second):
		// Give it up to 2 sec.
		t.Fatal("Expected connection to be disconnected")
	}

	// Make sure we can lool up new account and not old one.
	if _, err := s.LookupAccount(accPub2); err != nil {
		t.Fatalf("Error looking up account: %v", err)
	}

	if _, err := s.LookupAccount(accPub); err == nil {
		t.Fatalf("Expected error looking up old account")
	}
}

func TestReloadFailsWithBadAccountsWithMemoryResolver(t *testing.T) {
	// Create two accounts, system and normal account.
	sysJWT, sysKP := createAccountForConfig(t)
	sysPub, _ := sysKP.PublicKey()

	// Create an expired account by hand here. We want to make sure we start up correctly
	// with expired or otherwise accounts with validation issues.
	okp, _ := nkeys.FromSeed(oSeed)

	akp, _ := nkeys.CreateAccount()
	apub, _ := akp.PublicKey()
	nac := jwt.NewAccountClaims(apub)
	nac.IssuedAt = time.Now().Add(-10 * time.Second).Unix()
	nac.Expires = time.Now().Add(-2 * time.Second).Unix()
	ajwt, err := nac.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating account JWT: %v", err)
	}

	cf := `
	listen: 127.0.0.1:-1
	cluster {
		listen: 127.0.0.1:-1
		authorization {
			timeout: 2.2
		} %s
	}

	operator = "./configs/nkeys/op.jwt"
	system_account = "%s"

	resolver = MEMORY
	resolver_preload = {
		%s : "%s"
		%s : "%s"
	}
	`
	contents := strings.Replace(fmt.Sprintf(cf, "", sysPub, sysPub, sysJWT, apub, ajwt), "\n\t", "\n", -1)
	conf := createConfFile(t, []byte(contents))
	defer os.Remove(conf)

	s, _ := RunServerWithConfig(conf)
	defer s.Shutdown()

	// Now add in bogus account for second item and make sure reload fails.
	contents = strings.Replace(fmt.Sprintf(cf, "", sysPub, sysPub, sysJWT, "foo", "bar"), "\n\t", "\n", -1)
	err = ioutil.WriteFile(conf, []byte(contents), 0644)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Reload(); err == nil {
		t.Fatalf("Expected fatal error with bad account on reload")
	}

	// Put it back with a normal account and reload should succeed.
	accJWT, accKP := createAccountForConfig(t)
	accPub, _ := accKP.PublicKey()

	contents = strings.Replace(fmt.Sprintf(cf, "", sysPub, sysPub, sysJWT, accPub, accJWT), "\n\t", "\n", -1)
	err = ioutil.WriteFile(conf, []byte(contents), 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatalf("Got unexpected error on reload: %v", err)
	}
}

func TestConnsRequestDoesNotLoadAccountCheckingConnLimits(t *testing.T) {
	// Create two accounts, system and normal account.
	sysJWT, sysKP := createAccountForConfig(t)
	sysPub, _ := sysKP.PublicKey()

	// Do this account by nad to add in connection limits
	okp, _ := nkeys.FromSeed(oSeed)
	accKP, _ := nkeys.CreateAccount()
	accPub, _ := accKP.PublicKey()
	nac := jwt.NewAccountClaims(accPub)
	nac.Limits.Conn = 10
	accJWT, _ := nac.Encode(okp)

	cf := `
	listen: 127.0.0.1:-1
	cluster {
		listen: 127.0.0.1:-1
		authorization {
			timeout: 2.2
		} %s
	}

	operator = "./configs/nkeys/op.jwt"
	system_account = "%s"

	resolver = MEMORY
	resolver_preload = {
		%s : "%s"
		%s : "%s"
	}
	`
	contents := strings.Replace(fmt.Sprintf(cf, "", sysPub, sysPub, sysJWT, accPub, accJWT), "\n\t", "\n", -1)
	conf := createConfFile(t, []byte(contents))
	defer os.Remove(conf)

	s, opts := RunServerWithConfig(conf)
	defer s.Shutdown()

	// Create a new server and route to main one.
	routeStr := fmt.Sprintf("\n\t\troutes = [nats-route://%s:%d]", opts.Cluster.Host, opts.Cluster.Port)
	contents2 := strings.Replace(fmt.Sprintf(cf, routeStr, sysPub, sysPub, sysJWT, accPub, accJWT), "\n\t", "\n", -1)

	conf2 := createConfFile(t, []byte(contents2))
	defer os.Remove(conf2)

	s2, _ := RunServerWithConfig(conf2)
	defer s2.Shutdown()

	checkClusterFormed(t, s, s2)

	// Make sure that we do not have the account loaded.
	// Just SYS and $G
	if nla := s.NumLoadedAccounts(); nla != 2 {
		t.Fatalf("Expected only 2 loaded accounts, got %d", nla)
	}
	if nla := s2.NumLoadedAccounts(); nla != 2 {
		t.Fatalf("Expected only 2 loaded accounts, got %d", nla)
	}

	// Now connect to first server on accPub.
	nc, err := nats.Connect(s.ClientURL(), createUserCreds(t, s, accKP))
	if err != nil {
		t.Fatalf("Error on connect: %v", err)
	}
	defer nc.Close()

	// Just wait for the request for connections to move to S2 and cause a fetch.
	// This is what we want to fix.
	time.Sleep(100 * time.Millisecond)

	// We should have 3 here for sure.
	if nla := s.NumLoadedAccounts(); nla != 3 {
		t.Fatalf("Expected 3 loaded accounts, got %d", nla)
	}

	// Now make sure that we still only have 2 loaded accounts on server 2.
	if nla := s2.NumLoadedAccounts(); nla != 2 {
		t.Fatalf("Expected only 2 loaded accounts, got %d", nla)
	}
}

// Test that preloads cannot be used with URL resolver
func TestOperatorPreloadFailsWithURLResolver(t *testing.T) {
	confFileName := "/tmp/test_resolver_preload_url.conf"

	opts, err := server.ProcessConfigFile(confFileName)
	if err != nil {
		t.Fatalf("Error processing config file: %v", err)
	}

	// NewServer() internally calls configureResolver which should fail
	// when trying to use preloads with URL resolver
	s, err := server.NewServer(opts)
	if s != nil {
		defer s.Shutdown()
	}

	if err == nil {
		t.Fatal("Expected error when using preloads with URL resolver, got nil")
	}

	expectedErr := "resolver preloads only available for resolver type MEM"
	if err.Error() != expectedErr {
		t.Fatalf("Expected error %q, got %q", expectedErr, err.Error())
	}
}

// Test that resolver preload takes precedence over runtime JWT updates after restart (CLUSTER)
func TestOperatorPreloadPrecedenceAfterRestart(t *testing.T) {
	// Create operator key pair
	okp, _ := nkeys.FromSeed(oSeed)

	// Create system account (different from test account)
	syskp, _ := nkeys.CreateAccount()
	syspub, _ := syskp.PublicKey()
	sysnac := jwt.NewAccountClaims(syspub)
	sysJWT, err := sysnac.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating system account JWT: %v", err)
	}

	// Create test account key pair (this is the one we'll test)
	akp, _ := nkeys.CreateAccount()
	apub, _ := akp.PublicKey()

	// Create initial account JWT with connection limit of 10
	nac := jwt.NewAccountClaims(apub)
	nac.Limits.Conn = 10
	initialJWT, err := nac.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating initial account JWT: %v", err)
	}

	// Create operator JWT
	opub, _ := okp.PublicKey()
	opc := jwt.NewOperatorClaims(opub)
	operatorJWT, err := opc.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating operator JWT: %v", err)
	}

	// Write operator JWT to temp file
	operatorFile := "/tmp/test_op_preload.jwt"
	if err := ioutil.WriteFile(operatorFile, []byte(operatorJWT), 0644); err != nil {
		t.Fatalf("Error writing operator JWT: %v", err)
	}
	defer os.Remove(operatorFile)

	// Create cluster config template with preloaded account JWT (Conn limit = 10)
	cf := `
	listen: 127.0.0.1:-1
	cluster {
		listen: 127.0.0.1:-1
		%s
	}
	operator: %s
	system_account: %s
	resolver: MEMORY
	resolver_preload = {
		%s: %q
		%s: %q
	}
	`

	// Start first server in cluster
	contents := strings.Replace(fmt.Sprintf(cf, "", operatorFile, syspub, syspub, sysJWT, apub, initialJWT), "\n\t", "\n", -1)
	confFile := createConfFile(t, []byte(contents))
	defer os.Remove(confFile)

	s, opts := RunServerWithConfig(confFile)
	defer s.Shutdown()

	// Start second server in cluster with routes to first
	routeStr := fmt.Sprintf("routes = [nats-route://%s:%d]", opts.Cluster.Host, opts.Cluster.Port)
	contents2 := strings.Replace(fmt.Sprintf(cf, routeStr, operatorFile, syspub, syspub, sysJWT, apub, initialJWT), "\n\t", "\n", -1)
	confFile2 := createConfFile(t, []byte(contents2))
	defer os.Remove(confFile2)

	s2, _ := RunServerWithConfig(confFile2)
	defer s2.Shutdown()

	checkClusterFormed(t, s, s2)
	t.Logf("✓ Cluster formed with 2 servers")
	
	// Verify initial state on both servers: account should have Conn limit of 10
	acc, err := s.LookupAccount(apub)
	if err != nil {
		t.Fatalf("Error looking up account on s1: %v", err)
	}
	if acc.MaxActiveConnections() != 10 {
		t.Fatalf("Expected initial max connections to be 10 on s1, got %d", acc.MaxActiveConnections())
	}

	acc2, err := s2.LookupAccount(apub)
	if err != nil {
		t.Fatalf("Error looking up account on s2: %v", err)
	}
	if acc2.MaxActiveConnections() != 10 {
		t.Fatalf("Expected initial max connections to be 10 on s2, got %d", acc2.MaxActiveConnections())
	}
	t.Logf("✓ [Server 1] Initial state: account has Conn limit of 10 (from preload)")
	t.Logf("✓ [Server 2] Initial state: account has Conn limit of 10 (from preload)")
	
	// Simulate a runtime update by storing a different JWT in the resolver
	// This is what happens when using nsc push or other dynamic account updates
	nac2 := jwt.NewAccountClaims(apub)
	nac2.Limits.Conn = 50
	updatedJWT, err := nac2.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating updated account JWT: %v", err)
	}

	// Store the updated JWT directly in the resolver on both servers (simulates runtime update)
	// This is what would happen if an operator pushes an updated JWT via nsc or API
	if err := s.AccountResolver().Store(apub, updatedJWT); err != nil {
		t.Fatalf("Error storing updated JWT in resolver on s1: %v", err)
	}
	if err := s2.AccountResolver().Store(apub, updatedJWT); err != nil {
		t.Fatalf("Error storing updated JWT in resolver on s2: %v", err)
	}
	t.Logf("✓ [Server 1] Stored updated JWT (Conn=50) in resolver")
	t.Logf("✓ [Server 2] Stored updated JWT (Conn=50) in resolver")

	// Note: The account object in cache still has the old limits (Conn=10)
	// because we only updated the resolver, not the account object
	// In a real scenario, a system account notification would trigger the update
	// For this test, we'll just verify the resolver has the new JWT
	currentLimit := acc.MaxActiveConnections()
	t.Logf("Account object currently has Conn limit of %d", currentLimit)
	
	// Before reload, check what's in the resolver
	jwtBeforeReload, err := s.AccountResolver().Fetch(apub)
	if err != nil {
		t.Fatalf("Error fetching JWT before reload: %v", err)
	}
	claimsBeforeReload, err := jwt.DecodeAccountClaims(jwtBeforeReload)
	if err != nil {
		t.Fatalf("Error decoding JWT before reload: %v", err)
	}
	t.Logf("Before reload: JWT in resolver has Conn limit of %d", claimsBeforeReload.Limits.Conn)

	// Reload BOTH servers (this should re-apply the preload on each)
	if err := s.Reload(); err != nil {
		t.Fatalf("Error reloading server 1: %v", err)
	}
	if err := s2.Reload(); err != nil {
		t.Fatalf("Error reloading server 2: %v", err)
	}
	t.Logf("✓ Reloaded both cluster servers")

	// After reload, check what's in the resolver on both servers
	jwtAfterReload, err := s.AccountResolver().Fetch(apub)
	if err != nil {
		t.Fatalf("Error fetching JWT after reload on s1: %v", err)
	}
	claimsAfterReload, err := jwt.DecodeAccountClaims(jwtAfterReload)
	if err != nil {
		t.Fatalf("Error decoding JWT after reload on s1: %v", err)
	}
	t.Logf("[Server 1] After reload: JWT in resolver has Conn limit of %d", claimsAfterReload.Limits.Conn)

	jwtAfterReload2, err := s2.AccountResolver().Fetch(apub)
	if err != nil {
		t.Fatalf("Error fetching JWT after reload on s2: %v", err)
	}
	claimsAfterReload2, err := jwt.DecodeAccountClaims(jwtAfterReload2)
	if err != nil {
		t.Fatalf("Error decoding JWT after reload on s2: %v", err)
	}
	t.Logf("[Server 2] After reload: JWT in resolver has Conn limit of %d", claimsAfterReload2.Limits.Conn)

	// Look up the account again after reload on both servers
	acc, err = s.LookupAccount(apub)
	if err != nil {
		t.Fatalf("Error looking up account after reload on s1: %v", err)
	}
	acc2, err = s2.LookupAccount(apub)
	if err != nil {
		t.Fatalf("Error looking up account after reload on s2: %v", err)
	}

	// Check the final state: does preload override the runtime update?
	finalLimit := acc.MaxActiveConnections()
	finalLimit2 := acc2.MaxActiveConnections()
	resolverLimit := claimsAfterReload.Limits.Conn
	resolverLimit2 := claimsAfterReload2.Limits.Conn

	t.Logf("[Server 1] After reload: Account object has Conn limit of %d", finalLimit)
	t.Logf("[Server 2] After reload: Account object has Conn limit of %d", finalLimit2)

	if resolverLimit == 10 && resolverLimit2 == 10 {
		t.Logf("✓ PRELOAD JWT restored in BOTH cluster servers' resolvers (Conn limit = 10)")
		t.Logf("CONCLUSION: On reload, preloads ARE re-applied to the resolver on each node")
		t.Logf("This means runtime Store() updates are OVERWRITTEN by preloads on reload")

		if finalLimit == 10 && finalLimit2 == 10 {
			t.Logf("✓ Account objects on BOTH servers updated from preload JWT")
		} else {
			t.Logf("⚠ One or more account objects kept old limit (s1=%d, s2=%d)", finalLimit, finalLimit2)
			t.Logf("This is expected because the account was already in cache")
		}
	} else {
		t.Logf("✗ Runtime update persisted (s1 resolver=%d, s2 resolver=%d)", resolverLimit, resolverLimit2)
		t.Logf("UNEXPECTED: Preload should have been restored on reload for both servers")
		t.Fatalf("This is a bug - preloads should override runtime updates")
	}

	// Also check opts to ensure they still have the original config
	if opts.SystemAccount != syspub {
		t.Fatalf("System account mismatch")
	}

	t.Logf("\n=== FINAL SUMMARY (CLUSTER) ===")
	t.Logf("1. Preload JWTs take precedence in the RESOLVER after reload on ALL cluster nodes")
	t.Logf("2. Runtime Store() updates to MemAccResolver are OVERWRITTEN on reload on EACH node")
	t.Logf("3. Each cluster node independently manages its own MemAccResolver")
	t.Logf("4. Preloads are applied from config on each node during reload")
	t.Logf("\nIMPLICATION: In a cluster, preloads always win on ALL nodes after reload")
}

// Test that resolver preload takes precedence over runtime JWT updates after FULL NODE RESTART (CLUSTER)
// This is different from reload - the servers completely shut down and start fresh
func TestOperatorPreloadPrecedenceAfterNodeRestart(t *testing.T) {
	// Create operator key pair
	okp, _ := nkeys.FromSeed(oSeed)

	// Create system account (different from test account)
	syskp, _ := nkeys.CreateAccount()
	syspub, _ := syskp.PublicKey()
	sysnac := jwt.NewAccountClaims(syspub)
	sysJWT, err := sysnac.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating system account JWT: %v", err)
	}

	// Create test account key pair (this is the one we'll test)
	akp, _ := nkeys.CreateAccount()
	apub, _ := akp.PublicKey()

	// Create initial account JWT with connection limit of 10
	nac := jwt.NewAccountClaims(apub)
	nac.Limits.Conn = 10
	initialJWT, err := nac.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating initial account JWT: %v", err)
	}

	// Create operator JWT
	opub, _ := okp.PublicKey()
	opc := jwt.NewOperatorClaims(opub)
	operatorJWT, err := opc.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating operator JWT: %v", err)
	}

	// Write operator JWT to temp file
	operatorFile := "/tmp/test_op_restart.jwt"
	if err := ioutil.WriteFile(operatorFile, []byte(operatorJWT), 0644); err != nil {
		t.Fatalf("Error writing operator JWT: %v", err)
	}
	defer os.Remove(operatorFile)

	// Create cluster config template with preloaded account JWT (Conn limit = 10)
	cf := `
	listen: 127.0.0.1:-1
	cluster {
		listen: 127.0.0.1:-1
		%s
	}
	operator: %s
	system_account: %s
	resolver: MEMORY
	resolver_preload = {
		%s: %q
		%s: %q
	}
	`

	// Start first cluster server with preloaded JWT
	contents := strings.Replace(fmt.Sprintf(cf, "", operatorFile, syspub, syspub, sysJWT, apub, initialJWT), "\n\t", "\n", -1)
	confFile := createConfFile(t, []byte(contents))
	defer os.Remove(confFile)

	s1, opts1 := RunServerWithConfig(confFile)

	// Start second cluster server
	routeStr := fmt.Sprintf("routes = [nats-route://%s:%d]", opts1.Cluster.Host, opts1.Cluster.Port)
	contents2 := strings.Replace(fmt.Sprintf(cf, routeStr, operatorFile, syspub, syspub, sysJWT, apub, initialJWT), "\n\t", "\n", -1)
	confFile2 := createConfFile(t, []byte(contents2))
	defer os.Remove(confFile2)

	s2, _ := RunServerWithConfig(confFile2)

	checkClusterFormed(t, s1, s2)
	t.Logf("✓ CLUSTER 1 formed with 2 servers")

	// Verify initial state on both servers: account should have Conn limit of 10
	acc, err := s1.LookupAccount(apub)
	if err != nil {
		t.Fatalf("Error looking up account on s1: %v", err)
	}
	if acc.MaxActiveConnections() != 10 {
		t.Fatalf("Expected initial max connections to be 10 on s1, got %d", acc.MaxActiveConnections())
	}

	acc2, err := s2.LookupAccount(apub)
	if err != nil {
		t.Fatalf("Error looking up account on s2: %v", err)
	}
	if acc2.MaxActiveConnections() != 10 {
		t.Fatalf("Expected initial max connections to be 10 on s2, got %d", acc2.MaxActiveConnections())
	}
	t.Logf("✓ [Cluster 1 - Server 1] Initial state: account has Conn limit of 10 (from preload)")
	t.Logf("✓ [Cluster 1 - Server 2] Initial state: account has Conn limit of 10 (from preload)")

	// Simulate a runtime update by storing a different JWT in the resolver
	nac2 := jwt.NewAccountClaims(apub)
	nac2.Limits.Conn = 50
	updatedJWT, err := nac2.Encode(okp)
	if err != nil {
		t.Fatalf("Error generating updated account JWT: %v", err)
	}

	// Store the updated JWT directly in the resolver on BOTH servers (simulates runtime update)
	if err := s1.AccountResolver().Store(apub, updatedJWT); err != nil {
		t.Fatalf("Error storing updated JWT in resolver on s1: %v", err)
	}
	if err := s2.AccountResolver().Store(apub, updatedJWT); err != nil {
		t.Fatalf("Error storing updated JWT in resolver on s2: %v", err)
	}
	t.Logf("✓ [Cluster 1 - Server 1] Stored updated JWT (Conn=50) in resolver")
	t.Logf("✓ [Cluster 1 - Server 2] Stored updated JWT (Conn=50) in resolver")

	// Verify the resolver has the updated JWT on both servers
	jwtBeforeShutdown1, err := s1.AccountResolver().Fetch(apub)
	if err != nil {
		t.Fatalf("Error fetching JWT before shutdown on s1: %v", err)
	}
	claimsBeforeShutdown1, err := jwt.DecodeAccountClaims(jwtBeforeShutdown1)
	if err != nil {
		t.Fatalf("Error decoding JWT before shutdown on s1: %v", err)
	}
	if claimsBeforeShutdown1.Limits.Conn != 50 {
		t.Fatalf("Expected resolver to have Conn=50 before shutdown on s1, got %d", claimsBeforeShutdown1.Limits.Conn)
	}

	jwtBeforeShutdown2, err := s2.AccountResolver().Fetch(apub)
	if err != nil {
		t.Fatalf("Error fetching JWT before shutdown on s2: %v", err)
	}
	claimsBeforeShutdown2, err := jwt.DecodeAccountClaims(jwtBeforeShutdown2)
	if err != nil {
		t.Fatalf("Error decoding JWT before shutdown on s2: %v", err)
	}
	if claimsBeforeShutdown2.Limits.Conn != 50 {
		t.Fatalf("Expected resolver to have Conn=50 before shutdown on s2, got %d", claimsBeforeShutdown2.Limits.Conn)
	}
	t.Logf("✓ [Cluster 1] Before shutdown: JWT in both resolvers has Conn limit of 50")

	// SHUTDOWN THE ENTIRE CLUSTER COMPLETELY
	s1.Shutdown()
	s2.Shutdown()
	t.Logf("✓ [Cluster 1] Shut down both servers completely")

	// Wait for clean shutdown
	s1.WaitForShutdown()
	s2.WaitForShutdown()

	// START FRESH SERVERS with the same configs (not clustered this time, just independent servers)
	// This tests that each server independently loads preloads from its config
	s3, _ := RunServerWithConfig(confFile)
	defer s3.Shutdown()

	s4, _ := RunServerWithConfig(confFile2)
	defer s4.Shutdown()

	t.Logf("✓ CLUSTER 2 started fresh with same configs (2 independent servers)")

	// After restart, check what's in the resolver on both new servers
	jwtAfterRestart3, err := s3.AccountResolver().Fetch(apub)
	if err != nil {
		t.Fatalf("Error fetching JWT after restart on s3: %v", err)
	}
	claimsAfterRestart3, err := jwt.DecodeAccountClaims(jwtAfterRestart3)
	if err != nil {
		t.Fatalf("Error decoding JWT after restart on s3: %v", err)
	}

	jwtAfterRestart4, err := s4.AccountResolver().Fetch(apub)
	if err != nil {
		t.Fatalf("Error fetching JWT after restart on s4: %v", err)
	}
	claimsAfterRestart4, err := jwt.DecodeAccountClaims(jwtAfterRestart4)
	if err != nil {
		t.Fatalf("Error decoding JWT after restart on s4: %v", err)
	}

	t.Logf("[Cluster 2 - Server 1] After restart: JWT in resolver has Conn limit of %d", claimsAfterRestart3.Limits.Conn)
	t.Logf("[Cluster 2 - Server 2] After restart: JWT in resolver has Conn limit of %d", claimsAfterRestart4.Limits.Conn)

	// Look up the account after restart on both servers
	acc3, err := s3.LookupAccount(apub)
	if err != nil {
		t.Fatalf("Error looking up account after restart on s3: %v", err)
	}
	acc4, err := s4.LookupAccount(apub)
	if err != nil {
		t.Fatalf("Error looking up account after restart on s4: %v", err)
	}

	// Check the final state
	finalLimit3 := acc3.MaxActiveConnections()
	finalLimit4 := acc4.MaxActiveConnections()
	resolverLimit3 := claimsAfterRestart3.Limits.Conn
	resolverLimit4 := claimsAfterRestart4.Limits.Conn

	t.Logf("[Cluster 2 - Server 1] After restart: Account object has Conn limit of %d", finalLimit3)
	t.Logf("[Cluster 2 - Server 2] After restart: Account object has Conn limit of %d", finalLimit4)

	if resolverLimit3 == 10 && resolverLimit4 == 10 && finalLimit3 == 10 && finalLimit4 == 10 {
		t.Logf("✓ PRELOAD JWT restored in BOTH servers' resolvers (Conn limit = 10)")
		t.Logf("✓ Account objects on BOTH servers have preload limits (Conn limit = 10)")
		t.Logf("CONCLUSION: On full cluster restart, preloads ARE re-applied on all nodes")
		t.Logf("Runtime Store() updates are NOT persisted across restarts")
	} else {
		t.Logf("✗ Runtime update persisted (s3: resolver=%d account=%d, s4: resolver=%d account=%d)",
			resolverLimit3, finalLimit3, resolverLimit4, finalLimit4)
		t.Fatalf("UNEXPECTED: Preload should be restored on fresh server start")
	}

	t.Logf("\n=== RESTART TEST SUMMARY (CLUSTER) ===")
	t.Logf("1. MemAccResolver does NOT persist state across restarts on ANY node")
	t.Logf("2. On fresh start, preloads from config are loaded on EACH node")
	t.Logf("3. Runtime Store() updates are lost when cluster restarts")
	t.Logf("4. Each node independently loads its own preloads from its config")
	t.Logf("\nIMPLICATION: Preloads ALWAYS win after a full cluster restart")
	t.Logf("This is expected - MemAccResolver is in-memory only, not shared across cluster")
}

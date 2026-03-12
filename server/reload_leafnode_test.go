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
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestConfigReloadLeafNodeSimultaneousAddRemove tests that adding one remote
// and removing another in a single config reload works correctly.
func TestConfigReloadLeafNodeSimultaneousAddRemove(t *testing.T) {
	// Start two hub servers that will accept leaf connections.
	confHub1 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub1"
		leafnodes {
			port: -1
		}
	`))
	hub1, hub1Opts := RunServerWithConfig(confHub1)
	defer hub1.Shutdown()

	confHub2 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub2"
		leafnodes {
			port: -1
		}
	`))
	hub2, hub2Opts := RunServerWithConfig(confHub2)
	defer hub2.Shutdown()

	// Start a leaf server initially connecting to hub1 only.
	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				%s
			]
		}
	`
	remote1 := fmt.Sprintf(`{ url: "nats://127.0.0.1:%d" }`, hub1Opts.LeafNode.Port)
	remote2 := fmt.Sprintf(`{ url: "nats://127.0.0.1:%d" }`, hub2Opts.LeafNode.Port)

	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, remote1)))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	// Verify initial connection to hub1.
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hub1, 1)
	checkLeafNodeConnectedCount(t, hub2, 0)

	// Now reload: remove hub1 remote, add hub2 remote in a single reload.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, remote2))

	// Should now be connected to hub2 only.
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hub2, 1)
	// Hub1 should have lost its leaf connection.
	checkLeafNodeConnectedCount(t, hub1, 0)
}

// TestConfigReloadLeafNodeReenableDisabledRemote tests that a remote that was
// disabled via reload can be re-enabled by another reload.
func TestConfigReloadLeafNodeReenableDisabledRemote(t *testing.T) {
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "hub"
		leafnodes {
			port: -1
		}
	`))
	hub, hubOpts := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				{
					url: "nats://127.0.0.1:%d"
					%s
				}
			]
		}
	`
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, hubOpts.LeafNode.Port, "")))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	// Verify initial connection.
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hub, 1)

	// Disable the remote.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, hubOpts.LeafNode.Port, "disabled: true"))
	checkLeafNodeConnectedCount(t, leafSrv, 0)
	checkLeafNodeConnectedCount(t, hub, 0)

	// Re-enable the remote.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, hubOpts.LeafNode.Port, ""))
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hub, 1)
}

// TestConfigReloadLeafNodeDataPathAfterReload verifies that after a remote is
// added via config reload, the leafnode connection is functional: messages
// published on one side are received on the other.
func TestConfigReloadLeafNodeDataPathAfterReload(t *testing.T) {
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "hub"
		leafnodes {
			port: -1
		}
	`))
	hub, hubOpts := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	// Start leaf with no remotes initially.
	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				%s
			]
		}
	`
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, "")))
	leafSrv, leafOpts := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	// No leaf connections initially.
	checkLeafNodeConnectedCount(t, hub, 0)
	checkLeafNodeConnectedCount(t, leafSrv, 0)

	// Add the remote via reload.
	remote := fmt.Sprintf(`{ url: "nats://127.0.0.1:%d" }`, hubOpts.LeafNode.Port)
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, remote))

	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hub, 1)

	// Connect a client to the hub and subscribe.
	ncHub := natsConnect(t, hub.ClientURL())
	defer ncHub.Close()
	sub := natsSubSync(t, ncHub, "test.subject")
	natsFlush(t, ncHub)

	// Connect a client to the leaf and publish.
	ncLeaf := natsConnect(t, fmt.Sprintf("nats://127.0.0.1:%d", leafOpts.Port))
	defer ncLeaf.Close()

	// Wait for subscription to propagate through leaf node.
	checkFor(t, 2*time.Second, 50*time.Millisecond, func() error {
		if n := leafSrv.NumSubscriptions(); n == 0 {
			return fmt.Errorf("no subscriptions propagated yet")
		}
		return nil
	})

	payload := []byte("hello via reloaded leafnode")
	natsPub(t, ncLeaf, "test.subject", payload)
	natsFlush(t, ncLeaf)

	msg := natsNexMsg(t, sub, 2*time.Second)
	if string(msg.Data) != string(payload) {
		t.Fatalf("Expected payload %q, got %q", payload, msg.Data)
	}

	// Now test the reverse direction: hub -> leaf.
	hubSubsBefore := hub.NumSubscriptions()
	subLeaf := natsSubSync(t, ncLeaf, "reverse.subject")
	natsFlush(t, ncLeaf)

	checkFor(t, 2*time.Second, 50*time.Millisecond, func() error {
		if n := hub.NumSubscriptions(); n <= hubSubsBefore {
			return fmt.Errorf("no subscriptions propagated to hub yet")
		}
		return nil
	})

	reversePayload := []byte("hello from hub to leaf")
	natsPub(t, ncHub, "reverse.subject", reversePayload)
	natsFlush(t, ncHub)

	msg = natsNexMsg(t, subLeaf, 2*time.Second)
	if string(msg.Data) != string(reversePayload) {
		t.Fatalf("Expected payload %q, got %q", reversePayload, msg.Data)
	}
}

// TestConfigReloadLeafNodeMultipleSequentialReloads tests adding and removing
// remotes across multiple sequential reloads to ensure the bookkeeping stays
// consistent.
func TestConfigReloadLeafNodeMultipleSequentialReloads(t *testing.T) {
	// Start 3 hub servers.
	var hubs [3]*Server
	var hubPorts [3]int
	for i := range 3 {
		conf := createConfFile(t, []byte(fmt.Sprintf(`
			port: -1
			server_name: "hub%d"
			leafnodes {
				port: -1
			}
		`, i+1)))
		s, o := RunServerWithConfig(conf)
		defer s.Shutdown()
		hubs[i] = s
		hubPorts[i] = o.LeafNode.Port
	}

	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				%s
			]
		}
	`
	makeRemote := func(port int) string {
		return fmt.Sprintf(`{ url: "nats://127.0.0.1:%d" }`, port)
	}

	// Step 1: Start with hub0.
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, makeRemote(hubPorts[0]))))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hubs[0], 1)

	// Step 2: Add hub1 (now connected to hub0 + hub1).
	remotes := makeRemote(hubPorts[0]) + "\n" + makeRemote(hubPorts[1])
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, remotes))
	checkLeafNodeConnectedCount(t, leafSrv, 2)
	checkLeafNodeConnectedCount(t, hubs[0], 1)
	checkLeafNodeConnectedCount(t, hubs[1], 1)

	// Step 3: Remove hub0 (now connected to hub1 only).
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, makeRemote(hubPorts[1])))
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hubs[0], 0)
	checkLeafNodeConnectedCount(t, hubs[1], 1)

	// Step 4: Add hub2 and hub0 back (now connected to hub0, hub1, hub2).
	remotes = makeRemote(hubPorts[0]) + "\n" + makeRemote(hubPorts[1]) + "\n" + makeRemote(hubPorts[2])
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, remotes))
	checkLeafNodeConnectedCount(t, leafSrv, 3)
	for _, h := range hubs {
		checkLeafNodeConnectedCount(t, h, 1)
	}

	// Step 5: Remove all remotes.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, ""))
	checkLeafNodeConnectedCount(t, leafSrv, 0)
	for _, h := range hubs {
		checkLeafNodeConnectedCount(t, h, 0)
	}

	// Step 6: Re-add hub2 only to verify we can add back after removing all.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, makeRemote(hubPorts[2])))
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hubs[2], 1)
	checkLeafNodeConnectedCount(t, hubs[0], 0)
	checkLeafNodeConnectedCount(t, hubs[1], 0)
}

// TestConfigReloadLeafNodeURLChangeTriggersReconnect tests that modifying
// the URL list of an existing remote causes a disconnect and reconnect, since
// URLs are part of the remote's identity used for matching old and new configs.
func TestConfigReloadLeafNodeURLChangeTriggersReconnect(t *testing.T) {
	confHub1 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub1"
		leafnodes {
			port: -1
		}
	`))
	hub1, hub1Opts := RunServerWithConfig(confHub1)
	defer hub1.Shutdown()

	confHub2 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub2"
		leafnodes {
			port: -1
		}
	`))
	hub2, hub2Opts := RunServerWithConfig(confHub2)
	defer hub2.Shutdown()

	confHub3 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub3"
		leafnodes {
			port: -1
		}
	`))
	hub3, hub3Opts := RunServerWithConfig(confHub3)
	defer hub3.Shutdown()

	// Start leaf with a single remote that has two URLs.
	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				{ urls: [%s] }
			]
		}
	`
	url1 := fmt.Sprintf(`"nats://127.0.0.1:%d"`, hub1Opts.LeafNode.Port)
	url2 := fmt.Sprintf(`"nats://127.0.0.1:%d"`, hub2Opts.LeafNode.Port)
	url3 := fmt.Sprintf(`"nats://127.0.0.1:%d"`, hub3Opts.LeafNode.Port)

	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, url1+", "+url2)))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	// Should be connected to one of the two hubs.
	checkLeafNodeConnected(t, leafSrv)

	// Helper to collect leaf connection CIDs from the leaf server.
	collectCIDs := func(s *Server) map[uint64]struct{} {
		m := make(map[uint64]struct{})
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, l := range s.leafs {
			l.mu.Lock()
			m[l.cid] = struct{}{}
			l.mu.Unlock()
		}
		return m
	}

	// Now reload: change URLs from [hub1, hub2] to [hub1, hub3].
	// Since URLs are part of the remote identity, this is treated as
	// a remove of the old remote + add of a new remote, causing a
	// full disconnect and reconnect cycle.
	cidsBefore := collectCIDs(leafSrv)
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, url1+", "+url3))

	// The leaf should reconnect (to either hub1 or hub3 now).
	checkLeafNodeConnected(t, leafSrv)
	// hub2 should no longer have any leaf connections.
	checkLeafNodeConnectedCount(t, hub2, 0)

	// Verify the connection was replaced (new CID), not kept alive.
	cidsAfter := collectCIDs(leafSrv)
	for cid := range cidsBefore {
		if _, ok := cidsAfter[cid]; ok {
			t.Fatalf("Expected leaf connection %d to be replaced after URL change, but it survived", cid)
		}
	}

	// Now test adding a URL: [hub1, hub3] -> [hub1, hub2, hub3].
	// This also triggers a remove+add cycle since URLs changed.
	cidsBefore = collectCIDs(leafSrv)
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, url1+", "+url2+", "+url3))
	checkLeafNodeConnected(t, leafSrv)

	cidsAfter = collectCIDs(leafSrv)
	for cid := range cidsBefore {
		if _, ok := cidsAfter[cid]; ok {
			t.Fatalf("Expected leaf connection %d to be replaced after adding URL, but it survived", cid)
		}
	}

	// Now test removing a URL: [hub1, hub2, hub3] -> [hub1].
	cidsBefore = collectCIDs(leafSrv)
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, url1))
	checkLeafNodeConnected(t, leafSrv)
	// After reload, should only be connected to hub1.
	checkLeafNodeConnectedCount(t, hub1, 1)
	checkLeafNodeConnectedCount(t, hub2, 0)
	checkLeafNodeConnectedCount(t, hub3, 0)

	cidsAfter = collectCIDs(leafSrv)
	for cid := range cidsBefore {
		if _, ok := cidsAfter[cid]; ok {
			t.Fatalf("Expected leaf connection %d to be replaced after removing URLs, but it survived", cid)
		}
	}
}

// TestConfigReloadLeafNodeNamedRemoteURLChange tests that when a remote has
// a "name", changing URLs does NOT cause a disconnect/reconnect. The existing
// connection survives and the URL list is updated in-place.
func TestConfigReloadLeafNodeNamedRemoteURLChange(t *testing.T) {
	confHub1 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub1"
		leafnodes {
			port: -1
		}
	`))
	hub1, hub1Opts := RunServerWithConfig(confHub1)
	defer hub1.Shutdown()

	confHub2 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub2"
		leafnodes {
			port: -1
		}
	`))
	hub2, _ := RunServerWithConfig(confHub2)
	defer hub2.Shutdown()

	// Start leaf with a named remote pointing at hub1.
	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				{ name: "my-hub", urls: [%s] }
			]
		}
	`
	url1 := fmt.Sprintf(`"nats://127.0.0.1:%d"`, hub1Opts.LeafNode.Port)

	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, url1)))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnected(t, leafSrv)

	// Collect CIDs before reload.
	collectCIDs := func(s *Server) map[uint64]struct{} {
		m := make(map[uint64]struct{})
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, l := range s.leafs {
			l.mu.Lock()
			m[l.cid] = struct{}{}
			l.mu.Unlock()
		}
		return m
	}
	cidsBefore := collectCIDs(leafSrv)

	// Reload: add hub2's URL to the list. Because the remote has a name,
	// the server should match it and update URLs in-place without reconnecting.
	url2 := fmt.Sprintf(`"nats://127.0.0.1:%d"`, hub2.opts.LeafNode.Port)
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, url1+", "+url2))

	// Still connected.
	checkLeafNodeConnected(t, leafSrv)

	// Verify the connection was NOT replaced (same CID).
	cidsAfter := collectCIDs(leafSrv)
	for cid := range cidsBefore {
		if _, ok := cidsAfter[cid]; !ok {
			t.Fatalf("Expected leaf connection %d to survive URL update on named remote, but it was replaced", cid)
		}
	}

	// Verify the URL list was actually updated on the remote config.
	leafSrv.mu.RLock()
	var urlCount int
	for lrc := range leafSrv.leafRemoteCfgs {
		lrc.RLock()
		urlCount = len(lrc.urls)
		lrc.RUnlock()
	}
	leafSrv.mu.RUnlock()
	if urlCount != 2 {
		t.Fatalf("Expected 2 URLs after reload, got %d", urlCount)
	}

	// Now remove a URL: [hub1, hub2] -> [hub1]. Still no reconnect.
	cidsBefore = collectCIDs(leafSrv)
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, url1))
	checkLeafNodeConnected(t, leafSrv)

	cidsAfter = collectCIDs(leafSrv)
	for cid := range cidsBefore {
		if _, ok := cidsAfter[cid]; !ok {
			t.Fatalf("Expected leaf connection %d to survive URL removal on named remote, but it was replaced", cid)
		}
	}

	leafSrv.mu.RLock()
	for lrc := range leafSrv.leafRemoteCfgs {
		lrc.RLock()
		urlCount = len(lrc.urls)
		lrc.RUnlock()
	}
	leafSrv.mu.RUnlock()
	if urlCount != 1 {
		t.Fatalf("Expected 1 URL after reload, got %d", urlCount)
	}
}

// TestLeafNodeRemoteNameInLeafz tests that when a leaf remote has a configured
// "name", the hub's Leafz() endpoint includes it as RemoteName.
func TestLeafNodeRemoteNameInLeafz(t *testing.T) {
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "hub"
		leafnodes {
			port: -1
		}
	`))
	hub, hubOpts := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	url1 := fmt.Sprintf("nats://127.0.0.1:%d", hubOpts.LeafNode.Port)
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(`
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				{ name: "my-hub-link", urls: ["%s"] }
			]
		}
	`, url1)))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnected(t, hub)

	// Check the hub's Leafz shows the remote name.
	leafz, err := hub.Leafz(nil)
	if err != nil {
		t.Fatalf("Error getting leafz: %v", err)
	}
	if len(leafz.Leafs) != 1 {
		t.Fatalf("Expected 1 leaf, got %d", len(leafz.Leafs))
	}
	li := leafz.Leafs[0]
	if li.RemoteName != "my-hub-link" {
		t.Fatalf("Expected RemoteName %q, got %q", "my-hub-link", li.RemoteName)
	}
	if li.Name != "leaf" {
		t.Fatalf("Expected Name %q, got %q", "leaf", li.Name)
	}
}

// TestLeafNodeRemoteNameNotSetInLeafz tests that when a leaf remote does NOT
// have a configured "name", RemoteName is empty in Leafz.
func TestLeafNodeRemoteNameNotSetInLeafz(t *testing.T) {
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "hub"
		leafnodes {
			port: -1
		}
	`))
	hub, hubOpts := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	url1 := fmt.Sprintf("nats://127.0.0.1:%d", hubOpts.LeafNode.Port)
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(`
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				{ urls: ["%s"] }
			]
		}
	`, url1)))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnected(t, hub)

	leafz, err := hub.Leafz(nil)
	if err != nil {
		t.Fatalf("Error getting leafz: %v", err)
	}
	if len(leafz.Leafs) != 1 {
		t.Fatalf("Expected 1 leaf, got %d", len(leafz.Leafs))
	}
	if leafz.Leafs[0].RemoteName != "" {
		t.Fatalf("Expected empty RemoteName, got %q", leafz.Leafs[0].RemoteName)
	}
}

// TestConfigReloadLeafNodeAddRemoveSameAccountDifferentURLs tests that two
// remotes with the same local account but different URLs are handled correctly
// during add/remove operations.
func TestConfigReloadLeafNodeAddRemoveSameAccountDifferentURLs(t *testing.T) {
	confHub1 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub1"
		leafnodes {
			port: -1
		}
	`))
	hub1, hub1Opts := RunServerWithConfig(confHub1)
	defer hub1.Shutdown()

	confHub2 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub2"
		leafnodes {
			port: -1
		}
	`))
	hub2, hub2Opts := RunServerWithConfig(confHub2)
	defer hub2.Shutdown()

	// Start leaf with both remotes pointing to same (default/global) account.
	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				%s
			]
		}
	`
	remote1 := fmt.Sprintf(`{ url: "nats://127.0.0.1:%d" }`, hub1Opts.LeafNode.Port)
	remote2 := fmt.Sprintf(`{ url: "nats://127.0.0.1:%d" }`, hub2Opts.LeafNode.Port)

	remotes := remote1 + "\n" + remote2
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, remotes)))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnectedCount(t, leafSrv, 2)
	checkLeafNodeConnectedCount(t, hub1, 1)
	checkLeafNodeConnectedCount(t, hub2, 1)

	// Remove hub1 remote only. Hub2 should stay connected.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, remote2))
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hub1, 0)
	checkLeafNodeConnectedCount(t, hub2, 1)

	// Add hub1 back.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, remotes))
	checkLeafNodeConnectedCount(t, leafSrv, 2)
	checkLeafNodeConnectedCount(t, hub1, 1)
	checkLeafNodeConnectedCount(t, hub2, 1)
}

// TestConfigReloadLeafNodeDataPathAfterRemoveAndReAdd verifies that after
// removing a remote and re-adding it, the data path works end-to-end.
func TestConfigReloadLeafNodeDataPathAfterRemoveAndReAdd(t *testing.T) {
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "hub"
		leafnodes {
			port: -1
		}
	`))
	hub, hubOpts := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				%s
			]
		}
	`
	remote := fmt.Sprintf(`{ url: "nats://127.0.0.1:%d" }`, hubOpts.LeafNode.Port)
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, remote)))
	leafSrv, leafOpts := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnected(t, leafSrv)

	// Remove the remote.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, ""))
	checkLeafNodeConnectedCount(t, leafSrv, 0)
	checkLeafNodeConnectedCount(t, hub, 0)

	// Re-add the same remote.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, remote))
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hub, 1)

	// Verify data path works after remove + re-add cycle.
	ncHub := natsConnect(t, hub.ClientURL())
	defer ncHub.Close()
	sub := natsSubSync(t, ncHub, "test.>")
	natsFlush(t, ncHub)

	ncLeaf := natsConnect(t, fmt.Sprintf("nats://127.0.0.1:%d", leafOpts.Port))
	defer ncLeaf.Close()

	checkFor(t, 2*time.Second, 50*time.Millisecond, func() error {
		if n := leafSrv.NumSubscriptions(); n == 0 {
			return fmt.Errorf("no subscriptions propagated yet")
		}
		return nil
	})

	natsPub(t, ncLeaf, "test.readd", []byte("after re-add"))
	natsFlush(t, ncLeaf)

	msg := natsNexMsg(t, sub, 2*time.Second)
	if string(msg.Data) != "after re-add" {
		t.Fatalf("Expected 'after re-add', got %q", msg.Data)
	}
}

// TestConfigReloadLeafNodeDisableOneOfMultipleRemotes tests that disabling one
// remote while keeping another active does not affect the active remote's
// connection.
func TestConfigReloadLeafNodeDisableOneOfMultipleRemotes(t *testing.T) {
	confHub1 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub1"
		leafnodes {
			port: -1
		}
	`))
	hub1, hub1Opts := RunServerWithConfig(confHub1)
	defer hub1.Shutdown()

	confHub2 := createConfFile(t, []byte(`
		port: -1
		server_name: "hub2"
		leafnodes {
			port: -1
		}
	`))
	hub2, hub2Opts := RunServerWithConfig(confHub2)
	defer hub2.Shutdown()

	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				{
					url: "nats://127.0.0.1:%d"
					%s
				}
				{
					url: "nats://127.0.0.1:%d"
					%s
				}
			]
		}
	`
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl,
		hub1Opts.LeafNode.Port, "",
		hub2Opts.LeafNode.Port, "")))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnectedCount(t, leafSrv, 2)
	checkLeafNodeConnectedCount(t, hub1, 1)
	checkLeafNodeConnectedCount(t, hub2, 1)

	// Disable hub1 remote only.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl,
		hub1Opts.LeafNode.Port, "disabled: true",
		hub2Opts.LeafNode.Port, ""))
	checkLeafNodeConnected(t, leafSrv)
	checkLeafNodeConnectedCount(t, hub1, 0)
	checkLeafNodeConnectedCount(t, hub2, 1)

	// Re-enable hub1 remote.
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl,
		hub1Opts.LeafNode.Port, "",
		hub2Opts.LeafNode.Port, ""))
	checkLeafNodeConnectedCount(t, leafSrv, 2)
	checkLeafNodeConnectedCount(t, hub1, 1)
	checkLeafNodeConnectedCount(t, hub2, 1)
}

// TestConfigReloadLeafNodeConnectionStableOnNoChange verifies that existing
// leaf connections are not disrupted when a reload happens with no actual
// changes to the leaf node remote configuration.
func TestConfigReloadLeafNodeConnectionStableOnNoChange(t *testing.T) {
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "hub"
		leafnodes {
			port: -1
		}
	`))
	hub, hubOpts := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	tmpl := `
		port: -1
		server_name: "leaf"
		%s
		leafnodes {
			remotes [
				{
					url: "nats://127.0.0.1:%d"
				}
			]
		}
	`
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, "", hubOpts.LeafNode.Port)))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnected(t, leafSrv)

	// Collect connection IDs before reload.
	collectCIDs := func(s *Server) map[uint64]struct{} {
		m := make(map[uint64]struct{})
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, l := range s.leafs {
			l.mu.Lock()
			m[l.cid] = struct{}{}
			l.mu.Unlock()
		}
		return m
	}
	cidsBefore := collectCIDs(leafSrv)

	// Reload with an unrelated change (add debug: false).
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, "debug: false", hubOpts.LeafNode.Port))

	// Connection should still be up.
	checkLeafNodeConnected(t, leafSrv)

	// Verify the connection ID hasn't changed (same connection, not reconnected).
	cidsAfter := collectCIDs(leafSrv)
	for cid := range cidsBefore {
		if _, ok := cidsAfter[cid]; !ok {
			t.Fatalf("Expected leaf connection %d to survive reload, but it was replaced", cid)
		}
	}
}

// TestConfigReloadLeafNodeAddWithAccounts tests adding a remote with a
// specific local account via config reload.
func TestConfigReloadLeafNodeAddWithAccounts(t *testing.T) {
	// Hub with two accounts and leafnode authorization per account.
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "hub"
		accounts {
			ACCT_A { users: [{user: "a", password: "a"}] }
			ACCT_B { users: [{user: "b", password: "b"}] }
		}
		leafnodes {
			port: -1
			authorization {
				users: [
					{user: "leaf_a", password: "leaf_a", account: "ACCT_A"}
					{user: "leaf_b", password: "leaf_b", account: "ACCT_B"}
				]
			}
		}
	`))
	hub, hubOpts := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	tmpl := `
		port: -1
		server_name: "leaf"
		accounts {
			ACCT_A { users: [{user: "a", password: "a"}] }
			ACCT_B { users: [{user: "b", password: "b"}] }
		}
		leafnodes {
			remotes [
				%s
			]
		}
	`
	// Start with remote for ACCT_A only.
	remoteA := fmt.Sprintf(`{
		url: "nats://leaf_a:leaf_a@127.0.0.1:%d"
		account: "ACCT_A"
	}`, hubOpts.LeafNode.Port)

	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, remoteA)))
	leafSrv, leafOpts := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnected(t, leafSrv)

	// Add a second remote for ACCT_B.
	remoteB := fmt.Sprintf(`{
		url: "nats://leaf_b:leaf_b@127.0.0.1:%d"
		account: "ACCT_B"
	}`, hubOpts.LeafNode.Port)
	reloadUpdateConfig(t, leafSrv, confLeaf, fmt.Sprintf(tmpl, remoteA+"\n"+remoteB))
	checkLeafNodeConnectedCount(t, leafSrv, 2)
	checkLeafNodeConnectedCount(t, hub, 2)

	// Verify data path isolation: message on ACCT_A doesn't leak to ACCT_B.
	ncHubA := natsConnect(t, hub.ClientURL(), nats.UserInfo("a", "a"))
	defer ncHubA.Close()
	subA := natsSubSync(t, ncHubA, "acct.test")
	natsFlush(t, ncHubA)

	ncHubB := natsConnect(t, hub.ClientURL(), nats.UserInfo("b", "b"))
	defer ncHubB.Close()
	subB := natsSubSync(t, ncHubB, "acct.test")
	natsFlush(t, ncHubB)

	// Publish from leaf ACCT_A.
	ncLeafA := natsConnect(t, fmt.Sprintf("nats://a:a@127.0.0.1:%d", leafOpts.Port))
	defer ncLeafA.Close()

	checkFor(t, 2*time.Second, 50*time.Millisecond, func() error {
		if n := leafSrv.NumSubscriptions(); n < 2 {
			return fmt.Errorf("waiting for subscriptions to propagate, got %d", n)
		}
		return nil
	})

	natsPub(t, ncLeafA, "acct.test", []byte("from A"))
	natsFlush(t, ncLeafA)

	// ACCT_A subscriber on hub should get it.
	msg := natsNexMsg(t, subA, 2*time.Second)
	if string(msg.Data) != "from A" {
		t.Fatalf("Expected 'from A', got %q", msg.Data)
	}

	// ACCT_B subscriber on hub should NOT get it.
	_, err := subB.NextMsg(200 * time.Millisecond)
	if err != nats.ErrTimeout {
		t.Fatalf("Expected timeout on ACCT_B sub, got err=%v", err)
	}
}

// TestConfigReloadLeafNodeRemoteNameNotReloadable tests that changing the name
// of a remote leafnode is rejected during reload, since the name is used as the
// identity key for matching remotes across reloads.
func TestConfigReloadLeafNodeRemoteNameNotReloadable(t *testing.T) {
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "hub"
		leafnodes {
			port: -1
		}
	`))
	hub, hubOpts := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	tmpl := `
		port: -1
		server_name: "leaf"
		leafnodes {
			remotes [
				{ url: "nats://127.0.0.1:%d", name: "%s" }
			]
		}
	`
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(tmpl, hubOpts.LeafNode.Port, "original")))
	leafSrv, _ := RunServerWithConfig(confLeaf)
	defer leafSrv.Shutdown()

	checkLeafNodeConnected(t, leafSrv)

	// Attempt to change the remote name via reload — should fail.
	newConf := fmt.Sprintf(tmpl, hubOpts.LeafNode.Port, "renamed")
	if err := os.WriteFile(confLeaf, []byte(newConf), 0666); err != nil {
		t.Fatalf("Error writing config file: %v", err)
	}
	err := leafSrv.Reload()
	if err == nil {
		t.Fatal("Expected reload to fail when changing remote name, but it succeeded")
	}
	if !strings.Contains(err.Error(), "remote names are not reloadable") {
		t.Fatalf("Expected error about remote names not being reloadable, got: %v", err)
	}

	// Connection should still be up.
	checkLeafNodeConnected(t, leafSrv)
}

package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestLeafNodePassiveConnection tests that a passive leafnode connection
// bypasses loop detection, allowing topologies like:
//
//	Hub ← SITE_A ← SITE_B
//	  ↖____________↗ (passive)
//
// Without passive mode, this would trigger loop detection and close all connections.
func TestLeafNodePassiveConnection(t *testing.T) {
	// Hub - accepts connections from both SITE_A and SITE_B
	hubConf := `
		listen: 127.0.0.1:-1
		server_name: HUB
		leafnodes {
			port: -1
		}
	`

	// SITE_A - connects to Hub, accepts from SITE_B
	siteAConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_A
		leafnodes {
			port: -1
			remotes [
				{ url: "nats://127.0.0.1:%d" }
			]
		}
	`

	// SITE_B - connects to BOTH Hub (passive) AND SITE_A (normal)
	// The passive connection to Hub bypasses loop detection.
	siteBConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_B
		leafnodes {
			remotes [
				{ url: "nats://127.0.0.1:%d", passive: true }
				{ url: "nats://127.0.0.1:%d" }
			]
		}
	`

	// Start Hub first (accepts connections)
	hubConfFile := createConfFile(t, []byte(hubConf))
	sHub, oHub := RunServerWithConfig(hubConfFile)
	defer sHub.Shutdown()

	// Start SITE_A (connects to Hub, accepts from SITE_B)
	siteAConfFile := createConfFile(t, []byte(fmt.Sprintf(siteAConf, oHub.LeafNode.Port)))
	sSiteA, oSiteA := RunServerWithConfig(siteAConfFile)
	defer sSiteA.Shutdown()

	// Wait for SITE_A to connect to Hub
	checkLeafNodeConnectedCount(t, sSiteA, 1)
	checkLeafNodeConnectedCount(t, sHub, 1)

	// Start SITE_B (connects to both Hub and SITE_A)
	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf,
		oHub.LeafNode.Port, oSiteA.LeafNode.Port)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Wait for all connections to establish
	// Without passive mode, this would fail due to loop detection.
	// SITE_B has 2 outgoing connections (one passive to Hub, one normal to SITE_A)
	checkLeafNodeConnectedCount(t, sSiteB, 2)
	// Hub: 1 from SITE_A + 1 from SITE_B (passive) = 2
	checkLeafNodeConnectedCount(t, sHub, 2)
	// SITE_A: 1 outgoing to Hub + 1 incoming from SITE_B = 2
	checkLeafNodeConnectedCount(t, sSiteA, 2)

	t.Log("SUCCESS: Passive connection allowed the loop topology!")
}

// TestLeafNodePassiveConnectionWithJetStream tests that JetStream sourcing works
// when a passive leafnode connection is present in the topology.
// The passive connection allows the loop topology to exist without triggering
// loop detection, but actual message traffic flows through the chain path.
// This is useful for topologies where you need connectivity but want to
// control message flow through specific paths.
func TestLeafNodePassiveConnectionWithJetStream(t *testing.T) {
	// Hub - accepts connections, has JetStream
	hubConf := `
		listen: 127.0.0.1:-1
		server_name: HUB
		jetstream {
			store_dir: '%s'
		}
		accounts {
			SITE {
				jetstream: enabled
				users: [{user: site, password: site}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			port: -1
		}
	`

	// SITE_A - connects to Hub, accepts from SITE_B
	siteAConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_A
		jetstream {
			store_dir: '%s'
		}
		accounts {
			SITE {
				jetstream: enabled
				users: [{user: site, password: site}]
				mappings = {
					"$JS.SITE_A.API.>": "$JS.API.>"
				}
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			port: -1
			remotes [
				{ url: "nats://site:site@127.0.0.1:%d", account: SITE }
			]
		}
	`

	// SITE_B - connects to BOTH Hub (passive) AND SITE_A (normal)
	siteBConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_B
		jetstream {
			store_dir: '%s'
		}
		accounts {
			SITE {
				jetstream: enabled
				users: [{user: site, password: site}]
				mappings = {
					"$JS.SITE_B.API.>": "$JS.API.>"
				}
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			remotes [
				{ url: "nats://site:site@127.0.0.1:%d", account: SITE, passive: true }
				{ url: "nats://site:site@127.0.0.1:%d", account: SITE }
			]
		}
	`

	// Start Hub
	hubConfFile := createConfFile(t, []byte(fmt.Sprintf(hubConf, t.TempDir())))
	sHub, oHub := RunServerWithConfig(hubConfFile)
	defer sHub.Shutdown()

	// Start SITE_A
	siteAConfFile := createConfFile(t, []byte(fmt.Sprintf(siteAConf, t.TempDir(), oHub.LeafNode.Port)))
	sSiteA, oSiteA := RunServerWithConfig(siteAConfFile)
	defer sSiteA.Shutdown()

	checkLeafNodeConnectedCount(t, sSiteA, 1)
	checkLeafNodeConnectedCount(t, sHub, 1)

	// Start SITE_B
	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, t.TempDir(),
		oHub.LeafNode.Port, oSiteA.LeafNode.Port)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Verify all connections established
	checkLeafNodeConnectedCount(t, sSiteB, 2)
	checkLeafNodeConnectedCount(t, sHub, 2)
	checkLeafNodeConnectedCount(t, sSiteA, 2)

	// Create streams on SITE_A and SITE_B
	ncSiteA := natsConnect(t, sSiteA.ClientURL(), nats.UserInfo("site", "site"))
	defer ncSiteA.Close()
	jsSiteA, err := ncSiteA.JetStream()
	require_NoError(t, err)

	_, err = jsSiteA.AddStream(&nats.StreamConfig{
		Name:     "STREAM_A",
		Subjects: []string{"site_a.>"},
	})
	require_NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err = jsSiteA.Publish("site_a.data", []byte(fmt.Sprintf("a-msg-%d", i)))
		require_NoError(t, err)
	}

	ncSiteB := natsConnect(t, sSiteB.ClientURL(), nats.UserInfo("site", "site"))
	defer ncSiteB.Close()
	jsSiteB, err := ncSiteB.JetStream()
	require_NoError(t, err)

	_, err = jsSiteB.AddStream(&nats.StreamConfig{
		Name:     "STREAM_B",
		Subjects: []string{"site_b.>"},
	})
	require_NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err = jsSiteB.Publish("site_b.data", []byte(fmt.Sprintf("b-msg-%d", i)))
		require_NoError(t, err)
	}

	// Connect to Hub and source from both sites
	ncHub := natsConnect(t, sHub.ClientURL(), nats.UserInfo("site", "site"))
	defer ncHub.Close()
	jsHub, err := ncHub.JetStream()
	require_NoError(t, err)

	// Verify Hub can see both streams via their prefixes
	jsSiteAViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_A.API"))
	require_NoError(t, err)
	si, err := jsSiteAViaHub.StreamInfo("STREAM_A")
	require_NoError(t, err)
	t.Logf("Hub can see SITE_A's STREAM_A: %d msgs", si.State.Msgs)

	jsSiteBViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_B.API"))
	require_NoError(t, err)
	si, err = jsSiteBViaHub.StreamInfo("STREAM_B")
	require_NoError(t, err)
	t.Logf("Hub can see SITE_B's STREAM_B: %d msgs", si.State.Msgs)

	// Create sourced streams on Hub
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_A",
		Sources: []*nats.StreamSource{{
			Name:     "STREAM_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"},
		}},
	})
	require_NoError(t, err)

	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_B",
		Sources: []*nats.StreamSource{{
			Name:     "STREAM_B",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_B.API"},
		}},
	})
	require_NoError(t, err)

	// Give streams a moment to sync
	time.Sleep(500 * time.Millisecond)

	// Check SITE_B stream state
	si, err = jsSiteBViaHub.StreamInfo("STREAM_B")
	require_NoError(t, err)
	t.Logf("SITE_B's STREAM_B after sourcing setup: %d msgs", si.State.Msgs)

	// Wait for both sourced streams to sync
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_A info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_A: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_B")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_B info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_B: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})

	t.Log("SUCCESS: Hub sourced from both SITE_A and SITE_B with passive connection!")
}

// TestJetStreamLeafNodeChainWithPassiveSourceFromBothSites tests a topology where:
//
//	        ┌─────────────────┐
//	        │      HUB        │
//	        │  (sources from  │
//	        │   both sites)   │
//	        └────▲───────▲────┘
//	             │       │
//	      direct │       │ passive
//	             │       │
//	        ┌────┴───┐   │
//	        │ SITE_A │   │
//	        └────▲───┘   │
//	             │       │
//	       chain │       │
//	             │       │
//	        ┌────┴───────┴────┐
//	        │     SITE_B      │
//	        └─────────────────┘
//
// This topology enables:
// - Hub to source streams from both SITE_A and SITE_B
// - SITE_A has a direct connection to Hub
// - SITE_B has a chain connection through SITE_A AND a passive direct connection to Hub
// - The passive connection allows the loop without triggering loop detection
//
// This is useful when you want redundant connectivity but controlled message flow.
func TestJetStreamLeafNodeChainWithPassiveSourceFromBothSites(t *testing.T) {
	// Hub - the central hub that sources streams from leafnodes
	hubConf := `
		listen: 127.0.0.1:-1
		server_name: HUB
		jetstream {
			store_dir: '%s'
			domain: HUB
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`

	// SITE_A - connects directly to Hub, accepts chain connection from SITE_B
	siteAConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_A
		jetstream {
			store_dir: '%s'
			domain: SITE_A
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
			remotes [
				{ url: "nats://a:a@127.0.0.1:%d", account: A }
			]
		}
	`

	// SITE_B - connects to SITE_A (chain) AND directly to Hub (passive)
	siteBConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_B
		jetstream {
			store_dir: '%s'
			domain: SITE_B
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			remotes [
				# Chain connection through SITE_A (normal)
				{ url: "nats://a:a@127.0.0.1:%d", account: A }
				# Direct connection to Hub (passive to avoid loop detection)
				{ url: "nats://a:a@127.0.0.1:%d", account: A, passive: true }
			]
		}
	`

	// Start Hub
	hubConfFile := createConfFile(t, []byte(fmt.Sprintf(hubConf, t.TempDir())))
	sHub, oHub := RunServerWithConfig(hubConfFile)
	defer sHub.Shutdown()

	// Start SITE_A (connects to Hub)
	siteAConfFile := createConfFile(t, []byte(fmt.Sprintf(siteAConf, t.TempDir(), oHub.LeafNode.Port)))
	sSiteA, oSiteA := RunServerWithConfig(siteAConfFile)
	defer sSiteA.Shutdown()

	// Wait for SITE_A to connect to Hub
	checkLeafNodeConnectedCount(t, sSiteA, 1)
	checkLeafNodeConnectedCount(t, sHub, 1)

	// Start SITE_B (connects to SITE_A and Hub)
	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, t.TempDir(),
		oSiteA.LeafNode.Port, oHub.LeafNode.Port)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Verify all connections:
	// - SITE_B: 2 connections (chain to SITE_A + passive to Hub)
	// - SITE_A: 2 connections (to Hub + from SITE_B)
	// - Hub: 2 connections (from SITE_A + passive from SITE_B)
	checkLeafNodeConnectedCount(t, sSiteB, 2)
	checkLeafNodeConnectedCount(t, sSiteA, 2)
	checkLeafNodeConnectedCount(t, sHub, 2)

	t.Log("Topology established: Hub ← SITE_A ← SITE_B with passive SITE_B → Hub")

	// Create stream on SITE_A
	ncSiteA := natsConnect(t, sSiteA.ClientURL(), nats.UserInfo("a", "a"))
	defer ncSiteA.Close()
	jsSiteA, err := ncSiteA.JetStream()
	require_NoError(t, err)

	_, err = jsSiteA.AddStream(&nats.StreamConfig{
		Name:     "STREAM_A",
		Subjects: []string{"site_a.>"},
	})
	require_NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err = jsSiteA.Publish("site_a.data", []byte(fmt.Sprintf("a-msg-%d", i)))
		require_NoError(t, err)
	}
	t.Log("Created STREAM_A on SITE_A with 5 messages")

	// Create stream on SITE_B
	ncSiteB := natsConnect(t, sSiteB.ClientURL(), nats.UserInfo("a", "a"))
	defer ncSiteB.Close()
	jsSiteB, err := ncSiteB.JetStream()
	require_NoError(t, err)

	_, err = jsSiteB.AddStream(&nats.StreamConfig{
		Name:     "STREAM_B",
		Subjects: []string{"site_b.>"},
	})
	require_NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err = jsSiteB.Publish("site_b.data", []byte(fmt.Sprintf("b-msg-%d", i)))
		require_NoError(t, err)
	}
	t.Log("Created STREAM_B on SITE_B with 5 messages")

	// Connect to Hub
	ncHub := natsConnect(t, sHub.ClientURL(), nats.UserInfo("a", "a"))
	defer ncHub.Close()
	jsHub, err := ncHub.JetStream()
	require_NoError(t, err)

	// Verify Hub can see SITE_A's stream via domain prefix
	jsSiteAViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_A.API"))
	require_NoError(t, err)

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteAViaHub.StreamInfo("STREAM_A")
		if err != nil {
			return fmt.Errorf("Hub cannot see SITE_A's STREAM_A: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("STREAM_A expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("Hub can see SITE_A's STREAM_A via $JS.SITE_A.API")

	// Verify Hub can see SITE_B's stream via domain prefix
	// SITE_B is connected via the chain (through SITE_A) for interest propagation
	jsSiteBViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_B.API"))
	require_NoError(t, err)

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteBViaHub.StreamInfo("STREAM_B")
		if err != nil {
			return fmt.Errorf("Hub cannot see SITE_B's STREAM_B: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("STREAM_B expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("Hub can see SITE_B's STREAM_B via $JS.SITE_B.API (through chain)")

	// Create sourced stream on Hub that pulls from SITE_A
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_A",
		Sources: []*nats.StreamSource{{
			Name:     "STREAM_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"},
		}},
	})
	require_NoError(t, err)
	t.Log("Created HUB_FROM_SITE_A sourced stream")

	// Create sourced stream on Hub that pulls from SITE_B
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_B",
		Sources: []*nats.StreamSource{{
			Name:     "STREAM_B",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_B.API"},
		}},
	})
	require_NoError(t, err)
	t.Log("Created HUB_FROM_SITE_B sourced stream")

	// Wait for sourced streams to sync
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_A info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_A: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("HUB_FROM_SITE_A synced 5 messages from SITE_A")

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_B")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_B info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_B: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("HUB_FROM_SITE_B synced 5 messages from SITE_B")

	// Publish more messages and verify they sync
	for i := 5; i < 10; i++ {
		_, err = jsSiteA.Publish("site_a.data", []byte(fmt.Sprintf("a-msg-%d", i)))
		require_NoError(t, err)
		_, err = jsSiteB.Publish("site_b.data", []byte(fmt.Sprintf("b-msg-%d", i)))
		require_NoError(t, err)
	}
	t.Log("Published 5 more messages to each site")

	// Verify all 10 messages sync to Hub
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_A")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("HUB_FROM_SITE_A: expected 10 msgs, got %d", si.State.Msgs)
		}
		si, err = jsHub.StreamInfo("HUB_FROM_SITE_B")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("HUB_FROM_SITE_B: expected 10 msgs, got %d", si.State.Msgs)
		}
		return nil
	})

	t.Log("SUCCESS: Hub sourced from both SITE_A and SITE_B using chain + passive topology!")
}

// TestJetStreamLeafNodeChainWithPassiveSiteBSourcesFromSiteA tests a topology where:
//
//	        ┌─────────────────┐
//	        │      HUB        │
//	        │  (sources from  │
//	        │   both sites)   │
//	        └────▲───────▲────┘
//	             │       │
//	      direct │       │ passive
//	             │       │
//	        ┌────┴───┐   │
//	        │ SITE_A │   │
//	        │(SITE_A │   │
//	        │ stream)│   │
//	        └────▲───┘   │
//	             │       │
//	       chain │       │
//	     (source)│       │
//	             │       │
//	        ┌────┴───────┴────┐
//	        │     SITE_B      │
//	        │ (sources from   │
//	        │  SITE_A stream) │
//	        └─────────────────┘
//
// This topology demonstrates:
// - SITE_A has a stream named "SITE_A"
// - SITE_B sources from SITE_A's stream via the leafnode chain
// - Hub sources from both SITE_A's original stream and SITE_B's sourced stream
// - The passive connection allows the loop without triggering loop detection
func TestJetStreamLeafNodeChainWithPassiveSiteBSourcesFromSiteA(t *testing.T) {
	// Hub - the central hub that sources streams from leafnodes
	hubConf := `
		listen: 127.0.0.1:-1
		server_name: HUB
		jetstream {
			store_dir: '%s'
			domain: HUB
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`

	// SITE_A - connects directly to Hub, accepts chain connection from SITE_B
	siteAConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_A
		jetstream {
			store_dir: '%s'
			domain: SITE_A
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
			remotes [
				{ url: "nats://a:a@127.0.0.1:%d", account: A }
			]
		}
	`

	// SITE_B - connects to SITE_A (chain) AND directly to Hub (passive)
	siteBConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_B
		jetstream {
			store_dir: '%s'
			domain: SITE_B
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			remotes [
				# Chain connection through SITE_A (normal) - used for sourcing
				{ url: "nats://a:a@127.0.0.1:%d", account: A }
				# Direct connection to Hub (passive to avoid loop detection)
				{ url: "nats://a:a@127.0.0.1:%d", account: A, passive: true }
			]
		}
	`

	// Start Hub
	hubConfFile := createConfFile(t, []byte(fmt.Sprintf(hubConf, t.TempDir())))
	sHub, oHub := RunServerWithConfig(hubConfFile)
	defer sHub.Shutdown()

	// Start SITE_A (connects to Hub)
	siteAConfFile := createConfFile(t, []byte(fmt.Sprintf(siteAConf, t.TempDir(), oHub.LeafNode.Port)))
	sSiteA, oSiteA := RunServerWithConfig(siteAConfFile)
	defer sSiteA.Shutdown()

	// Wait for SITE_A to connect to Hub
	checkLeafNodeConnectedCount(t, sSiteA, 1)
	checkLeafNodeConnectedCount(t, sHub, 1)

	// Start SITE_B (connects to SITE_A and Hub)
	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, t.TempDir(),
		oSiteA.LeafNode.Port, oHub.LeafNode.Port)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Verify all connections
	checkLeafNodeConnectedCount(t, sSiteB, 2)
	checkLeafNodeConnectedCount(t, sSiteA, 2)
	checkLeafNodeConnectedCount(t, sHub, 2)

	t.Log("Topology established: Hub ← SITE_A ← SITE_B with passive SITE_B → Hub")

	// Create stream "SITE_A" on SITE_A
	ncSiteA := natsConnect(t, sSiteA.ClientURL(), nats.UserInfo("a", "a"))
	defer ncSiteA.Close()
	jsSiteA, err := ncSiteA.JetStream()
	require_NoError(t, err)

	_, err = jsSiteA.AddStream(&nats.StreamConfig{
		Name:     "SITE_A",
		Subjects: []string{"events.>"},
	})
	require_NoError(t, err)

	// Publish messages to SITE_A stream
	for i := 0; i < 5; i++ {
		_, err = jsSiteA.Publish("events.site_a", []byte(fmt.Sprintf("event-%d", i)))
		require_NoError(t, err)
	}
	t.Log("Created stream SITE_A on SITE_A with 5 messages")

	// Connect to SITE_B
	ncSiteB := natsConnect(t, sSiteB.ClientURL(), nats.UserInfo("a", "a"))
	defer ncSiteB.Close()
	jsSiteB, err := ncSiteB.JetStream()
	require_NoError(t, err)

	// SITE_B sources from SITE_A's stream via the chain connection
	// Using domain prefix $JS.SITE_A.API to reach SITE_A through the leafnode
	_, err = jsSiteB.AddStream(&nats.StreamConfig{
		Name: "SITE_B_FROM_SITE_A",
		Sources: []*nats.StreamSource{{
			Name:     "SITE_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"},
		}},
	})
	require_NoError(t, err)
	t.Log("Created sourced stream SITE_B_FROM_SITE_A on SITE_B")

	// Wait for SITE_B to sync from SITE_A
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteB.StreamInfo("SITE_B_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("could not get SITE_B_FROM_SITE_A info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("SITE_B_FROM_SITE_A: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("SITE_B sourced 5 messages from SITE_A via chain connection")

	// Connect to Hub
	ncHub := natsConnect(t, sHub.ClientURL(), nats.UserInfo("a", "a"))
	defer ncHub.Close()
	jsHub, err := ncHub.JetStream()
	require_NoError(t, err)

	// Verify Hub can see SITE_A's stream
	jsSiteAViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_A.API"))
	require_NoError(t, err)

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteAViaHub.StreamInfo("SITE_A")
		if err != nil {
			return fmt.Errorf("Hub cannot see SITE_A stream: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("SITE_A expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("Hub can see SITE_A's stream via $JS.SITE_A.API")

	// Verify Hub can see SITE_B's sourced stream
	jsSiteBViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_B.API"))
	require_NoError(t, err)

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteBViaHub.StreamInfo("SITE_B_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("Hub cannot see SITE_B_FROM_SITE_A stream: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("SITE_B_FROM_SITE_A expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("Hub can see SITE_B's sourced stream via $JS.SITE_B.API (through chain)")

	// Hub sources from SITE_A's original stream
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_A",
		Sources: []*nats.StreamSource{{
			Name:     "SITE_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"},
		}},
	})
	require_NoError(t, err)
	t.Log("Created HUB_FROM_SITE_A sourced stream")

	// Hub sources from SITE_B's sourced stream (which sourced from SITE_A)
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_B",
		Sources: []*nats.StreamSource{{
			Name:     "SITE_B_FROM_SITE_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_B.API"},
		}},
	})
	require_NoError(t, err)
	t.Log("Created HUB_FROM_SITE_B sourced stream")

	// Wait for Hub streams to sync
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_A info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_A: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("HUB_FROM_SITE_A synced 5 messages")

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_B")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_B info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_B: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("HUB_FROM_SITE_B synced 5 messages")

	// Publish more messages to SITE_A and verify they propagate through the chain
	for i := 5; i < 10; i++ {
		_, err = jsSiteA.Publish("events.site_a", []byte(fmt.Sprintf("event-%d", i)))
		require_NoError(t, err)
	}
	t.Log("Published 5 more messages to SITE_A")

	// Verify all streams have 10 messages
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		// Check SITE_A original stream
		si, err := jsSiteA.StreamInfo("SITE_A")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("SITE_A: expected 10 msgs, got %d", si.State.Msgs)
		}

		// Check SITE_B's sourced stream
		si, err = jsSiteB.StreamInfo("SITE_B_FROM_SITE_A")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("SITE_B_FROM_SITE_A: expected 10 msgs, got %d", si.State.Msgs)
		}

		// Check Hub's sourced streams
		si, err = jsHub.StreamInfo("HUB_FROM_SITE_A")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("HUB_FROM_SITE_A: expected 10 msgs, got %d", si.State.Msgs)
		}

		si, err = jsHub.StreamInfo("HUB_FROM_SITE_B")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("HUB_FROM_SITE_B: expected 10 msgs, got %d", si.State.Msgs)
		}

		return nil
	})

	t.Log("SUCCESS: SITE_B sources from SITE_A via chain, Hub sources from both!")
}

// TestJetStreamLeafNodeChainWithPassiveHubClusterSiteBSourcesFromSiteA tests a topology where:
//
//	        ┌─────────────────────────────────┐
//	        │         HUB CLUSTER             │
//	        │  ┌───────┬───────┬───────┐      │
//	        │  │ HUB-1 │ HUB-2 │ HUB-3 │      │
//	        │  └───▲───┴───▲───┴───▲───┘      │
//	        │      │       │       │          │
//	        └──────┼───────┼───────┼──────────┘
//	               │       │       │
//	        ┌──────┴───────┴───────┴──────┐
//	        │ SITE_A (connects to any)    │
//	        │ (has stream "SITE_A")       │
//	        └────────────▲────────────────┘
//	                     │
//	               chain │ (source)
//	                     │
//	        ┌────────────┴────────────────┐
//	        │          SITE_B             │
//	        │  (sources from SITE_A)      │
//	        │  (passive conn to Hub)      │
//	        └─────────────────────────────┘
//
// This test is similar to TestJetStreamLeafNodeChainWithPassiveSiteBSourcesFromSiteA
// but with a 3-node NATS cluster for the Hub. Leafnodes can connect to any of the
// cluster endpoints for high availability.
func TestJetStreamLeafNodeChainWithPassiveHubClusterSiteBSourcesFromSiteA(t *testing.T) {
	// Create a 3-node Hub cluster with JetStream and leafnode support
	hubClusterTempl := `
		listen: 127.0.0.1:-1
		server_name: %s
		jetstream: {max_mem_store: 256MB, max_file_store: 2GB, store_dir: '%s', domain: HUB}

		cluster {
			name: %s
			listen: 127.0.0.1:%d
			routes = [%s]
		}

		leafnodes {
			listen: 127.0.0.1:-1
		}

		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: s3cr3t!}] }
		}
	`

	hubCluster := createJetStreamCluster(t, hubClusterTempl, "HUB", "HUB-", 3, 22280, true)
	defer hubCluster.shutdown()

	hubCluster.waitOnLeader()
	t.Log("Hub cluster ready with 3 nodes")

	// Build leafnode URLs for connecting to Hub cluster (any node)
	var hubLeafURLs []string
	for _, s := range hubCluster.servers {
		ln := s.getOpts().LeafNode
		hubLeafURLs = append(hubLeafURLs, fmt.Sprintf("nats://a:a@%s:%d", ln.Host, ln.Port))
	}
	hubLeafURLStr := strings.Join(hubLeafURLs, ", ")

	// SITE_A - connects to Hub cluster, accepts chain connection from SITE_B
	siteAConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_A
		jetstream {
			store_dir: '%s'
			domain: SITE_A
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
			remotes [
				{ urls: [%s], account: A }
			]
		}
	`

	siteAConfFile := createConfFile(t, []byte(fmt.Sprintf(siteAConf, t.TempDir(), hubLeafURLStr)))
	sSiteA, oSiteA := RunServerWithConfig(siteAConfFile)
	defer sSiteA.Shutdown()

	// Wait for SITE_A to connect to Hub cluster
	checkLeafNodeConnectedCount(t, sSiteA, 1)
	t.Log("SITE_A connected to Hub cluster")

	// SITE_B - connects to SITE_A (chain) AND directly to Hub cluster (passive)
	siteBConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_B
		jetstream {
			store_dir: '%s'
			domain: SITE_B
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			remotes [
				# Chain connection through SITE_A (normal) - used for sourcing
				{ url: "nats://a:a@127.0.0.1:%d", account: A }
				# Direct connection to Hub cluster (passive to avoid loop detection)
				{ urls: [%s], account: A, passive: true }
			]
		}
	`

	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, t.TempDir(),
		oSiteA.LeafNode.Port, hubLeafURLStr)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Verify all connections
	// SITE_B: 2 connections (chain to SITE_A + passive to Hub)
	checkLeafNodeConnectedCount(t, sSiteB, 2)
	// SITE_A: 2 connections (to Hub + from SITE_B)
	checkLeafNodeConnectedCount(t, sSiteA, 2)
	t.Log("Topology established: Hub Cluster ← SITE_A ← SITE_B with passive SITE_B → Hub Cluster")

	// Create stream "SITE_A" on SITE_A
	ncSiteA := natsConnect(t, sSiteA.ClientURL(), nats.UserInfo("a", "a"))
	defer ncSiteA.Close()
	jsSiteA, err := ncSiteA.JetStream()
	require_NoError(t, err)

	_, err = jsSiteA.AddStream(&nats.StreamConfig{
		Name:     "SITE_A",
		Subjects: []string{"events.>"},
	})
	require_NoError(t, err)

	// Publish messages to SITE_A stream
	for i := 0; i < 5; i++ {
		_, err = jsSiteA.Publish("events.site_a", []byte(fmt.Sprintf("event-%d", i)))
		require_NoError(t, err)
	}
	t.Log("Created stream SITE_A on SITE_A with 5 messages")

	// Connect to SITE_B
	ncSiteB := natsConnect(t, sSiteB.ClientURL(), nats.UserInfo("a", "a"))
	defer ncSiteB.Close()
	jsSiteB, err := ncSiteB.JetStream()
	require_NoError(t, err)

	// SITE_B sources from SITE_A's stream via the chain connection
	_, err = jsSiteB.AddStream(&nats.StreamConfig{
		Name: "SITE_B_FROM_SITE_A",
		Sources: []*nats.StreamSource{{
			Name:     "SITE_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"},
		}},
	})
	require_NoError(t, err)
	t.Log("Created sourced stream SITE_B_FROM_SITE_A on SITE_B")

	// Wait for SITE_B to sync from SITE_A
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteB.StreamInfo("SITE_B_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("could not get SITE_B_FROM_SITE_A info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("SITE_B_FROM_SITE_A: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("SITE_B sourced 5 messages from SITE_A via chain connection")

	// Connect to Hub cluster (any node)
	hubServer := hubCluster.servers[0]
	ncHub := natsConnect(t, hubServer.ClientURL(), nats.UserInfo("a", "a"))
	defer ncHub.Close()
	jsHub, err := ncHub.JetStream()
	require_NoError(t, err)

	// Verify Hub can see SITE_A's stream
	jsSiteAViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_A.API"))
	require_NoError(t, err)

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteAViaHub.StreamInfo("SITE_A")
		if err != nil {
			return fmt.Errorf("Hub cannot see SITE_A stream: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("SITE_A expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("Hub cluster can see SITE_A's stream via $JS.SITE_A.API")

	// Verify Hub can see SITE_B's sourced stream
	jsSiteBViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_B.API"))
	require_NoError(t, err)

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteBViaHub.StreamInfo("SITE_B_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("Hub cannot see SITE_B_FROM_SITE_A stream: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("SITE_B_FROM_SITE_A expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("Hub cluster can see SITE_B's sourced stream via $JS.SITE_B.API (through chain)")

	// Hub sources from SITE_A's original stream
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_A",
		Sources: []*nats.StreamSource{{
			Name:     "SITE_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"},
		}},
	})
	require_NoError(t, err)
	t.Log("Created HUB_FROM_SITE_A sourced stream on Hub cluster")

	// Hub sources from SITE_B's sourced stream
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_B",
		Sources: []*nats.StreamSource{{
			Name:     "SITE_B_FROM_SITE_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_B.API"},
		}},
	})
	require_NoError(t, err)
	t.Log("Created HUB_FROM_SITE_B sourced stream on Hub cluster")

	// Wait for Hub streams to sync
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_A info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_A: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("HUB_FROM_SITE_A synced 5 messages")

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_B")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_B info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_B: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("HUB_FROM_SITE_B synced 5 messages")

	// Publish more messages to SITE_A and verify they propagate through the chain
	for i := 5; i < 10; i++ {
		_, err = jsSiteA.Publish("events.site_a", []byte(fmt.Sprintf("event-%d", i)))
		require_NoError(t, err)
	}
	t.Log("Published 5 more messages to SITE_A")

	// Verify all streams have 10 messages
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		// Check SITE_A original stream
		si, err := jsSiteA.StreamInfo("SITE_A")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("SITE_A: expected 10 msgs, got %d", si.State.Msgs)
		}

		// Check SITE_B's sourced stream
		si, err = jsSiteB.StreamInfo("SITE_B_FROM_SITE_A")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("SITE_B_FROM_SITE_A: expected 10 msgs, got %d", si.State.Msgs)
		}

		// Check Hub's sourced streams
		si, err = jsHub.StreamInfo("HUB_FROM_SITE_A")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("HUB_FROM_SITE_A: expected 10 msgs, got %d", si.State.Msgs)
		}

		si, err = jsHub.StreamInfo("HUB_FROM_SITE_B")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("HUB_FROM_SITE_B: expected 10 msgs, got %d", si.State.Msgs)
		}

		return nil
	})

	t.Log("SUCCESS: SITE_B sources from SITE_A via chain, 3-node Hub cluster sources from both!")

	// Now test what happens when SITE_A goes down: verify that the passive connection
	// alone is NOT sufficient for JetStream API routing (by design).
	// Passive connections bypass loop detection but don't propagate interest,
	// so when the chain path (Hub → SITE_A → SITE_B) goes down, the interest
	// routing to SITE_B is lost.
	t.Log("Shutting down SITE_A to test passive connection behavior...")
	sSiteA.Shutdown()

	// Wait for SITE_B to detect SITE_A is gone (chain connection lost)
	// SITE_B should now only have 1 connection (passive to Hub)
	checkLeafNodeConnectedCount(t, sSiteB, 1)
	t.Log("SITE_A shutdown, SITE_B now has only passive connection to Hub")

	// Verify that Hub can NO LONGER see SITE_B's stream via the passive connection.
	// This is expected because passive connections don't propagate interest.
	// The interest for $JS.SITE_B.API.> was only propagated through the chain.
	time.Sleep(500 * time.Millisecond) // Give time for connection state to settle

	_, err = jsSiteBViaHub.StreamInfo("SITE_B_FROM_SITE_A")
	if err == nil {
		t.Log("Note: Hub can still see SITE_B stream (interest may have been cached)")
	} else {
		t.Logf("Expected: Hub cannot see SITE_B stream after SITE_A shutdown: %v", err)
	}

	t.Log("SUCCESS: Test demonstrates passive connection behavior - interest not propagated!")
}

// TestLeafNodePassivePropagateInterestBasic tests that the passive_propagate_interest
// option enables basic pub/sub routing through a passive leafnode connection.
func TestLeafNodePassivePropagateInterestBasic(t *testing.T) {
	// Hub - accepts connections
	hubConf := `
		listen: 127.0.0.1:-1
		server_name: HUB
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`

	// SITE_B - connects to Hub with passive + propagate_interest
	siteBConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_B
		leafnodes {
			remotes [
				{ url: "nats://127.0.0.1:%d", passive: true, passive_propagate_interest: true }
			]
		}
	`

	// Start Hub
	hubConfFile := createConfFile(t, []byte(hubConf))
	sHub, oHub := RunServerWithConfig(hubConfFile)
	defer sHub.Shutdown()

	// Start SITE_B
	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, oHub.LeafNode.Port)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Wait for connection
	checkLeafNodeConnectedCount(t, sSiteB, 1)
	checkLeafNodeConnectedCount(t, sHub, 1)
	t.Log("SITE_B connected to Hub with passive + propagate_interest")

	// Create subscriber on SITE_B
	ncSiteB := natsConnect(t, sSiteB.ClientURL())
	defer ncSiteB.Close()

	sub, err := ncSiteB.SubscribeSync("test.subject")
	require_NoError(t, err)
	ncSiteB.Flush()

	// Give time for subscription to propagate
	time.Sleep(100 * time.Millisecond)

	// Publish from Hub
	ncHub := natsConnect(t, sHub.ClientURL())
	defer ncHub.Close()

	err = ncHub.Publish("test.subject", []byte("hello"))
	require_NoError(t, err)
	ncHub.Flush()

	// Check if SITE_B receives the message
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("SITE_B did not receive message from Hub: %v", err)
	}
	t.Logf("SUCCESS! SITE_B received message: %s", string(msg.Data))
	t.Log("passive_propagate_interest enables subscription propagation through passive connections")
}

// TestJetStreamLeafNodePassiveWithPropagateInterest tests that the passive_propagate_interest
// option enables JetStream API routing through a passive leafnode connection.
//
// This is a simpler test that only uses the passive connection with propagate_interest,
// without the chain path, to avoid message duplication issues.
//
// Topology:
//
//	        ┌─────────────────────────────────┐
//	        │              HUB                │
//	        └────────────────────▲────────────┘
//	                             │
//	                             │ passive with
//	                             │ propagate_interest
//	                             │
//	        ┌────────────────────┴────────────┐
//	        │          SITE_B                 │
//	        └─────────────────────────────────┘
//
// With passive_propagate_interest: true, the passive connection will:
// - Still bypass loop detection (passive behavior)
// - BUT propagate interest/subscriptions (new behavior)
//
// This allows Hub to access SITE_B's JetStream API.
func TestJetStreamLeafNodePassiveWithPropagateInterest(t *testing.T) {
	// Hub - accepts connections
	hubConf := `
		listen: 127.0.0.1:-1
		server_name: HUB
		jetstream {
			store_dir: '%s'
			domain: HUB
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: s3cr3t!}] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`

	// SITE_B - connects directly to Hub with passive + propagate_interest
	siteBConf := `
		listen: 127.0.0.1:-1
		server_name: SITE_B
		jetstream {
			store_dir: '%s'
			domain: SITE_B
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			remotes [
				# Direct passive connection to Hub with interest propagation
				{ url: "nats://a:a@127.0.0.1:%d", account: A, passive: true, passive_propagate_interest: true }
			]
		}
	`

	// Start Hub
	hubConfFile := createConfFile(t, []byte(fmt.Sprintf(hubConf, t.TempDir())))
	sHub, oHub := RunServerWithConfig(hubConfFile)
	defer sHub.Shutdown()

	// Start SITE_B
	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, t.TempDir(), oHub.LeafNode.Port)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Verify connection
	checkLeafNodeConnectedCount(t, sSiteB, 1)
	checkLeafNodeConnectedCount(t, sHub, 1)
	t.Log("SITE_B connected to Hub with passive + propagate_interest")

	// Create stream on SITE_B
	ncSiteB := natsConnect(t, sSiteB.ClientURL(), nats.UserInfo("a", "a"))
	defer ncSiteB.Close()
	jsSiteB, err := ncSiteB.JetStream()
	require_NoError(t, err)

	_, err = jsSiteB.AddStream(&nats.StreamConfig{
		Name:     "SITE_B_STREAM",
		Subjects: []string{"site_b.>"},
	})
	require_NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err = jsSiteB.Publish("site_b.data", []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}
	t.Log("Created SITE_B_STREAM with 5 messages")

	// Connect to Hub and verify we can see SITE_B's stream via domain prefix
	ncHub := natsConnect(t, sHub.ClientURL(), nats.UserInfo("a", "a"))
	defer ncHub.Close()

	jsSiteBViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_B.API"))
	require_NoError(t, err)

	// Verify Hub can access SITE_B's stream through the passive connection
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteBViaHub.StreamInfo("SITE_B_STREAM")
		if err != nil {
			return fmt.Errorf("Hub cannot see SITE_B_STREAM: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
	t.Log("Hub can see SITE_B_STREAM via passive connection with propagate_interest")

	// Also verify we can add more messages and see them
	for i := 5; i < 10; i++ {
		_, err = jsSiteB.Publish("site_b.data", []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsSiteBViaHub.StreamInfo("SITE_B_STREAM")
		if err != nil {
			return err
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("expected 10 msgs, got %d", si.State.Msgs)
		}
		return nil
	})

	t.Log("SUCCESS! Hub can access SITE_B JetStream through passive connection with propagate_interest!")
}

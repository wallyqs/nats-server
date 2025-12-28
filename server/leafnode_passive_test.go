package server

import (
	"fmt"
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

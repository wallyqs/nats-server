package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamLeafNodeChainSourceFromBothSites tests a chain topology where:
//   Hub ← SITE_A ← SITE_B
// The hub can source streams from both SITE_A and SITE_B through the chain.
// SITE_B traffic flows through SITE_A to reach the hub.
func TestJetStreamLeafNodeChainSourceFromBothSites(t *testing.T) {
	// SITE_B - end of chain, only connects to SITE_A
	// Has mapping to handle $JS.SITE_B.API.> requests
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
			port: -1
		}
	`

	// SITE_A - middle of chain, connects to hub and accepts SITE_B
	// Has mapping to handle $JS.SITE_A.API.> requests
	// Does NOT have mapping for $JS.SITE_B.API.> - those flow through to SITE_B
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
				{ url: "nats://admin:admin@127.0.0.1:%d", account: "$SYS" }
			]
		}
	`

	// Hub - start of chain, only connects to SITE_A
	// Can source from both SITE_A ($JS.SITE_A.API) and SITE_B ($JS.SITE_B.API)
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
			remotes [
				{ url: "nats://site:site@127.0.0.1:%d", account: SITE }
				{ url: "nats://admin:admin@127.0.0.1:%d", account: "$SYS" }
			]
		}
	`

	// Start SITE_B first (end of chain)
	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, t.TempDir())))
	sSiteB, oSiteB := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Start SITE_A (connects to SITE_B)
	siteAConfFile := createConfFile(t, []byte(fmt.Sprintf(siteAConf, t.TempDir(), 
		oSiteB.LeafNode.Port, oSiteB.LeafNode.Port)))
	sSiteA, oSiteA := RunServerWithConfig(siteAConfFile)
	defer sSiteA.Shutdown()

	// Wait for SITE_A to connect to SITE_B
	checkLeafNodeConnectedCount(t, sSiteA, 2)
	checkLeafNodeConnectedCount(t, sSiteB, 2)

	// Start Hub (connects to SITE_A)
	hubConfFile := createConfFile(t, []byte(fmt.Sprintf(hubConf, t.TempDir(),
		oSiteA.LeafNode.Port, oSiteA.LeafNode.Port)))
	sHub, _ := RunServerWithConfig(hubConfFile)
	defer sHub.Shutdown()

	// Wait for Hub to connect to SITE_A
	checkLeafNodeConnectedCount(t, sHub, 2)
	checkLeafNodeConnectedCount(t, sSiteA, 4) // 2 from SITE_B + 2 from Hub

	// Create stream on SITE_A
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

	// Create stream on SITE_B
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

	// Connect to Hub
	ncHub := natsConnect(t, sHub.ClientURL(), nats.UserInfo("site", "site"))
	defer ncHub.Close()
	jsHub, err := ncHub.JetStream()
	require_NoError(t, err)

	// Verify Hub can access SITE_A's JetStream API
	jsSiteAViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_A.API"))
	require_NoError(t, err)
	si, err := jsSiteAViaHub.StreamInfo("STREAM_A")
	require_NoError(t, err)
	t.Logf("Hub can see SITE_A's STREAM_A: %d msgs", si.State.Msgs)

	// Verify Hub can access SITE_B's JetStream API (through SITE_A)
	jsSiteBViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_B.API"))
	require_NoError(t, err)
	si, err = jsSiteBViaHub.StreamInfo("STREAM_B")
	require_NoError(t, err)
	t.Logf("Hub can see SITE_B's STREAM_B (via chain): %d msgs", si.State.Msgs)

	// Create sourced stream on Hub from SITE_A
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_A",
		Sources: []*nats.StreamSource{{
			Name:     "STREAM_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"},
		}},
	})
	require_NoError(t, err)

	// Create sourced stream on Hub from SITE_B (through chain)
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_B",
		Sources: []*nats.StreamSource{{
			Name:     "STREAM_B",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_B.API"},
		}},
	})
	require_NoError(t, err)

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

	t.Log("SUCCESS: Hub sourced from both SITE_A and SITE_B through chain topology!")
}

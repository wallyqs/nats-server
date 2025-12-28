package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamLeafNodeChainWithHubCluster tests a chain topology where
// the hub is a 3-node cluster:
//   Hub Cluster (3 nodes) → SITE_A ← SITE_B
// SITE_A accepts connections from both the hub cluster and SITE_B.
// The hub cluster can source streams from both SITE_A and SITE_B through the chain.
func TestJetStreamLeafNodeChainWithHubCluster(t *testing.T) {
	// SITE_A - middle of chain, accepts connections from both hub and SITE_B
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
		}
	`

	// Start SITE_A first (it accepts connections)
	siteAConfFile := createConfFile(t, []byte(fmt.Sprintf(siteAConf, t.TempDir())))
	sSiteA, oSiteA := RunServerWithConfig(siteAConfFile)
	defer sSiteA.Shutdown()

	// SITE_B - end of chain, connects TO SITE_A
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
			remotes [
				{ url: "nats://site:site@127.0.0.1:%d", account: SITE }
			]
		}
	`

	// Start SITE_B (connects to SITE_A)
	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, t.TempDir(), oSiteA.LeafNode.Port)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Wait for SITE_B to connect to SITE_A
	checkLeafNodeConnectedCount(t, sSiteA, 1)
	checkLeafNodeConnectedCount(t, sSiteB, 1)

	// Hub cluster template - connects TO SITE_A
	hubClusterTempl := `
		listen: 127.0.0.1:-1
		server_name: %s
		jetstream: {max_mem_store: 256MB, max_file_store: 2GB, store_dir: '%s'}

		accounts {
			SITE {
				jetstream: enabled
				users: [{user: site, password: site}]
			}
			$SYS { users: [{user: admin, password: s3cr3t!}] }
		}

		leafnodes {
			remotes [
				{ url: "nats://site:site@127.0.0.1:SITE_A_PORT", account: SITE }
			]
		}

		cluster {
			name: %s
			listen: 127.0.0.1:%d
			routes = [%s]
		}
	`

	// Replace SITE_A_PORT in template
	hubClusterTemplWithPort := fmt.Sprintf(hubClusterTempl[:len(hubClusterTempl)],
		"%s", "%s", "%s", "%d", "%s")
	hubClusterTemplWithPort = fmt.Sprintf(
		`listen: 127.0.0.1:-1
		server_name: %%s
		jetstream: {max_mem_store: 256MB, max_file_store: 2GB, store_dir: '%%s'}

		accounts {
			SITE {
				jetstream: enabled
				users: [{user: site, password: site}]
			}
			$SYS { users: [{user: admin, password: s3cr3t!}] }
		}

		leafnodes {
			remotes [
				{ url: "nats://site:site@127.0.0.1:%d", account: SITE }
			]
		}

		cluster {
			name: %%s
			listen: 127.0.0.1:%%d
			routes = [%%s]
		}
	`, oSiteA.LeafNode.Port)

	// Create hub cluster
	c := createJetStreamClusterWithTemplate(t, hubClusterTemplWithPort, "HUB", 3)
	defer c.shutdown()

	// Wait for hub cluster to connect to SITE_A
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		total := 0
		for _, s := range c.servers {
			total += s.NumLeafNodes()
		}
		// Each hub server connects to SITE_A = 3 connections
		if total != 3 {
			return fmt.Errorf("expected 3 leafnodes from hub cluster, got %d", total)
		}
		return nil
	})

	// SITE_A should have 4 leafnodes: 1 from SITE_B + 3 from hub cluster
	checkLeafNodeConnectedCount(t, sSiteA, 4)

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

	// Connect to hub cluster
	ncHub := natsConnect(t, c.randomServer().ClientURL(), nats.UserInfo("site", "site"))
	defer ncHub.Close()
	jsHub, err := ncHub.JetStream()
	require_NoError(t, err)

	// Verify hub can access SITE_A's JetStream
	jsSiteAViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_A.API"))
	require_NoError(t, err)
	si, err := jsSiteAViaHub.StreamInfo("STREAM_A")
	require_NoError(t, err)
	t.Logf("Hub cluster can see SITE_A's STREAM_A: %d msgs", si.State.Msgs)

	// Verify hub can access SITE_B's JetStream (through chain)
	jsSiteBViaHub, err := ncHub.JetStream(nats.APIPrefix("$JS.SITE_B.API"))
	require_NoError(t, err)
	si, err = jsSiteBViaHub.StreamInfo("STREAM_B")
	require_NoError(t, err)
	t.Logf("Hub cluster can see SITE_B's STREAM_B (via chain): %d msgs", si.State.Msgs)

	// Create replicated sourced stream on hub from SITE_A
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name:     "HUB_FROM_SITE_A",
		Replicas: 3,
		Sources: []*nats.StreamSource{{
			Name:     "STREAM_A",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"},
		}},
	})
	require_NoError(t, err)

	// Create replicated sourced stream on hub from SITE_B
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name:     "HUB_FROM_SITE_B",
		Replicas: 3,
		Sources: []*nats.StreamSource{{
			Name:     "STREAM_B",
			External: &nats.ExternalStream{APIPrefix: "$JS.SITE_B.API"},
		}},
	})
	require_NoError(t, err)

	// Wait for both sourced streams to sync
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_A")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_A info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_A: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_FROM_SITE_B")
		if err != nil {
			return fmt.Errorf("could not get HUB_FROM_SITE_B info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_B: expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})

	t.Log("SUCCESS: Hub cluster sourced from both SITE_A and SITE_B through chain!")
}

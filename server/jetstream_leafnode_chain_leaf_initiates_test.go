package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamLeafNodeChainLeafInitiates tests a chain topology where
// leafnodes initiate connections toward the hub:
//   Hub Cluster (3 nodes) ← SITE_A ← SITE_B
// Hub accepts connections, SITE_A connects to hub and accepts from SITE_B,
// SITE_B connects to SITE_A.
func TestJetStreamLeafNodeChainLeafInitiates(t *testing.T) {
	// Hub cluster template - ACCEPTS connections
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
			listen: 127.0.0.1:-1
		}

		cluster {
			name: %s
			listen: 127.0.0.1:%d
			routes = [%s]
		}
	`

	// Create hub cluster first
	c := createJetStreamClusterWithTemplate(t, hubClusterTempl, "HUB", 3)
	defer c.shutdown()

	// Get leafnode URLs for hub cluster
	var lnURLs []string
	for _, s := range c.servers {
		ln := s.getOpts().LeafNode
		lnURLs = append(lnURLs, fmt.Sprintf("nats://site:site@%s:%d", ln.Host, ln.Port))
	}
	lnURLsStr := `"` + strings.Join(lnURLs, `", "`) + `"`

	// SITE_A - connects TO hub, accepts FROM SITE_B
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
				{ urls: [%s], account: SITE }
			]
		}
	`

	siteAConfFile := createConfFile(t, []byte(fmt.Sprintf(siteAConf, t.TempDir(), lnURLsStr)))
	sSiteA, oSiteA := RunServerWithConfig(siteAConfFile)
	defer sSiteA.Shutdown()

	// Wait for SITE_A to connect to hub
	checkLeafNodeConnectedCount(t, sSiteA, 1)

	// SITE_B - connects TO SITE_A
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

	siteBConfFile := createConfFile(t, []byte(fmt.Sprintf(siteBConf, t.TempDir(), oSiteA.LeafNode.Port)))
	sSiteB, _ := RunServerWithConfig(siteBConfFile)
	defer sSiteB.Shutdown()

	// Wait for SITE_B to connect to SITE_A
	checkLeafNodeConnectedCount(t, sSiteA, 2) // 1 to hub + 1 from SITE_B
	checkLeafNodeConnectedCount(t, sSiteB, 1)

	// Create stream on SITE_A
	ncSiteA := natsConnect(t, sSiteA.ClientURL(), nats.UserInfo("site", "site"))
	defer ncSiteA.Close()
	jsSiteA, _ := ncSiteA.JetStream()
	jsSiteA.AddStream(&nats.StreamConfig{Name: "STREAM_A", Subjects: []string{"site_a.>"}})
	for i := 0; i < 5; i++ {
		jsSiteA.Publish("site_a.data", []byte(fmt.Sprintf("a-msg-%d", i)))
	}

	// Create stream on SITE_B
	ncSiteB := natsConnect(t, sSiteB.ClientURL(), nats.UserInfo("site", "site"))
	defer ncSiteB.Close()
	jsSiteB, _ := ncSiteB.JetStream()
	jsSiteB.AddStream(&nats.StreamConfig{Name: "STREAM_B", Subjects: []string{"site_b.>"}})
	for i := 0; i < 5; i++ {
		jsSiteB.Publish("site_b.data", []byte(fmt.Sprintf("b-msg-%d", i)))
	}

	// Connect to hub cluster
	ncHub := natsConnect(t, c.randomServer().ClientURL(), nats.UserInfo("site", "site"))
	defer ncHub.Close()
	jsHub, _ := ncHub.JetStream()

	// Verify hub can access SITE_A
	jsSiteAViaHub, _ := ncHub.JetStream(nats.APIPrefix("$JS.SITE_A.API"))
	si, err := jsSiteAViaHub.StreamInfo("STREAM_A")
	if err != nil {
		t.Fatalf("Hub cannot see SITE_A: %v", err)
	}
	t.Logf("Hub can see SITE_A's STREAM_A: %d msgs", si.State.Msgs)

	// Verify hub can access SITE_B through chain
	jsSiteBViaHub, _ := ncHub.JetStream(nats.APIPrefix("$JS.SITE_B.API"))
	si, err = jsSiteBViaHub.StreamInfo("STREAM_B")
	if err != nil {
		t.Fatalf("Hub cannot see SITE_B through chain: %v", err)
	}
	t.Logf("Hub can see SITE_B's STREAM_B (via chain): %d msgs", si.State.Msgs)

	// Create sourced streams on hub
	jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_A", Replicas: 3,
		Sources: []*nats.StreamSource{{Name: "STREAM_A", External: &nats.ExternalStream{APIPrefix: "$JS.SITE_A.API"}}},
	})
	jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_FROM_SITE_B", Replicas: 3,
		Sources: []*nats.StreamSource{{Name: "STREAM_B", External: &nats.ExternalStream{APIPrefix: "$JS.SITE_B.API"}}},
	})

	// Verify sync
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, _ := jsHub.StreamInfo("HUB_FROM_SITE_A")
		if si == nil || si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_A not synced")
		}
		si, _ = jsHub.StreamInfo("HUB_FROM_SITE_B")
		if si == nil || si.State.Msgs != 5 {
			return fmt.Errorf("HUB_FROM_SITE_B not synced")
		}
		return nil
	})

	t.Log("SUCCESS: Hub cluster sourced from chain where leafnodes initiate connections!")
}

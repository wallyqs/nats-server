package test

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"golang.org/x/crypto/ocsp"
)

func TestOCSPAlwaysMustStapleAndShutdown(t *testing.T) {
	// Certs that have must staple will auto shutdown the server.
	const (
		caCert     = "configs/certs/ocsp/ca-cert.pem"
		caKey      = "configs/certs/ocsp/ca-key.pem"
		serverCert = "configs/certs/ocsp/server-cert.pem"
		serverKey  = "configs/certs/ocsp/server-key.pem"
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ocspr := newOCSPResponder(t, caCert, caKey)
	defer ocspr.Shutdown(ctx)
	addr := fmt.Sprintf("http://%s", ocspr.Addr)
	setOCSPStatus(t, addr, serverCert, ocsp.Good)

	opts := DefaultTestOptions
	opts.Port = -1
	opts.TLSCert = serverCert
	opts.TLSKey = serverKey
	opts.TLSCaCert = caCert
	opts.TLSTimeout = 5

	tlsConf, err := server.GenTLSConfig(&server.TLSConfigOpts{
		CertFile: opts.TLSCert,
		KeyFile:  opts.TLSKey,
		CaFile:   opts.TLSCaCert,
		Timeout:  opts.TLSTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	opts.TLSConfig = tlsConf

	// FIXME: StateDir cannot change during reloads, need to restart the server
	// so we assume that it is static right now.
	opts.StateDir = createDir(t, "ocsp-status")
	opts.OCSPConfig = &server.OCSPConfig{
		Mode:         server.OCSPModeAlways,
		OverrideURLs: []string{addr},
	}

	srv := RunServer(&opts)
	defer srv.Shutdown()
	defer removeDir(t, opts.StateDir)

	nc, err := nats.Connect(fmt.Sprintf("tls://localhost:%d", opts.Port),
		nats.RootCAs(caCert),
		nats.ErrorHandler(noOpErrHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := nc.SubscribeSync("foo")
	if err != nil {
		t.Fatal(err)
	}
	nc.Publish("foo", []byte("hello world"))
	nc.Flush()

	_, err = sub.NextMsg(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()

	// The server will shutdown because the server becomes revoked
	// and the policy is to always must-staple.  The OCSP Responder
	// instructs the NATS Server to fetch OCSP Staples every 2 seconds.
	time.Sleep(2 * time.Second)
	setOCSPStatus(t, addr, serverCert, ocsp.Revoked)
	time.Sleep(2 * time.Second)

	// Should be connection refused since server will abort now.
	_, err = nats.Connect(fmt.Sprintf("tls://localhost:%d", opts.Port),
		nats.RootCAs(caCert),
		nats.ErrorHandler(noOpErrHandler),
	)
	if err != nats.ErrNoServers {
		t.Errorf("Expected connection refused")
	}
}

func TestOCSPMustStapleShutdown(t *testing.T) {
	// Certs that have must staple will get staples but not shutdown the server.
	const (
		caCert     = "configs/certs/ocsp/ca-cert.pem"
		caKey      = "configs/certs/ocsp/ca-key.pem"
		serverCert = "configs/certs/ocsp/server-cert.pem"
		serverKey  = "configs/certs/ocsp/server-key.pem"
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ocspr := newOCSPResponder(t, caCert, caKey)
	defer ocspr.Shutdown(ctx)
	addr := fmt.Sprintf("http://%s", ocspr.Addr)
	setOCSPStatus(t, addr, serverCert, ocsp.Good)

	opts := DefaultTestOptions
	opts.Port = -1
	opts.TLSCert = serverCert
	opts.TLSKey = serverKey
	opts.TLSCaCert = caCert
	opts.TLSTimeout = 5

	tlsConf, err := server.GenTLSConfig(&server.TLSConfigOpts{
		CertFile: opts.TLSCert,
		KeyFile:  opts.TLSKey,
		CaFile:   opts.TLSCaCert,
		Timeout:  opts.TLSTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	opts.TLSConfig = tlsConf

	// FIXME: StateDir cannot change during reloads, need to restart the server
	// so we assume that it is static right now.
	opts.StateDir = createDir(t, "ocsp-status")
	opts.OCSPConfig = &server.OCSPConfig{
		Mode:         server.OCSPModeMust,
		OverrideURLs: []string{addr},
	}

	srv := RunServer(&opts)
	defer srv.Shutdown()
	defer removeDir(t, opts.StateDir)

	nc, err := nats.Connect(fmt.Sprintf("tls://localhost:%d", opts.Port),
		nats.RootCAs(caCert),
		nats.ErrorHandler(noOpErrHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := nc.SubscribeSync("foo")
	if err != nil {
		t.Fatal(err)
	}
	nc.Publish("foo", []byte("hello world"))
	nc.Flush()

	_, err = sub.NextMsg(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()

	// The server will shutdown because the server becomes revoked
	// and the policy is to always must-staple.  The OCSP Responder
	// instructs the NATS Server to fetch OCSP Staples every 2 seconds.
	time.Sleep(2 * time.Second)
	setOCSPStatus(t, addr, serverCert, ocsp.Revoked)
	time.Sleep(2 * time.Second)

	// Should be connection refused since server will abort now.
	_, err = nats.Connect(fmt.Sprintf("tls://localhost:%d", opts.Port),
		nats.RootCAs(caCert),
		nats.ErrorHandler(noOpErrHandler),
	)
	if err != nats.ErrNoServers {
		t.Errorf("Expected connection refused")
	}
}

func TestOCSPAutoPolicyModeDoesNotShutdownOnRevoke(t *testing.T) {
	// Certs that have must staple will get staples but not shutdown the server.
	const (
		caCert     = "configs/certs/ocsp/ca-cert.pem"
		caKey      = "configs/certs/ocsp/ca-key.pem"
		serverCert = "configs/certs/ocsp/server-cert.pem"
		serverKey  = "configs/certs/ocsp/server-key.pem"
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ocspr := newOCSPResponder(t, caCert, caKey)
	defer ocspr.Shutdown(ctx)
	addr := fmt.Sprintf("http://%s", ocspr.Addr)
	setOCSPStatus(t, addr, serverCert, ocsp.Good)

	opts := DefaultTestOptions
	opts.Port = -1
	opts.TLSCert = serverCert
	opts.TLSKey = serverKey
	opts.TLSCaCert = caCert
	opts.TLSTimeout = 5

	tlsConf, err := server.GenTLSConfig(&server.TLSConfigOpts{
		CertFile: opts.TLSCert,
		KeyFile:  opts.TLSKey,
		CaFile:   opts.TLSCaCert,
		Timeout:  opts.TLSTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	opts.TLSConfig = tlsConf

	// FIXME: StateDir cannot change during reloads, need to restart the server
	// so we assume that it is static right now.
	opts.StateDir = createDir(t, "ocsp-status")
	opts.OCSPConfig = &server.OCSPConfig{
		Mode:         server.OCSPModeAuto,
		OverrideURLs: []string{addr},
	}

	srv := RunServer(&opts)
	defer srv.Shutdown()
	defer removeDir(t, opts.StateDir)

	nc, err := nats.Connect(fmt.Sprintf("tls://localhost:%d", opts.Port),
		nats.RootCAs(caCert),
		nats.ErrorHandler(noOpErrHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := nc.SubscribeSync("foo")
	if err != nil {
		t.Fatal(err)
	}
	nc.Publish("foo", []byte("hello world"))
	nc.Flush()

	_, err = sub.NextMsg(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()

	// The server will shutdown because the server becomes revoked
	// and the policy is to always must-staple.  The OCSP Responder
	// instructs the NATS Server to fetch OCSP Staples every 2 seconds.
	time.Sleep(2 * time.Second)
	setOCSPStatus(t, addr, serverCert, ocsp.Revoked)
	time.Sleep(2 * time.Second)

	// Should not be connection refused since server will continue running.
	_, err = nats.Connect(fmt.Sprintf("tls://localhost:%d", opts.Port),
		nats.RootCAs(caCert),
		nats.ErrorHandler(noOpErrHandler),
	)
	if err != nil {
		t.Errorf("Unexpected error: %s", err)
	}
}

func TestOCSPClient(t *testing.T) {
	const (
		caCert     = "configs/certs/ocsp/ca-cert.pem"
		caKey      = "configs/certs/ocsp/ca-key.pem"
		serverCert = "configs/certs/ocsp/server-cert.pem"
		serverKey  = "configs/certs/ocsp/server-key.pem"
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ocspr := newOCSPResponder(t, caCert, caKey)
	ocspURL := fmt.Sprintf("http://%s", ocspr.Addr)
	defer ocspr.Shutdown(ctx)
	dir := createDir(t, "ocsp")
	defer removeDir(t, dir)

	for _, test := range []struct {
		name      string
		config    string
		certs     nats.Option
		err       error
		rerr      error
		configure func()
	}{
		{
			"OCSP Stapling makes server fail to boot if status is unknown",
			`
				port: -1

				# Enable OCSP stapling with policy to honor must staple if present.
				ocsp: true
				state_dir: "%s"

				tls {
					cert_file: "configs/certs/ocsp/server-cert.pem"
					key_file: "configs/certs/ocsp/server-key.pem"
					ca_file: "configs/certs/ocsp/ca-cert.pem"
					timeout: 5
				}
			`,
			nats.ClientCert("./configs/certs/ocsp/client-cert.pem", "./configs/certs/ocsp/client-key.pem"),
			nil,
			nil,
			func() {},
		},
		{
			"OCSP Stapling ignored by default if server without must staple status",
			`
				port: -1

				# Enable OCSP stapling with policy to honor must staple if present.
				ocsp: true
				state_dir: "%s"

				tls {
					cert_file: "configs/certs/ocsp/server-cert.pem"
					key_file: "configs/certs/ocsp/server-key.pem"
					ca_file: "configs/certs/ocsp/ca-cert.pem"
					timeout: 5
				}
			`,
			nats.ClientCert("./configs/certs/ocsp/client-cert.pem", "./configs/certs/ocsp/client-key.pem"),
			nil,
			nil,
			func() { setOCSPStatus(t, ocspURL, serverCert, ocsp.Good) },
		},
		{
			"OCSP Stapling honored by default if server has must staple status",
			`
				port: -1

				ocsp: true
				state_dir: "%s"

				tls {
					cert_file: "configs/certs/ocsp/server-status-request-url-cert.pem"
					key_file: "configs/certs/ocsp/server-status-request-url-key.pem"
					ca_file: "configs/certs/ocsp/ca-cert.pem"
					timeout: 5
				}
			`,
			nats.ClientCert("./configs/certs/ocsp/client-cert.pem", "./configs/certs/ocsp/client-key.pem"),
			nil,
			nil,
			func() {
				setOCSPStatus(t, ocspURL, "configs/certs/ocsp/server-status-request-url-cert.pem", ocsp.Good)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.configure()
			content := fmt.Sprintf(test.config, dir)
			conf := createConfFile(t, []byte(content))
			defer removeFile(t, conf)
			s, opts := RunServerWithConfig(conf)
			defer s.Shutdown()

			nc, err := nats.Connect(fmt.Sprintf("tls://localhost:%d", opts.Port),
				test.certs,
				nats.RootCAs(caCert),
				nats.ErrorHandler(noOpErrHandler),
			)
			if test.err == nil && err != nil {
				t.Errorf("Expected to connect, got %v", err)
			} else if test.err != nil && err == nil {
				t.Errorf("Expected error on connect")
			} else if test.err != nil && err != nil {
				// Error on connect was expected
				if test.err.Error() != err.Error() {
					t.Errorf("Expected error %s, got: %s", test.err, err)
				}
				return
			}
			defer nc.Close()

			nc.Subscribe("ping", func(m *nats.Msg) {
				m.Respond([]byte("pong"))
			})
			nc.Flush()

			_, err = nc.Request("ping", []byte("ping"), 250*time.Millisecond)
			if test.rerr != nil && err == nil {
				t.Errorf("Expected error getting response")
			} else if test.rerr == nil && err != nil {
				t.Errorf("Expected response")
			}
		})
	}
}

func newOCSPResponder(t *testing.T, issuerCertPEM, issuerKeyPEM string) *http.Server {
	t.Helper()
	var mu sync.Mutex
	status := make(map[string]int)

	issuerCert := parseCertPEM(t, issuerCertPEM)
	issuerKey := parseKeyPEM(t, issuerKeyPEM)

	mux := http.NewServeMux()
	// The "/statuses/" endpoint is for directly setting a key-value pair in
	// the CA's status database.
	mux.HandleFunc("/statuses/", func(rw http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		key := r.URL.Path[len("/statuses/"):]
		switch r.Method {
		case "GET":
			mu.Lock()
			n, ok := status[key]
			if !ok {
				n = ocsp.Unknown
			}
			mu.Unlock()

			fmt.Fprintf(rw, "%s %d", key, n)
		case "POST":
			data, err := ioutil.ReadAll(r.Body)
			if err != nil {
				http.Error(rw, err.Error(), http.StatusBadRequest)
				return
			}

			n, err := strconv.Atoi(string(data))
			if err != nil {
				http.Error(rw, err.Error(), http.StatusBadRequest)
				return
			}

			mu.Lock()
			status[key] = n
			mu.Unlock()

			fmt.Fprintf(rw, "%s %d", key, n)
		default:
			http.Error(rw, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
	})
	// The "/" endpoint is for normal OCSP requests. This actually parses an
	// OCSP status request and signs a response with a CA. Lightly based off:
	// https://www.ietf.org/rfc/rfc2560.txt
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(rw, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		reqData, err := base64.StdEncoding.DecodeString(r.URL.Path[1:])
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		ocspReq, err := ocsp.ParseRequest(reqData)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		n, ok := status[ocspReq.SerialNumber.String()]
		if !ok {
			n = ocsp.Unknown
		}
		mu.Unlock()

		tmpl := ocsp.Response{
			Status:       n,
			SerialNumber: ocspReq.SerialNumber,
			ThisUpdate:   time.Now(),
			NextUpdate:   time.Now().Add(4 * time.Second),
		}
		respData, err := ocsp.CreateResponse(issuerCert, issuerCert, tmpl, issuerKey)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}

		rw.Header().Set("Content-Type", "application/ocsp-response")
		rw.Header().Set("Content-Length", fmt.Sprint(len(respData)))

		fmt.Fprint(rw, string(respData))
	})

	srv := &http.Server{
		Addr:    "127.0.0.1:8888",
		Handler: mux,
	}
	go srv.ListenAndServe()
	return srv
}

func setOCSPStatus(t *testing.T, ocspURL, certPEM string, status int) {
	t.Helper()

	cert := parseCertPEM(t, certPEM)

	hc := &http.Client{Timeout: 3 * time.Second}
	resp, err := hc.Post(
		fmt.Sprintf("%s/statuses/%s", ocspURL, cert.SerialNumber),
		"",
		strings.NewReader(fmt.Sprint(status)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read OCSP HTTP response body: %s", err)
	}

	if got, want := resp.Status, "200 OK"; got != want {
		t.Error(strings.TrimSpace(string(data)))
		t.Fatalf("unexpected OCSP HTTP set status, got %q, want %q", got, want)
	}
}

func parseCertPEM(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block := parsePEM(t, certPEM)

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse cert %s: %s", certPEM, err)
	}
	return cert
}

func parseKeyPEM(t *testing.T, keyPEM string) *rsa.PrivateKey {
	t.Helper()
	block := parsePEM(t, keyPEM)

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse ikey %s: %s", keyPEM, err)
	}
	return key
}

func parsePEM(t *testing.T, pemPath string) *pem.Block {
	t.Helper()
	data, err := ioutil.ReadFile(pemPath)
	if err != nil {
		t.Fatal(err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("failed to decode PEM %s", pemPath)
	}
	return block
}

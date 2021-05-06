package server

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ocsp"
)

type OCSPMode uint8

const (
	// OCSPModeAuto staples a status, only if "status_request" is set in cert.
	OCSPModeAuto OCSPMode = iota

	// OCSPModeAlways enforces OCSP stapling for certs and shuts down the server in
	// case a server is revoked or cannot get OCSP staples.
	OCSPModeAlways

	// OCSPModeNever disables OCSP stapling even if cert has Must-Staple flag.
	OCSPModeNever

	// OCSPModeMust honors the Must-Staple flag from a certificate but also causing shutdown
	// in case the certificate has been revoked.
	OCSPModeMust
)

// OCSPMonitor monitors the state of a staple per certificate.
type OCSPMonitor struct {
	mu   sync.Mutex
	raw  []byte
	srv  *Server
	resp *ocsp.Response
	hc   *http.Client

	// FIXME: Make reloadable aware.
	Leaf   *x509.Certificate
	Issuer *x509.Certificate
}

func (oc *OCSPMonitor) getNextRun() time.Duration {
	oc.mu.Lock()
	nextUpdate := oc.resp.NextUpdate
	oc.mu.Unlock()

	now := time.Now()
	if nextUpdate.IsZero() {
		// If response is missing NextUpdate, we check the day after.
		// Technically, if NextUpdate is missing, we can try whenever.
		// https://tools.ietf.org/html/rfc6960#section-4.2.2.1
		return 24 * time.Hour
	}

	return nextUpdate.Sub(now) / 2
}

func (oc *OCSPMonitor) getStatus() ([]byte, *ocsp.Response, error) {
	raw, resp := oc.getCacheStatus()
	if len(raw) > 0 && resp != nil {
		return raw, resp, nil
	}

	var err error
	raw, resp, err = oc.getLocalStatus()
	if err == nil {
		return raw, resp, nil
	}

	return oc.getRemoteStatus()
}

func (oc *OCSPMonitor) getCacheStatus() ([]byte, *ocsp.Response) {
	oc.mu.Lock()
	defer oc.mu.Unlock()
	return oc.raw, oc.resp
}

func (oc *OCSPMonitor) getLocalStatus() ([]byte, *ocsp.Response, error) {
	key := fmt.Sprintf("%x", sha256.Sum256(oc.Leaf.Raw))
	opts := oc.srv.getOpts()
	stateDir := opts.StateDir
	oc.mu.Lock()
	raw, err := ioutil.ReadFile(filepath.Join(stateDir, key))
	oc.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}

	resp, err := ocsp.ParseResponse(raw, oc.Issuer)
	if err != nil {
		return nil, nil, err
	}
	if err := validOCSPResponse(resp); err != nil {
		return nil, nil, err
	}

	oc.mu.Lock()
	oc.raw = raw
	oc.resp = resp
	oc.mu.Unlock()

	return raw, resp, nil
}

func (oc *OCSPMonitor) getRemoteStatus() ([]byte, *ocsp.Response, error) {
	opts := oc.srv.getOpts()
	stateDir := opts.StateDir
	overrideURLs := opts.OCSPConfig.OverrideURLs
	getRequestBytes := func(u string, hc *http.Client) ([]byte, error) {
		resp, err := hc.Get(u)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("non-ok http status: %d", resp.StatusCode)
		}

		return ioutil.ReadAll(resp.Body)
	}

	// Request documentation:
	// https://tools.ietf.org/html/rfc6960#appendix-A.1

	reqDER, err := ocsp.CreateRequest(oc.Leaf, oc.Issuer, nil)
	if err != nil {
		return nil, nil, err
	}

	reqEnc := base64.StdEncoding.EncodeToString(reqDER)

	responders := oc.Leaf.OCSPServer
	if len(overrideURLs) > 0 {
		responders = overrideURLs
	}

	oc.mu.Lock()
	hc := oc.hc
	oc.mu.Unlock()
	var raw []byte
	for _, u := range responders {
		u = strings.TrimSuffix(u, "/")
		raw, err = getRequestBytes(fmt.Sprintf("%s/%s", u, reqEnc), hc)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("exhausted ocsp servers: %w", err)
	}

	resp, err := ocsp.ParseResponse(raw, oc.Issuer)
	if err != nil {
		return nil, nil, err
	}
	if err := validOCSPResponse(resp); err != nil {
		return nil, nil, err
	}

	key := fmt.Sprintf("%x", sha256.Sum256(oc.Leaf.Raw))
	if err := oc.writeOCSPStatus(stateDir, key, raw); err != nil {
		return nil, nil, fmt.Errorf("failed to write ocsp status: %w", err)
	}

	oc.mu.Lock()
	oc.raw = raw
	oc.resp = resp
	oc.mu.Unlock()

	return raw, resp, nil
}

func (oc *OCSPMonitor) run() {
	s := oc.srv
	defer s.grWG.Done()
	config := s.getOpts().OCSPConfig
	shouldShutdown := config != nil && (config.Mode == OCSPModeMust || config.Mode == OCSPModeAlways)

	var nextRun time.Duration
	_, resp, err := oc.getStatus()
	if err == nil && resp.Status == ocsp.Good {
		nextRun = oc.getNextRun()
		t := resp.NextUpdate.Format(time.RFC3339Nano)
		s.Noticef(
			"Found existing OCSP certificate status: good, next update %s, checking again in %s",
			t, nextRun,
		)
	} else if err == nil && shouldShutdown {
		// If resp.Status is ocsp.Revoked, ocsp.Unknown, or any other value.
		s.Errorf("Found existing OCSP certificate status: %s", ocspStatusString(resp.Status))
		s.Shutdown()
		return
	}

	for {
		time.Sleep(nextRun)

		s.mu.Lock()
		running := s.running
		shuttingDown := s.shutdown
		s.mu.Unlock()

		if !running && !shuttingDown {
			// Server is probably still coming up. Wait a little more.
			nextRun = 1 * time.Second
			continue
		}
		if !running && shuttingDown {
			// Server is shutting down. Exit goroutine.
			s.Noticef("Shutting down OCSP service...")
			return
		}

		_, resp, err := oc.getRemoteStatus()
		if err != nil {
			nextRun = oc.getNextRun()
			s.Errorf("Bad OCSP status update: %s, trying again in %s", err, nextRun)
			continue
		}

		switch n := resp.Status; n {
		case ocsp.Good:
			nextRun = oc.getNextRun()
			t := resp.NextUpdate.Format(time.RFC3339Nano)
			s.Noticef(
				"Received OCSP certificate status: good, next update %s, checking again in %s",
				t, nextRun,
			)
			continue
		default:
			s.Errorf("Received OCSP certificate status: %s, shutting down", ocspStatusString(n))
			s.Shutdown()
			return
		}
	}
}

// NewOCSPMonitor takes a TLS configuration then wraps it with the callbacks set for OCSP verification
// along with a monitor that will periodically fetch OCSP staples.
func (srv *Server) NewOCSPMonitor(tc *tls.Config) (*tls.Config, *OCSPMonitor, error) {
	opts := srv.getOpts()
	oc := opts.OCSPConfig
	if oc.Mode == OCSPModeNever {
		return tc, nil, nil
	}
	cert, err := tls.LoadX509KeyPair(opts.TLSCert, opts.TLSKey)
	if err != nil {
		return nil, nil, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	if oc.Mode == OCSPModeAuto && !hasOCSPStatusRequest(cert.Leaf) {
		// "status_request" flag not set. No need to do anything.
		return tc, nil, nil
	}

	// TODO: Add OCSP 'responder_cert' option in case CA cert not available.
	issuer, err := getOCSPIssuer(opts.TLSCaCert, cert.Certificate)
	if err != nil {
		return nil, nil, err
	}

	// Callbacks below will be in charge of returning the certificate instead,
	// so this has to be nil.
	tc.Certificates = nil

	mon := &OCSPMonitor{
		srv: srv,
		hc:  &http.Client{Timeout: 30 * time.Second},
		// FIXME: Make reloadable aware, otherwise a change in certificates
		//  could make the staples stale.
		Leaf:   cert.Leaf,
		Issuer: issuer,
	}

	// Get the certificate status from the memory, disk, then remote OCSP responder.
	_, resp, err := mon.getStatus()
	if err != nil {
		return nil, nil, fmt.Errorf("bad OCSP status update: %s", err)
	}
	if err == nil && resp.Status != ocsp.Good && (oc.Mode == OCSPModeAlways || oc.Mode == OCSPModeMust) {
		return nil, nil, fmt.Errorf("found existing OCSP certificate status: %s", ocspStatusString(resp.Status))
	}

	// GetCertificate returns a certificate that's presented to a
	// client.
	tc.GetCertificate = func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
		raw, _, err := mon.getStatus()
		if err != nil {
			return nil, err
		}

		return &tls.Certificate{
			OCSPStaple:                   raw,
			Certificate:                  cert.Certificate,
			PrivateKey:                   cert.PrivateKey,
			SupportedSignatureAlgorithms: cert.SupportedSignatureAlgorithms,
			SignedCertificateTimestamps:  cert.SignedCertificateTimestamps,
			Leaf:                         cert.Leaf,
		}, nil
	}

	// GetClientCertificate returns a certificate that's presented to a
	// server.
	tc.GetClientCertificate = func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return &cert, nil
	}
	return tc, mon, nil
}

func hasOCSPStatusRequest(cert *x509.Certificate) bool {
	tlsFeatures := asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 24}
	const statusRequestExt = byte(5)
	const tlsFeaturesLen = 5

	// Example Value: [48 3 2 1 5]
	// Documentation:
	// https://tools.ietf.org/html/rfc6066

	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(tlsFeatures) {
			continue
		}
		if len(ext.Value) != tlsFeaturesLen {
			continue
		}
		return ext.Value[tlsFeaturesLen-1] == statusRequestExt
	}

	return false
}

func parseCertPEM(name string) (*x509.Certificate, error) {
	data, err := ioutil.ReadFile(name)
	if err != nil {
		return nil, err
	}

	// Ignoring left over byte slice.
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM cert %s", name)
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("unexpected PEM certificate type: %s", block.Type)
	}

	return x509.ParseCertificate(block.Bytes)
}

// getOCSPIssuer returns a CA cert from the given path. If the path is empty,
// then this checks a given cert chain. If both are empty, then it returns an
// error.
func getOCSPIssuer(issuerCert string, chain [][]byte) (*x509.Certificate, error) {
	var issuer *x509.Certificate
	var err error
	switch {
	case len(chain) == 1 && issuerCert == "":
		err = fmt.Errorf("require ocsp ca in chain or configuration")
	case issuerCert != "":
		issuer, err = parseCertPEM(issuerCert)
	case len(chain) > 1 && issuerCert == "":
		issuer, err = x509.ParseCertificate(chain[1])
	default:
		err = fmt.Errorf("invalid ocsp ca configuration")
	}
	if err != nil {
		return nil, err
	} else if !issuer.IsCA {
		return nil, fmt.Errorf("%s invalid ca basic constraints: is not ca", issuerCert)
	}

	return issuer, nil
}

// writeOCSPStatus writes an OCSP status to a temporary file then moves it to a
// new path, in an attempt to avoid corrupting existing data.
func (oc *OCSPMonitor) writeOCSPStatus(dir, file string, data []byte) error {
	tmp, err := ioutil.TempFile(dir, "tmp-cert-status")
	if err != nil {
		return err
	}

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	oc.mu.Lock()
	err = os.Rename(tmp.Name(), filepath.Join(dir, file))
	oc.mu.Unlock()
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}

	return nil
}

func ocspStatusString(n int) string {
	switch n {
	case ocsp.Good:
		return "good"
	case ocsp.Revoked:
		return "revoked"
	default:
		return "unknown"
	}
}

func validOCSPResponse(r *ocsp.Response) error {
	// Time validation not handled by ParseResponse.
	// https://tools.ietf.org/html/rfc6960#section-4.2.2.1
	if !r.NextUpdate.IsZero() && r.NextUpdate.Before(time.Now()) {
		t := r.NextUpdate.Format(time.RFC3339Nano)
		return fmt.Errorf("invalid ocsp NextUpdate, is past time: %s", t)
	}
	if r.ThisUpdate.After(time.Now()) {
		t := r.ThisUpdate.Format(time.RFC3339Nano)
		return fmt.Errorf("invalid ocsp ThisUpdate, is future time: %s", t)
	}

	return nil
}

package tls

import (
	"crypto/ecdsa"
	"crypto/sha256"
	stdtls "crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Default file names inside the TLS dir. Using fixed names so a user
// can find them with `ls /etc/knot/tls` and a recovery procedure
// over SSH/serial doesn't require a config lookup.
const (
	rootCertFile = "root.crt"
	rootKeyFile  = "root.key"
	leafCertFile = "leaf.crt"
	leafKeyFile  = "leaf.key"
)

// Root is the in-memory representation of the device's root CA.
type Root struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	// CertPEM is the on-disk encoding, kept around for the
	// /setup-ca.crt download endpoint without re-encoding on every
	// request.
	CertPEM []byte
}

// Materials bundles the full PKI state of the device: the root and
// the currently-active leaf. Returned by Open / Init; held by the
// HTTP server through a small accessor so cert rotation is visible
// without restarting the listener.
type Materials struct {
	mu       sync.RWMutex
	root     *Root
	leafKP   *stdtls.Certificate
	leafCert *x509.Certificate
	leafSubj LeafSubject

	dir string
}

// Options configures Open / Init.
type Options struct {
	// Dir is the directory containing root.crt / root.key /
	// leaf.crt / leaf.key. Created with 0700 if missing.
	Dir string
	// Subject is what to use when (re-)issuing the leaf. If the
	// existing on-disk leaf already covers this subject, it is
	// kept; otherwise a fresh leaf is issued.
	Subject LeafSubject
}

// Open loads existing PKI material from Opts.Dir and (re-)issues a
// leaf if needed. If no root exists, generates one. Idempotent —
// safe to call on every boot.
func Open(opts Options) (*Materials, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("tls: Dir is required")
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", opts.Dir, err)
	}

	m := &Materials{dir: opts.Dir}

	// Load or generate root.
	root, err := loadRoot(opts.Dir)
	if errors.Is(err, os.ErrNotExist) {
		root, err = createRoot(opts.Dir)
	}
	if err != nil {
		return nil, fmt.Errorf("root: %w", err)
	}
	m.root = root

	// Load existing leaf, if any. Re-issue when missing, expiring
	// soon, or its SANs no longer cover the requested subject.
	needIssue := false
	leafCert, leafKP, err := loadLeaf(opts.Dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		needIssue = true
	case err != nil:
		return nil, fmt.Errorf("leaf: %w", err)
	default:
		if !covers(leafCert, opts.Subject) {
			needIssue = true
		}
		// 30-day proactive renewal window.
		if time.Until(leafCert.NotAfter) < 30*24*time.Hour {
			needIssue = true
		}
		m.leafCert = leafCert
		m.leafKP = leafKP
	}

	if needIssue {
		if err := m.issueAndPersistLeaf(opts.Subject); err != nil {
			return nil, fmt.Errorf("issue leaf: %w", err)
		}
	} else {
		m.leafSubj = opts.Subject
	}

	return m, nil
}

// Regenerate forces a fresh leaf even if the current one is still
// valid. Used by POST /api/tls/regenerate. The root is untouched —
// a user who installed the root keeps trusting the new leaf.
func (m *Materials) Regenerate(subj LeafSubject) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.issueAndPersistLeafLocked(subj)
}

func (m *Materials) issueAndPersistLeaf(subj LeafSubject) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.issueAndPersistLeafLocked(subj)
}

func (m *Materials) issueAndPersistLeafLocked(subj LeafSubject) error {
	certPEM, keyPEM, err := issueLeaf(m.root, subj)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(m.dir, leafCertFile), certPEM, 0o600); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(m.dir, leafKeyFile), keyPEM, 0o600); err != nil {
		return err
	}
	kp, err := stdtls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("loadback: %w", err)
	}
	parsed, err := x509.ParseCertificate(kp.Certificate[0])
	if err != nil {
		return fmt.Errorf("reparse: %w", err)
	}
	m.leafKP = &kp
	m.leafCert = parsed
	m.leafSubj = subj
	return nil
}

// TLSConfig returns a *tls.Config wired to the current leaf via
// GetCertificate, so a future cert rotation is picked up by the
// HTTP server without listener restart.
func (m *Materials) TLSConfig() *stdtls.Config {
	return &stdtls.Config{
		MinVersion: stdtls.VersionTLS12,
		GetCertificate: func(_ *stdtls.ClientHelloInfo) (*stdtls.Certificate, error) {
			m.mu.RLock()
			defer m.mu.RUnlock()
			if m.leafKP == nil {
				return nil, fmt.Errorf("tls: no leaf cert loaded")
			}
			return m.leafKP, nil
		},
	}
}

// RootCertPEM returns the PEM bytes of the root cert, suitable for
// serving from /setup-ca.crt.
func (m *Materials) RootCertPEM() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root.CertPEM
}

// Info is a JSON-friendly snapshot of the current PKI state.
// Returned by GET /api/tls/info.
type Info struct {
	RootFingerprint string    `json:"root_fingerprint"`
	RootNotAfter    time.Time `json:"root_not_after"`
	LeafFingerprint string    `json:"leaf_fingerprint"`
	LeafNotAfter    time.Time `json:"leaf_not_after"`
	LeafDNSNames    []string  `json:"leaf_dns_names"`
	LeafIPs         []string  `json:"leaf_ips"`
}

// Snapshot reads the current materials in a thread-safe fashion.
func (m *Materials) Snapshot() Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := Info{
		RootFingerprint: fpFromCert(m.root.Cert),
		RootNotAfter:    m.root.Cert.NotAfter,
	}
	if m.leafCert != nil {
		out.LeafFingerprint = fpFromCert(m.leafCert)
		out.LeafNotAfter = m.leafCert.NotAfter
		out.LeafDNSNames = append(out.LeafDNSNames, m.leafCert.DNSNames...)
		ips := make([]string, 0, len(m.leafCert.IPAddresses))
		for _, ip := range m.leafCert.IPAddresses {
			ips = append(ips, ip.String())
		}
		out.LeafIPs = ips
	}
	return out
}

// covers reports whether the given leaf already vouches for every
// DNS name and IP in subj. Conservative: treats any miss as a need
// to re-issue.
func covers(leaf *x509.Certificate, subj LeafSubject) bool {
	have := map[string]struct{}{}
	for _, n := range leaf.DNSNames {
		have[strings.ToLower(n)] = struct{}{}
	}
	for _, n := range subj.DNSNames {
		if _, ok := have[strings.ToLower(n)]; !ok {
			return false
		}
	}
	haveIP := map[string]struct{}{}
	for _, ip := range leaf.IPAddresses {
		haveIP[ip.String()] = struct{}{}
	}
	for _, ip := range subj.IPAddresses {
		if _, ok := haveIP[ip.String()]; !ok {
			return false
		}
	}
	return true
}

func loadRoot(dir string) (*Root, error) {
	certBytes, err := os.ReadFile(filepath.Join(dir, rootCertFile))
	if err != nil {
		return nil, err
	}
	keyBytes, err := os.ReadFile(filepath.Join(dir, rootKeyFile))
	if err != nil {
		return nil, err
	}
	cert, err := parseCertPEM(certBytes)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rootCertFile, err)
	}
	key, err := parseECKeyPEM(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rootKeyFile, err)
	}
	return &Root{Cert: cert, Key: key, CertPEM: certBytes}, nil
}

func createRoot(dir string) (*Root, error) {
	certPEM, keyPEM, err := generateRoot()
	if err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, rootCertFile), certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, rootKeyFile), keyPEM, 0o600); err != nil {
		return nil, err
	}
	cert, err := parseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	key, err := parseECKeyPEM(keyPEM)
	if err != nil {
		return nil, err
	}
	return &Root{Cert: cert, Key: key, CertPEM: certPEM}, nil
}

func loadLeaf(dir string) (*x509.Certificate, *stdtls.Certificate, error) {
	certPath := filepath.Join(dir, leafCertFile)
	keyPath := filepath.Join(dir, leafKeyFile)
	if _, err := os.Stat(certPath); err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(keyPath); err != nil {
		return nil, nil, err
	}
	kp, err := stdtls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := x509.ParseCertificate(kp.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	return parsed, &kp, nil
}

func parseCertPEM(b []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func parseECKeyPEM(b []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	return x509.ParseECPrivateKey(blk.Bytes)
}

// writeFile is an atomic write via temp+rename. Critical for the
// key files: a half-written key would brick the next boot.
func writeFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tls-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func fpFromCert(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	hex := hex.EncodeToString(sum[:])
	// Group as colon-separated hex pairs, the format browsers and
	// `openssl x509 -fingerprint` produce.
	var b strings.Builder
	b.Grow(len(hex) + len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hex[i : i+2])
	}
	return b.String()
}

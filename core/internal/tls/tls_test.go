package tls

import (
	stdtls "crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenGeneratesRootAndLeafFirstTime(t *testing.T) {
	dir := t.TempDir()
	subj := BuildLeafSubject("knot", nil, []net.IP{net.IPv4(192, 168, 42, 1)})
	m, err := Open(Options{Dir: dir, Subject: subj})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, name := range []string{"root.crt", "root.key", "leaf.crt", "leaf.key"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	snap := m.Snapshot()
	if snap.RootFingerprint == "" || snap.LeafFingerprint == "" {
		t.Errorf("fingerprints: %+v", snap)
	}
	if !snap.LeafNotAfter.After(time.Now().Add(180 * 24 * time.Hour)) {
		t.Errorf("leaf valid for less than 180d: %v", snap.LeafNotAfter)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	subj := BuildLeafSubject("knot", nil, []net.IP{net.IPv4(192, 168, 42, 1)})

	first, err := Open(Options{Dir: dir, Subject: subj})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	firstFP := first.Snapshot().LeafFingerprint

	second, err := Open(Options{Dir: dir, Subject: subj})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	secondFP := second.Snapshot().LeafFingerprint

	if firstFP != secondFP {
		t.Errorf("leaf rotated unnecessarily: %s -> %s", firstFP, secondFP)
	}
}

func TestOpenReissuesWhenSANChanges(t *testing.T) {
	dir := t.TempDir()
	subjA := BuildLeafSubject("knot", nil, []net.IP{net.IPv4(192, 168, 42, 1)})
	subjB := BuildLeafSubject("knot", nil, []net.IP{net.IPv4(10, 0, 0, 1)})

	first, err := Open(Options{Dir: dir, Subject: subjA})
	if err != nil {
		t.Fatal(err)
	}
	firstRoot := first.Snapshot().RootFingerprint
	firstLeaf := first.Snapshot().LeafFingerprint

	second, err := Open(Options{Dir: dir, Subject: subjB})
	if err != nil {
		t.Fatal(err)
	}
	secondRoot := second.Snapshot().RootFingerprint
	secondLeaf := second.Snapshot().LeafFingerprint

	if firstRoot != secondRoot {
		t.Errorf("root must NOT rotate when only leaf SAN changes")
	}
	if firstLeaf == secondLeaf {
		t.Errorf("leaf must rotate when SAN changes")
	}
}

func TestRegenerateKeepsRoot(t *testing.T) {
	dir := t.TempDir()
	subj := BuildLeafSubject("knot", nil, nil)
	m, err := Open(Options{Dir: dir, Subject: subj})
	if err != nil {
		t.Fatal(err)
	}
	rootBefore := m.Snapshot().RootFingerprint
	leafBefore := m.Snapshot().LeafFingerprint

	if err := m.Regenerate(BuildLeafSubject("knot", nil, []net.IP{net.IPv4(10, 1, 2, 3)})); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	rootAfter := m.Snapshot().RootFingerprint
	leafAfter := m.Snapshot().LeafFingerprint
	if rootBefore != rootAfter {
		t.Error("root rotated on Regenerate (should not)")
	}
	if leafBefore == leafAfter {
		t.Error("leaf did not rotate on Regenerate")
	}
}

func TestLeafChainsToRoot(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(Options{
		Dir:     dir,
		Subject: BuildLeafSubject("knot", nil, []net.IP{net.IPv4(192, 168, 42, 1)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	rootPEM := m.RootCertPEM()

	// Pull the leaf back through the TLSConfig path the HTTPS server
	// uses, then verify it chains to the root we serve at /setup-ca.crt.
	cert, err := m.TLSConfig().GetCertificate(&stdtls.ClientHelloInfo{ServerName: "knot.local"})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(rootPEM) {
		t.Fatal("AppendCertsFromPEM failed")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       pool,
		DNSName:     "knot.local",
		CurrentTime: time.Now(),
	}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestBuildLeafSubjectAddsCanonicalNames(t *testing.T) {
	subj := BuildLeafSubject("MyDevice", []string{"extra.local"}, []net.IP{net.IPv4(192, 168, 1, 1)})
	if subj.CommonName != "MyDevice" {
		t.Errorf("CN=%q", subj.CommonName)
	}
	want := map[string]bool{
		"mydevice.local": true,
		"knot.local":     true,
		"localhost":      true,
		"extra.local":    true,
	}
	for _, n := range subj.DNSNames {
		delete(want, n)
	}
	if len(want) != 0 {
		t.Errorf("missing DNS SANs: %v (got %v)", want, subj.DNSNames)
	}
	hasGateway := false
	for _, ip := range subj.IPAddresses {
		if ip.Equal(net.IPv4(192, 168, 1, 1)) {
			hasGateway = true
		}
	}
	if !hasGateway {
		t.Errorf("gateway IP missing from SAN: %v", subj.IPAddresses)
	}
}

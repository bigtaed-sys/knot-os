package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// leafValidity is the lifetime of a leaf cert. Apple platforms cap
// leaves under user-installed roots at 825 days; we stay well under
// that with 397 (the same value Let's Encrypt uses for the public
// web), so a leaf can be cycled annually with margin.
const leafValidity = 397 * 24 * time.Hour

// LeafSubject is everything the issuer needs to know about the
// device the leaf is for. Wider than just "hostname" because the
// admin UI is reached over many names (`<device>.local`, the LAN
// gateway IP, sometimes `knot.local` as a stable alias).
type LeafSubject struct {
	// CommonName is the human-visible cert subject. Usually the
	// device name from config, falling back to "knot".
	CommonName string
	// DNSNames is the SAN list. Already-deduplicated, lowercased.
	DNSNames []string
	// IPAddresses are the SAN IPs (gateway, loopback, anything else
	// we want the cert to vouch for).
	IPAddresses []net.IP
}

// BuildLeafSubject assembles a LeafSubject from the loose set of
// hints the daemon has at apply-time. Always includes localhost +
// 127.0.0.1 so a developer running with `-listen :8443` on their
// laptop gets a working cert with no extra config.
//
// Deduplicates DNS names case-insensitively; preserves first-seen
// order so the CommonName ends up first in the SAN list (common
// convention).
func BuildLeafSubject(deviceName string, extraDNS []string, extraIPs []net.IP) LeafSubject {
	cn := strings.TrimSpace(deviceName)
	if cn == "" {
		cn = "knot"
	}

	seen := map[string]struct{}{}
	add := func(out []string, name string) []string {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return out
		}
		if _, ok := seen[name]; ok {
			return out
		}
		seen[name] = struct{}{}
		return append(out, name)
	}

	dns := []string{}
	dns = add(dns, cn+".local")
	dns = add(dns, "knot.local")
	dns = add(dns, "localhost")
	for _, n := range extraDNS {
		dns = add(dns, n)
	}

	// Build the IP list. Always include loopback; sort for stable
	// output (so a regenerate doesn't churn the binary representation
	// when nothing meaningful changed).
	ipSet := map[string]net.IP{}
	add4 := func(ip net.IP) {
		if ip == nil {
			return
		}
		ipSet[ip.String()] = ip
	}
	add4(net.IPv4(127, 0, 0, 1))
	add4(net.IPv6loopback)
	for _, ip := range extraIPs {
		add4(ip)
	}
	keys := make([]string, 0, len(ipSet))
	for k := range ipSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ips := make([]net.IP, 0, len(ipSet))
	for _, k := range keys {
		ips = append(ips, ipSet[k])
	}

	return LeafSubject{
		CommonName:  cn,
		DNSNames:    dns,
		IPAddresses: ips,
	}
}

// issueLeaf signs a fresh leaf cert under the supplied root.
// Returns PEM-encoded cert and private key.
func issueLeaf(root *Root, subj LeafSubject) (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   subj.CommonName,
			Organization: []string{"KnotOS"},
		},
		NotBefore:   now.Add(-1 * time.Hour),
		NotAfter:    now.Add(leafValidity),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    subj.DNSNames,
		IPAddresses: subj.IPAddresses,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, root.Cert, &priv.PublicKey, root.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("create cert: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

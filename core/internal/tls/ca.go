// Package tls owns KnotOS's local PKI: a single ECDSA P-256 root CA
// generated once per device and re-used across reboots, plus
// short-lived leaf certificates issued under it for the daemon's
// HTTPS endpoint.
//
// The model is "captive root": the user installs the root once
// (downloaded from the captive portal during setup) and the device
// can re-issue its own leaf without rotating the root. This is the
// same pattern uhttpd uses on OpenWrt.
//
// What this package is NOT:
//
//   - A general-purpose ACME client. We deliberately don't try
//     Let's Encrypt — a router on a private LAN has no public DNS
//     name, no port-forward, and frequently no internet during the
//     setup flow when HTTPS matters most.
//   - A cert renewal scheduler. v0.3 uses 1-year leaves and
//     surfaces a "regenerate" API; an automatic-renewal goroutine
//     is scoped for v0.4 once we know how leaf rotation interacts
//     with browser session caching and pinned-cert clients.
package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// rootValidity is how long a freshly-minted root CA is valid for.
// Browsers don't (yet) cap user-installed roots, so 10 years is
// fine and removes the need for an "your router root expired"
// failure mode in practice.
const rootValidity = 10 * 365 * 24 * time.Hour

// rootCommonName is the visible name of the issuer in the
// certificate-installation UI on iOS / Android / desktop browsers.
// Kept stable across reboots so re-installing on a new device
// doesn't pile up duplicate entries with cryptic numeric names.
const rootCommonName = "KnotOS device root"

// generateRoot creates a fresh ECDSA P-256 root CA. The returned
// PEM blocks are ready to write to disk via the store.
func generateRoot() (certPEM, keyPEM []byte, err error) {
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
			CommonName:   rootCommonName,
			Organization: []string{"KnotOS"},
		},
		NotBefore:             now.Add(-1 * time.Hour), // tolerate small clock skew
		NotAfter:              now.Add(rootValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true, // no intermediate CAs — we sign leaves directly
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
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

// randomSerial picks a 128-bit positive integer per RFC 5280 §4.1.2.2.
// crypto/rand never blocks on Linux; on the slim chance we somehow
// get an unreadable randomness source, propagate the error.
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DefaultRescuePath is where the rescue keypair lives on a real
// device. Inside /etc/knot so it shares the same root-only directory
// as config.yaml; the file itself is mode 0600 with both halves
// stored together until the user reveals the private key.
const DefaultRescuePath = "/etc/knot/rescue.json"

// Rescue is a per-device Ed25519 keypair generated on first boot.
//
// Lifecycle:
//
//  1. First call to LoadOrCreateRescue with a missing file: generate
//     a fresh keypair, write {public, private, revealed=false} to
//     disk, return a Rescue with both halves in memory.
//  2. Subsequent calls: load the on-disk file. If revealed=true,
//     only the public half is loaded; the private bytes have already
//     been delivered to the user once and are gone.
//  3. RevealPrivateOnce: returns the hex-encoded private key, sets
//     revealed=true on disk, and overwrites the private bytes with
//     zeros so a later disk dump can't recover them.
//
// The user's intended workflow: download the private key on first
// login, save it in a password manager, and use it to sign
// self-built knotd binaries that the running daemon will accept
// even if the official release key is compromised or rotated.
type Rescue struct {
	mu         sync.Mutex
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	revealed   bool
	storePath  string
}

// LoadOrCreateRescue opens the rescue keypair file at path,
// generating a new keypair on first run.
func LoadOrCreateRescue(path string) (*Rescue, error) {
	if path == "" {
		path = DefaultRescuePath
	}
	r := &Rescue{storePath: path}

	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, r.generate()
	}
	if err != nil {
		return nil, err
	}
	var doc rescueDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	pub, err := hex.DecodeString(doc.Public)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("rescue: malformed public key in %s", path)
	}
	r.publicKey = ed25519.PublicKey(pub)
	r.revealed = doc.Revealed
	if doc.Private != "" {
		priv, err := hex.DecodeString(doc.Private)
		if err != nil || len(priv) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("rescue: malformed private key in %s", path)
		}
		r.privateKey = ed25519.PrivateKey(priv)
	}
	return r, nil
}

// PublicKey returns the public half. Always available, even after
// the private key has been revealed.
func (r *Rescue) PublicKey() ed25519.PublicKey {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append(ed25519.PublicKey(nil), r.publicKey...)
}

// HasPrivateKey reports whether the daemon still holds the private
// half on disk, which is true iff RevealPrivateOnce has not been
// called yet.
func (r *Rescue) HasPrivateKey() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.privateKey) > 0 && !r.revealed
}

// RevealPrivateOnce returns the hex-encoded private key exactly
// once. After this call, the private bytes are wiped from memory
// and the on-disk file is rewritten with revealed=true (and no
// private payload). Future calls return ErrAlreadyRevealed.
func (r *Rescue) RevealPrivateOnce() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.revealed || len(r.privateKey) == 0 {
		return "", ErrAlreadyRevealed
	}
	out := hex.EncodeToString(r.privateKey)
	// Zero the in-memory copy so a later goroutine can't dredge it.
	for i := range r.privateKey {
		r.privateKey[i] = 0
	}
	r.privateKey = nil
	r.revealed = true
	if err := r.persistLocked(); err != nil {
		return out, fmt.Errorf("rescue: persist after reveal: %w", err)
	}
	return out, nil
}

// ErrAlreadyRevealed is returned by RevealPrivateOnce on a second
// (or later) call. The API surfaces this as 410 Gone.
var ErrAlreadyRevealed = errors.New("rescue private key already revealed")

func (r *Rescue) generate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	r.publicKey = pub
	r.privateKey = priv
	r.revealed = false
	return r.persistLocked()
}

// persistLocked writes the keypair file atomically. Caller must
// hold r.mu.
func (r *Rescue) persistLocked() error {
	dir := filepath.Dir(r.storePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	doc := rescueDoc{
		Public:   hex.EncodeToString(r.publicKey),
		Revealed: r.revealed,
	}
	if !r.revealed && len(r.privateKey) > 0 {
		doc.Private = hex.EncodeToString(r.privateKey)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rescue-*.tmp")
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
	if err := tmp.Chmod(0o600); err != nil {
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
	return os.Rename(tmpName, r.storePath)
}

type rescueDoc struct {
	Public   string `json:"public"`
	Private  string `json:"private,omitempty"`
	Revealed bool   `json:"revealed"`
}

package vpn

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultListenPort is the standard WireGuard port used by Mullvad,
// Cloudflare WARP, and the wg-quick examples. Allowing it through
// most NATs is well-trodden territory.
const DefaultListenPort = 51820

// DefaultInterfaceCIDR is the server-side WG subnet. /24 gives 252
// usable peer slots — plenty for a home/family deployment.
const DefaultInterfaceCIDR = "10.20.30.1/24"

// ServerConfig is the per-device configuration of the WireGuard
// listener. Persisted in the same YAML file as peers.
type ServerConfig struct {
	// Enabled flips the whole feature on/off without losing peer
	// state. When false the apply path skips wg-quick entirely.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// ListenPort is the UDP port wg listens on (default 51820).
	ListenPort int `yaml:"listen_port" json:"listen_port"`
	// InterfaceCIDR is the wg0-side address with prefix length,
	// e.g. "10.20.30.1/24". Server's own IP is the first usable.
	InterfaceCIDR string `yaml:"interface_cidr" json:"interface_cidr"`
	// EndpointHost is what we put in `Endpoint = ` of the client
	// configs we hand out. Typically a DDNS hostname the user set
	// up themselves (no-ip, Cloudflare, duckdns). The wizard
	// suggests `current-wan-ip` as a fallback but warns that it
	// drifts on most ISPs.
	EndpointHost string `yaml:"endpoint_host" json:"endpoint_host"`
	// PrivateKey is the server's WG private key. Generated on
	// first construction; never returned via API; stored on disk
	// only at mode 0600 inside the encrypted /etc tree.
	PrivateKey Key `yaml:"private_key" json:"-"`
}

// Defaults returns a fresh ServerConfig appropriate for a
// just-installed device. Caller still has to generate a key and
// pick an EndpointHost.
func Defaults() ServerConfig {
	return ServerConfig{
		Enabled:       false, // opt-in
		ListenPort:    DefaultListenPort,
		InterfaceCIDR: DefaultInterfaceCIDR,
	}
}

// State is the on-disk shape of /etc/knot/wg.yaml. Keeping server
// + peers in one file means atomic writes are easy.
type State struct {
	Server ServerConfig `yaml:"server"`
	Peers  []Peer       `yaml:"peers"`
}

// Registry is the in-memory + on-disk owner of WireGuard state.
// Thread-safe: every public method takes the lock.
type Registry struct {
	mu        sync.RWMutex
	server    ServerConfig
	peers     map[string]*Peer // keyed by ID
	storePath string
}

// Open loads or creates the registry at storePath.
//
// Missing file → first-run: generate a server keypair, persist
// defaults, return.
// Existing file → load it; if PrivateKey is somehow empty (legacy
// or corruption), regenerate a fresh server keypair so the apply
// step doesn't fail later.
func Open(storePath string) (*Registry, error) {
	r := &Registry{
		peers:     make(map[string]*Peer),
		storePath: storePath,
	}
	data, err := os.ReadFile(storePath)
	if errors.Is(err, os.ErrNotExist) {
		if err := r.bootstrap(); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("vpn: read %s: %w", storePath, err)
	}
	var st State
	if err := yaml.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("vpn: parse %s: %w", storePath, err)
	}
	r.server = st.Server
	for i := range st.Peers {
		p := st.Peers[i]
		r.peers[p.ID] = &p
	}
	if (r.server == ServerConfig{}) {
		r.server = Defaults()
	}
	if r.server.PrivateKey == (Key{}) {
		priv, _, err := GenerateKeyPair()
		if err != nil {
			return nil, err
		}
		r.server.PrivateKey = priv
		if err := r.saveLocked(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) bootstrap() error {
	r.server = Defaults()
	priv, _, err := GenerateKeyPair()
	if err != nil {
		return err
	}
	r.server.PrivateKey = priv
	return r.saveLocked()
}

// Server returns a copy of the server config. PrivateKey is included
// because internal callers (config rendering, apply path) need it;
// the API layer is responsible for never serialising it back to
// HTTP responses (the JSON tag on PrivateKey is `-`).
func (r *Registry) Server() ServerConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.server
}

// PublicServerKey returns the server's public key. Safe to expose
// via API — it's what every client needs anyway.
func (r *Registry) PublicServerKey() Key {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return PublicFor(r.server.PrivateKey)
}

// SetServer mutates the user-controllable parts of the server
// config (Enabled, ListenPort, EndpointHost, InterfaceCIDR). The
// PrivateKey is preserved across the call — we never accept a
// new private key from the API surface.
func (r *Registry) SetServer(c ServerConfig) error {
	if c.ListenPort <= 0 || c.ListenPort > 65535 {
		return fmt.Errorf("listen_port: %d out of range", c.ListenPort)
	}
	if c.InterfaceCIDR == "" {
		c.InterfaceCIDR = DefaultInterfaceCIDR
	}
	r.mu.Lock()
	c.PrivateKey = r.server.PrivateKey // protect existing key
	r.server = c
	err := r.saveLocked()
	r.mu.Unlock()
	return err
}

// Peers returns a snapshot of all peers, sorted by Name then ID.
func (r *Registry) Peers() []Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Peer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// AddPeer creates a new peer record. The caller supplies the user-
// chosen name; the server generates the keypair, allocates an
// AllowedIP, and persists everything except the private key —
// the private key is returned in the response struct exactly once
// and must be sent to the client immediately, then dropped.
type AddPeerResult struct {
	Peer       Peer
	PrivateKey Key // freshly generated; not stored server-side
}

func (r *Registry) AddPeer(name, profileID string) (*AddPeerResult, error) {
	if err := ValidatePeerName(name); err != nil {
		return nil, err
	}
	priv, pub, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	id, err := NewPeerID()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	used := make([]string, 0, len(r.peers))
	for _, p := range r.peers {
		used = append(used, p.AllowedIP)
	}
	allowed, err := AllocateAllowedIP(r.server.InterfaceCIDR, used)
	if err != nil {
		return nil, err
	}

	peer := Peer{
		ID:        id,
		Name:      name,
		PublicKey: pub,
		AllowedIP: allowed,
		ProfileID: profileID,
		CreatedAt: time.Now(),
	}
	r.peers[id] = &peer
	if err := r.saveLocked(); err != nil {
		// Roll back the in-memory addition so a failed disk write
		// doesn't leak a peer the user can never reach.
		delete(r.peers, id)
		return nil, err
	}
	return &AddPeerResult{Peer: peer, PrivateKey: priv}, nil
}

// RemovePeer deletes a peer by ID.
func (r *Registry) RemovePeer(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.peers[id]; !ok {
		return fmt.Errorf("peer %q not found", id)
	}
	delete(r.peers, id)
	return r.saveLocked()
}

// SetPeerProfile updates the profile assignment for a peer.
func (r *Registry) SetPeerProfile(id, profileID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.peers[id]
	if !ok {
		return fmt.Errorf("peer %q not found", id)
	}
	p.ProfileID = profileID
	return r.saveLocked()
}

// LookupByAllowedIP finds the peer whose AllowedIP matches addr
// (without the /32 suffix). Used by the dnsDeviceLookup adapter so
// queries arriving over wg0 are attributed to the right peer's
// profile, mirroring how LAN MAC lookups work.
func (r *Registry) LookupByAllowedIP(addr string) (Peer, bool) {
	want := addr + "/32"
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.peers {
		if p.AllowedIP == want {
			return *p, true
		}
	}
	return Peer{}, false
}

// saveLocked writes the registry to disk atomically. Caller holds
// r.mu in write mode (or is inside a constructor where no
// concurrent readers exist yet).
func (r *Registry) saveLocked() error {
	if r.storePath == "" {
		return nil // memory-only mode, used in some tests
	}
	dir := filepath.Dir(r.storePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	state := State{Server: r.server}
	for _, p := range r.peers {
		state.Peers = append(state.Peers, *p)
	}
	sort.Slice(state.Peers, func(i, j int) bool {
		return state.Peers[i].ID < state.Peers[j].ID
	})
	data, err := yaml.Marshal(&state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wg-*.tmp")
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

// readRand is broken out for testability — the keypair generator
// in keys.go calls crypto/rand directly, but peer.NewPeerID uses
// this so a future test can stub it.
func readRand(b []byte) error {
	_, err := rand.Read(b)
	return err
}

// Package plugin implements plugin discovery and lifecycle bookkeeping
// for KnotOS.
//
// v0.1 scope: a plugin is a directory containing a plugin.yaml manifest.
// knotd discovers plugins on disk, lists them via /api/plugins, and
// persists enable/disable state into config.yaml. There is no plugin
// runtime yet — actual code execution arrives in v0.2 when the gRPC
// contract is finalized.
package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// ManifestFile is the file plugins must place in their directory to be
// recognized.
const ManifestFile = "plugin.yaml"

// Manifest is the parsed plugin.yaml. Fields are kept narrow on
// purpose: anything we cannot enforce in v0.1 (permissions, hooks,
// runtime selection) is omitted to keep the contract honest.
type Manifest struct {
	// ID is the plugin's stable identifier. Must match the directory
	// name and be safe for use in URLs (letters, digits, dot, hyphen,
	// underscore).
	ID string `yaml:"id" json:"id"`
	// Name is the human-readable display label.
	Name string `yaml:"name" json:"name"`
	// Version is a free-form version string, conventionally semver.
	Version string `yaml:"version" json:"version"`
	// Description is one-or-two-line summary shown on the plugin list.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// idRE accepts the usual reverse-DNS-ish or short ID styles. The
// constraint exists so an ID can be safely embedded in URL paths
// (/plugins/<id>, /api/plugins/<id>).
var idRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// validate reports an error if the manifest is structurally bad.
func (m Manifest) validate() error {
	if !idRE.MatchString(m.ID) {
		return fmt.Errorf("plugin id %q is not a valid identifier", m.ID)
	}
	if m.Name == "" {
		return errors.New("plugin name is required")
	}
	if m.Version == "" {
		return errors.New("plugin version is required")
	}
	return nil
}

// Plugin is a manifest enriched with runtime state (currently just
// enabled/disabled). It is what the API surfaces.
type Plugin struct {
	Manifest `yaml:",inline" json:",inline"`
	Enabled  bool `yaml:"enabled" json:"enabled"`
}

// Registry is the in-memory list of plugins discovered on disk plus
// their enabled state. It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
	dir     string
}

// NewRegistry constructs an empty Registry rooted at dir. Discover
// must be called before the first read.
func NewRegistry(dir string) *Registry {
	return &Registry{plugins: make(map[string]Plugin), dir: dir}
}

// Discover scans the plugins directory for valid manifests. Plugins
// that fail to parse or validate are skipped with an error returned in
// the second return value (a multi-error joined by errors.Join);
// successful parses still populate the registry.
//
// Existing enabled state in the registry (set via Apply) is preserved
// across rediscoveries: only newly-found plugins start disabled.
func (r *Registry) Discover() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			// No plugins dir is fine — the registry stays empty.
			r.mu.Lock()
			r.plugins = make(map[string]Plugin)
			r.mu.Unlock()
			return nil
		}
		return fmt.Errorf("read %s: %w", r.dir, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	prev := r.plugins
	r.plugins = make(map[string]Plugin)
	var errs []error

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(r.dir, e.Name(), ManifestFile)
		m, err := loadManifest(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if m.ID != e.Name() {
			errs = append(errs, fmt.Errorf("%s: id %q does not match directory name %q", path, m.ID, e.Name()))
			continue
		}
		p := Plugin{Manifest: m}
		if old, ok := prev[m.ID]; ok {
			p.Enabled = old.Enabled
		}
		r.plugins[m.ID] = p
	}

	return errors.Join(errs...)
}

// loadManifest reads and parses a single manifest file.
func loadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("yaml parse: %w", err)
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// List returns a stable, alphabetical copy of the registered plugins.
func (r *Registry) List() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a single plugin by ID, or false if it is not registered.
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[id]
	return p, ok
}

// SetEnabled flips the enabled flag. Returns false if the plugin does
// not exist.
func (r *Registry) SetEnabled(id string, enabled bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[id]
	if !ok {
		return false
	}
	p.Enabled = enabled
	r.plugins[id] = p
	return true
}

// ApplyEnabledMap synchronizes the registry with the enabled-state map
// stored in config.yaml. Plugins missing from the map remain disabled.
// Plugins in the map but not on disk are silently ignored — their
// state will be re-applied if/when the plugin is re-installed.
func (r *Registry) ApplyEnabledMap(enabled map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.plugins {
		p.Enabled = enabled[id]
		r.plugins[id] = p
	}
}

// EnabledMap returns the inverse: a config-friendly view of which
// plugins are currently on. Disabled plugins are omitted to keep
// config.yaml tidy.
func (r *Registry) EnabledMap() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]bool)
	for id, p := range r.plugins {
		if p.Enabled {
			out[id] = true
		}
	}
	return out
}

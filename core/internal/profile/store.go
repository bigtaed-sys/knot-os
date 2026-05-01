package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load reads the YAML store and merges user-defined profiles on top
// of the built-ins (which are already in the registry from
// NewRegistry). User-defined profiles can override built-ins'
// editable fields (Schedule, DNSBlocklists, Description) but cannot
// rename or delete a built-in — Builtin stays true.
//
// Missing file is not an error: a fresh device starts with just the
// built-ins.
func (r *Registry) Load() error {
	data, err := os.ReadFile(r.store)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc storeDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", r.store, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range doc.Profiles {
		if err := p.Validate(); err != nil {
			// Skip malformed entries with a log via Save's caller —
			// here we silently ignore. M11+ may add a logger field.
			continue
		}
		if existing, ok := r.profiles[p.ID]; ok && existing.Builtin {
			p.Builtin = true
		}
		pp := p
		r.profiles[p.ID] = &pp
	}
	r.dirty = false
	return nil
}

// Save writes only the non-builtin profiles plus any builtin whose
// editable fields differ from the seed (so user customizations to a
// built-in survive a restart). Atomic via temp+rename.
func (r *Registry) Save() error {
	r.mu.RLock()
	seed := make(map[string]Profile, 8)
	for _, b := range builtins() {
		seed[b.ID] = b
	}
	out := storeDoc{}
	for _, p := range r.profiles {
		// Persist non-builtins always; builtins only if they differ
		// from the seed in any user-editable field.
		if !p.Builtin {
			out.Profiles = append(out.Profiles, *p)
			continue
		}
		s := seed[p.ID]
		if !sameEditable(*p, s) {
			out.Profiles = append(out.Profiles, *p)
		}
	}
	r.mu.RUnlock()

	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.store), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.store), ".profiles-*.yaml.tmp")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, r.store); err != nil {
		cleanup()
		return err
	}
	r.mu.Lock()
	r.dirty = false
	r.mu.Unlock()
	return nil
}

// FlushIfDirty writes only when there are pending changes.
func (r *Registry) FlushIfDirty() error {
	r.mu.RLock()
	dirty := r.dirty
	r.mu.RUnlock()
	if !dirty {
		return nil
	}
	return r.Save()
}

func sameEditable(a, b Profile) bool {
	if a.Description != b.Description {
		return false
	}
	if len(a.BlockWindows) != len(b.BlockWindows) {
		return false
	}
	for i := range a.BlockWindows {
		x, y := a.BlockWindows[i], b.BlockWindows[i]
		if x.Start != y.Start || x.End != y.End {
			return false
		}
		if !sameInts(x.Days, y.Days) {
			return false
		}
	}
	if len(a.DNSBlocklists) != len(b.DNSBlocklists) {
		return false
	}
	for i := range a.DNSBlocklists {
		if a.DNSBlocklists[i] != b.DNSBlocklists[i] {
			return false
		}
	}
	return true
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type storeDoc struct {
	Profiles []Profile `yaml:"profiles"`
}

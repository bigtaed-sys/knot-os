package config

import "github.com/knot-os/knot-os/core/internal/secrets"

// secretFields lists every place in Config that holds an at-rest
// secret. Keeping the list narrow and explicit so a future field
// added in a hurry doesn't accidentally bypass encryption — the
// compiler error from "nope, this field isn't here" is the prompt.

func (c *Config) wrapSecrets(s Sealer) error {
	for _, p := range c.secretPtrs() {
		w, err := s.Wrap(*p)
		if err != nil {
			return err
		}
		*p = w
	}
	return nil
}

func (c *Config) unwrapSecrets(s Sealer) error {
	for _, p := range c.secretPtrs() {
		u, err := s.Unwrap(*p)
		if err != nil {
			return err
		}
		*p = u
	}
	return nil
}

// secretPtrs returns pointers to every secret string in the config.
// nil-safe for the optional Network sub-structs.
func (c *Config) secretPtrs() []*string {
	out := make([]*string, 0, 2)
	if c.Network.Uplink != nil {
		out = append(out, &c.Network.Uplink.PSK)
	}
	if c.Network.AP != nil {
		out = append(out, &c.Network.AP.PSK)
	}
	return out
}

// hasCleartextSecrets reports whether any secret-bearing field is
// non-empty AND not yet wrapped. Used by main.go to trigger a
// one-shot re-save that encrypts a v0.1/v0.2 config in place.
func (c *Config) hasCleartextSecrets() bool {
	for _, p := range c.secretPtrs() {
		if *p != "" && !secrets.IsEncrypted(*p) {
			return true
		}
	}
	return false
}

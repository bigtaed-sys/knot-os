package network

import (
	"context"
	"sync"

	"github.com/knot-os/knot-os/core/internal/config"
)

// MockBackend is a Backend implementation that records calls and reports a
// plausible status without touching the host. It's used by `knotd -dev` and
// in unit tests.
type MockBackend struct {
	mu       sync.Mutex
	last     config.Config
	hasState bool
	// Applies counts successful Apply calls. Useful in tests.
	Applies int
}

// NewMock returns a fresh MockBackend with no recorded state.
func NewMock() *MockBackend {
	return &MockBackend{}
}

// Name implements Backend.
func (m *MockBackend) Name() string { return "mock" }

// Apply implements Backend by recording the config and incrementing the
// counter. It never returns an error.
func (m *MockBackend) Apply(_ context.Context, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last = cfg
	m.hasState = true
	m.Applies++
	return nil
}

// Status implements Backend by synthesizing a plausible Status from the
// last applied config. Before any Apply has run, it reports the "setup"
// role with no uplink/AP, mimicking a freshly booted device.
func (m *MockBackend) Status(_ context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.hasState {
		return Status{Backend: m.Name(), Role: config.RoleSetup}, nil
	}

	st := Status{Backend: m.Name(), Role: m.last.Role}
	if m.last.Network.Uplink != nil {
		st.Uplink = &UplinkStatus{
			SSID:      m.last.Network.Uplink.SSID,
			Connected: true, // mock pretends uplink is always healthy
			RSSIdBm:   -55,  // a "good signal" placeholder
		}
	}
	if m.last.Network.AP != nil {
		st.AP = &APStatus{
			SSID: m.last.Network.AP.SSID,
			Up:   true,
		}
	}
	return st, nil
}

// Last returns the most recently applied config. Returns the zero Config
// and false if Apply has never been called.
func (m *MockBackend) Last() (config.Config, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last, m.hasState
}

// Scan implements Backend by returning a deterministic set of fake
// networks. Useful for exercising the wizard UI without real radios.
func (m *MockBackend) Scan(_ context.Context) ([]ScannedNetwork, error) {
	return []ScannedNetwork{
		{SSID: "HomeWiFi", BSSID: "aa:bb:cc:dd:ee:01", Channel: 6, Band: "2.4", RSSIdBm: -45, Secured: true},
		{SSID: "Neighbor", BSSID: "aa:bb:cc:dd:ee:02", Channel: 11, Band: "2.4", RSSIdBm: -68, Secured: true},
		{SSID: "FreeCafe", BSSID: "aa:bb:cc:dd:ee:03", Channel: 1, Band: "2.4", RSSIdBm: -78, Secured: false},
		{SSID: "HomeWiFi-5G", BSSID: "aa:bb:cc:dd:ee:04", Channel: 36, Band: "5", RSSIdBm: -55, Secured: true},
	}, nil
}

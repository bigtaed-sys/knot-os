package network

import (
	"context"
	"testing"

	"github.com/knot-os/knot-os/core/internal/config"
)

func TestMockInitialStatusIsSetup(t *testing.T) {
	m := NewMock()
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Role != config.RoleSetup {
		t.Errorf("initial role: want %q, got %q", config.RoleSetup, st.Role)
	}
	if st.Uplink != nil || st.AP != nil {
		t.Errorf("initial Uplink/AP should be nil, got %+v / %+v", st.Uplink, st.AP)
	}
}

func TestMockApplyPopulatesStatus(t *testing.T) {
	m := NewMock()
	cfg := config.Config{
		Device: config.Device{Name: "knot", Country: "RU"},
		Role:   config.RoleWiFiExtender,
		Network: config.Network{
			Uplink: &config.WiFiUplink{SSID: "Home"},
			AP:     &config.WiFiAP{SSID: "Knot", Band: "2.4"},
		},
	}
	if err := m.Apply(context.Background(), cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if m.Applies != 1 {
		t.Fatalf("Applies: want 1, got %d", m.Applies)
	}

	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Role != config.RoleWiFiExtender {
		t.Errorf("role: want wifi-extender, got %q", st.Role)
	}
	if st.Uplink == nil || st.Uplink.SSID != "Home" {
		t.Errorf("uplink mismatch: %+v", st.Uplink)
	}
	if st.AP == nil || st.AP.SSID != "Knot" {
		t.Errorf("AP mismatch: %+v", st.AP)
	}
}

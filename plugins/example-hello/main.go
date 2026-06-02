// example-hello is the reference KnotOS plugin. It demonstrates the
// v2 plugin runtime contract end to end:
//
//   - knotd launches this process while the plugin is enabled, handing
//     it KNOT_PLUGIN_SOCKET (where to listen) and KNOT_HOST_SOCKET +
//     KNOT_HOST_TOKEN (how to call back into the router).
//   - The plugin serves a *declarative UI spec* (JSON) on its Unix
//     socket; knotd's web UI renders it with native KnotOS components,
//     so the page matches the rest of the app and no plugin HTML/JS
//     runs in the admin UI.
//   - The spec is built from live router state read through the host
//     API (status + devices) plus a feed of router events streamed
//     from /host/v1/events — exercising the read and reactive halves
//     of the permission model (this plugin declares status:read,
//     devices:read, events:read in plugin.yaml).
//
// Pure standard library — no dependencies, so it cross-compiles for
// the Pi with a one-line `go build`.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// --- the declarative UI spec the KnotOS web UI renders --------------------

type uiItem struct {
	Type    string     `json:"type"` // stat | text | badge | table
	Label   string     `json:"label,omitempty"`
	Value   string     `json:"value,omitempty"`
	Tone    string     `json:"tone,omitempty"` // ok | warn | bad | neutral
	Text    string     `json:"text,omitempty"`
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
}

type uiSection struct {
	Title string   `json:"title,omitempty"`
	Items []uiItem `json:"items"`
}

type uiSpec struct {
	Title      string      `json:"title,omitempty"`
	RefreshSec int         `json:"refresh_sec,omitempty"`
	Sections   []uiSection `json:"sections"`
}

func main() {
	log.SetFlags(0)
	sock := os.Getenv("KNOT_PLUGIN_SOCKET")
	if sock == "" {
		log.Fatal("KNOT_PLUGIN_SOCKET not set — run me under knotd")
	}
	host := newHostClient(os.Getenv("KNOT_HOST_SOCKET"), os.Getenv("KNOT_HOST_TOKEN"))

	feed := &eventFeed{}
	go host.streamEvents(feed)

	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatalf("listen %s: %v", sock, err)
	}
	log.Printf("example-hello listening on %s", sock)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildSpec(host, feed))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.Serve(ln))
}

func buildSpec(host *hostClient, feed *eventFeed) uiSpec {
	var status struct {
		Role       string `json:"role"`
		DeviceName string `json:"device_name"`
		Version    string `json:"version"`
		WANUp      bool   `json:"wan_up"`
	}
	var devices struct {
		Devices []struct {
			Online bool `json:"online"`
		} `json:"devices"`
	}
	_ = host.get("/host/v1/status", &status)
	_ = host.get("/host/v1/devices", &devices)

	online := 0
	for _, d := range devices.Devices {
		if d.Online {
			online++
		}
	}
	wanTone, wanVal := "bad", "down"
	if status.WANUp {
		wanTone, wanVal = "ok", "up"
	}

	rows := [][]string{}
	for _, e := range feed.snapshot() {
		if t, k, ok := strings.Cut(e, "  "); ok {
			rows = append(rows, []string{t, k})
		}
	}
	if len(rows) == 0 {
		rows = append(rows, []string{"—", "waiting for events…"})
	}

	return uiSpec{
		Title:      "Hello Plugin",
		RefreshSec: 5,
		Sections: []uiSection{
			{
				Title: "Router",
				Items: []uiItem{
					{Type: "text", Text: "Served by a separate process and rendered natively by KnotOS. Values come from the host API."},
					{Type: "stat", Label: "Device", Value: status.DeviceName},
					{Type: "stat", Label: "Role", Value: status.Role},
					{Type: "stat", Label: "knotd version", Value: status.Version},
					{Type: "stat", Label: "WAN", Value: wanVal, Tone: wanTone},
					{Type: "stat", Label: "Devices online", Value: fmt.Sprintf("%d / %d", online, len(devices.Devices))},
				},
			},
			{
				Title: "Live events",
				Items: []uiItem{
					{Type: "table", Columns: []string{"Time", "Event"}, Rows: rows},
				},
			},
		},
	}
}

// --- host API client ------------------------------------------------------

type hostClient struct {
	rpc    *http.Client
	stream *http.Client
	token  string
}

func newHostClient(sock, token string) *hostClient {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}
	return &hostClient{
		token:  token,
		rpc:    &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: dial}},
		stream: &http.Client{Transport: &http.Transport{DialContext: dial}},
	}
}

func (h *hostClient) get(path string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, "http://host"+path, nil)
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.rpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("host %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (h *hostClient) streamEvents(feed *eventFeed) {
	for {
		if err := h.readEvents(feed); err != nil {
			feed.note(fmt.Sprintf("stream error: %v", err))
		}
		time.Sleep(2 * time.Second)
	}
}

func (h *hostClient) readEvents(feed *eventFeed) error {
	req, _ := http.NewRequest(http.MethodGet, "http://host/host/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.stream.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "event: ") {
			feed.note(strings.TrimPrefix(line, "event: "))
		}
	}
	return sc.Err()
}

// eventFeed is a tiny thread-safe ring of recent "HH:MM:SS  kind" lines.
type eventFeed struct {
	mu    sync.Mutex
	items []string
}

func (f *eventFeed) note(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append([]string{time.Now().Format("15:04:05") + "  " + kind}, f.items...)
	if len(f.items) > 12 {
		f.items = f.items[:12]
	}
}

func (f *eventFeed) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.items...)
}

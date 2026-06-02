// ddns is a KnotOS plugin that keeps a dynamic-DNS hostname pointed at
// the router's changing public IP. It supports DuckDNS and any
// provider with a simple GET-to-update URL ("custom").
//
// It demonstrates the interactive + stateful side of the plugin
// platform: a config form (rendered natively by knotd from a JSON UI
// spec), persistent config in KNOT_PLUGIN_DATA, the host API for the
// current WAN IP + an event subscription to react to IP changes, and
// the `network` permission so it can actually reach the provider.
//
// Standard library only — cross-compiles with a one-line `go build`.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type config struct {
	Provider  string `json:"provider"`   // "duckdns" | "custom"
	Domain    string `json:"domain"`     // DuckDNS subdomain(s), comma-separated
	Token     string `json:"token"`      // DuckDNS token (secret)
	CustomURL string `json:"custom_url"` // template; {ip} is substituted
}

type state struct {
	mu         sync.Mutex
	cfg        config
	lastIP     string
	lastRun    time.Time
	lastResult string
	lastOK     bool
}

var (
	st       = &state{}
	dataPath string
	host     *hostClient
)

func main() {
	log.SetFlags(0)
	sock := os.Getenv("KNOT_PLUGIN_SOCKET")
	if sock == "" {
		log.Fatal("KNOT_PLUGIN_SOCKET not set — run me under knotd")
	}
	if d := os.Getenv("KNOT_PLUGIN_DATA"); d != "" {
		dataPath = filepath.Join(d, "config.json")
	}
	host = newHostClient(os.Getenv("KNOT_HOST_SOCKET"), os.Getenv("KNOT_HOST_TOKEN"))
	st.cfg = loadConfig()

	go watch() // react to WAN IP changes + periodic re-assert

	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatalf("listen %s: %v", sock, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/save", handleSave)
	mux.HandleFunc("/update", handleUpdate)
	mux.HandleFunc("/", handleSpec)
	log.Printf("ddns listening on %s", sock)
	log.Fatal((&http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}).Serve(ln))
}

// --- config persistence ---------------------------------------------------

func loadConfig() config {
	var c config
	if dataPath == "" {
		return c
	}
	if b, err := os.ReadFile(dataPath); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Provider == "" {
		c.Provider = "duckdns"
	}
	return c
}

func saveConfig(c config) error {
	if dataPath == "" {
		return fmt.Errorf("no data dir")
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(dataPath, b, 0o600)
}

// --- HTTP handlers ---------------------------------------------------------

func handleSpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(buildSpec())
}

func handleSave(w http.ResponseWriter, r *http.Request) {
	var in config
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, `{"error":{"message":"bad json"}}`, http.StatusBadRequest)
		return
	}
	st.mu.Lock()
	// Empty token means "keep the current one" (the UI never echoes the
	// saved secret back).
	if strings.TrimSpace(in.Token) == "" {
		in.Token = st.cfg.Token
	}
	if in.Provider == "" {
		in.Provider = "duckdns"
	}
	st.cfg = in
	st.mu.Unlock()
	if err := saveConfig(in); err != nil {
		http.Error(w, `{"error":{"message":"save failed"}}`, http.StatusInternalServerError)
		return
	}
	go runUpdate()
	writeOK(w)
}

func handleUpdate(w http.ResponseWriter, _ *http.Request) {
	go runUpdate()
	writeOK(w)
}

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// --- the update itself -----------------------------------------------------

// watch performs an initial update, then re-runs on every WAN status
// event and on a slow timer (to catch silent IP changes / re-assert).
func watch() {
	time.Sleep(2 * time.Second)
	runUpdate()
	events := make(chan struct{}, 8)
	go host.streamWANEvents(events)
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-events:
			runUpdate()
		case <-tick.C:
			runUpdate()
		}
	}
}

func runUpdate() {
	st.mu.Lock()
	cfg := st.cfg
	st.mu.Unlock()

	ip := host.wanIP()
	result, ok := doUpdate(cfg, ip)

	st.mu.Lock()
	st.lastIP = ip
	st.lastRun = time.Now()
	st.lastResult = result
	st.lastOK = ok
	st.mu.Unlock()
	log.Printf("ddns: update ip=%q result=%q", ip, result)
}

// doUpdate hits the configured provider. Returns a human result + ok.
func doUpdate(cfg config, ip string) (string, bool) {
	switch cfg.Provider {
	case "duckdns":
		if cfg.Domain == "" || cfg.Token == "" {
			return "not configured (need domain + token)", false
		}
		u := "https://www.duckdns.org/update?domains=" + cfg.Domain + "&token=" + cfg.Token
		if ip != "" {
			u += "&ip=" + ip
		}
		body, err := httpGet(u)
		if err != nil {
			return err.Error(), false
		}
		if strings.HasPrefix(strings.TrimSpace(body), "OK") {
			return "OK", true
		}
		return "provider replied: " + strings.TrimSpace(body), false
	case "custom":
		if cfg.CustomURL == "" {
			return "not configured (need update URL)", false
		}
		u := strings.ReplaceAll(cfg.CustomURL, "{ip}", ip)
		if _, err := httpGet(u); err != nil {
			return err.Error(), false
		}
		return "OK", true
	default:
		return "unknown provider", false
	}
}

func httpGet(url string) (string, error) {
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(b), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(b), nil
}

// --- the UI spec -----------------------------------------------------------

type uiItem struct {
	Type        string     `json:"type"`
	Label       string     `json:"label,omitempty"`
	Value       string     `json:"value,omitempty"`
	Tone        string     `json:"tone,omitempty"`
	Text        string     `json:"text,omitempty"`
	Key         string     `json:"key,omitempty"`
	Placeholder string     `json:"placeholder,omitempty"`
	Secret      bool       `json:"secret,omitempty"`
	Action      string     `json:"action,omitempty"`
	Style       string     `json:"style,omitempty"`
	Options     []uiOption `json:"options,omitempty"`
}
type uiOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
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

func buildSpec() uiSpec {
	st.mu.Lock()
	cfg := st.cfg
	lastIP, lastRun, lastResult, lastOK := st.lastIP, st.lastRun, st.lastResult, st.lastOK
	st.mu.Unlock()

	ip := lastIP
	if ip == "" {
		ip = host.wanIP()
	}
	if ip == "" {
		ip = "unknown"
	}
	domain := cfg.Domain
	if domain == "" {
		domain = "—"
	}
	last := "never"
	resultItem := uiItem{Type: "stat", Label: "Last result", Value: "—", Tone: "neutral"}
	if !lastRun.IsZero() {
		last = lastRun.Format("2006-01-02 15:04:05")
		tone := "bad"
		if lastOK {
			tone = "ok"
		}
		resultItem = uiItem{Type: "stat", Label: "Last result", Value: lastResult, Tone: tone}
	}

	tokenPlaceholder := "DuckDNS token"
	if cfg.Token != "" {
		tokenPlaceholder = "saved — leave blank to keep"
	}

	return uiSpec{
		Title:      "Dynamic DNS",
		RefreshSec: 10,
		Sections: []uiSection{
			{
				Title: "Status",
				Items: []uiItem{
					{Type: "stat", Label: "Public IP", Value: ip},
					{Type: "stat", Label: "Domain", Value: domain},
					{Type: "stat", Label: "Last update", Value: last},
					resultItem,
				},
			},
			{
				Title: "Settings",
				Items: []uiItem{
					{Type: "select", Key: "provider", Label: "Provider", Value: cfg.Provider, Options: []uiOption{
						{Value: "duckdns", Label: "DuckDNS"},
						{Value: "custom", Label: "Custom update URL"},
					}},
					{Type: "input", Key: "domain", Label: "DuckDNS domain(s)", Value: cfg.Domain, Placeholder: "myhome"},
					{Type: "input", Key: "token", Label: "DuckDNS token", Secret: true, Placeholder: tokenPlaceholder},
					{Type: "input", Key: "custom_url", Label: "Custom update URL ({ip} is substituted)", Value: cfg.CustomURL, Placeholder: "https://example.com/update?host=...&myip={ip}"},
					{Type: "action", Label: "Save & update", Action: "save"},
					{Type: "action", Label: "Update now", Action: "update", Style: "ghost"},
				},
			},
		},
	}
}

// --- host API client -------------------------------------------------------

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

func (h *hostClient) wanIP() string {
	req, _ := http.NewRequest(http.MethodGet, "http://host/host/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.rpc.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var s struct {
		WANIP string `json:"wan_ip"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&s)
	return s.WANIP
}

// streamWANEvents pings the channel whenever a wan_status event arrives.
func (h *hostClient) streamWANEvents(ping chan<- struct{}) {
	for {
		if err := h.readEvents(ping); err != nil {
			log.Printf("ddns: event stream: %v", err)
		}
		time.Sleep(3 * time.Second)
	}
}

func (h *hostClient) readEvents(ping chan<- struct{}) error {
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
		if line := sc.Text(); strings.HasPrefix(line, "event: wan_status") {
			select {
			case ping <- struct{}{}:
			default:
			}
		}
	}
	return sc.Err()
}

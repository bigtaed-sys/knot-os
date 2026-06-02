// example-hello is the reference KnotOS plugin. It demonstrates the
// v2 plugin runtime contract end to end:
//
//   - knotd launches this process while the plugin is enabled, handing
//     it KNOT_PLUGIN_SOCKET (where to listen) and KNOT_HOST_SOCKET +
//     KNOT_HOST_TOKEN (how to call back into the router).
//   - The plugin serves an HTML page on its Unix socket; knotd
//     reverse-proxies it under /api/plugins/example-hello/proxy/.
//   - The page calls the host API for live router state, demonstrating
//     the permission model (this plugin declares status:read +
//     devices:read in plugin.yaml).
//
// Pure standard library — no dependencies, so it cross-compiles for
// the Pi with a one-line `go build`.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	log.SetFlags(0)
	sock := os.Getenv("KNOT_PLUGIN_SOCKET")
	if sock == "" {
		log.Fatal("KNOT_PLUGIN_SOCKET not set — run me under knotd")
	}
	host := newHostClient(os.Getenv("KNOT_HOST_SOCKET"), os.Getenv("KNOT_HOST_TOKEN"))

	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatalf("listen %s: %v", sock, err)
	}
	log.Printf("example-hello listening on %s", sock)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		page(w, host)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.Serve(ln))
}

// hostClient talks to knotd's host API over its Unix socket.
type hostClient struct {
	c     *http.Client
	token string
}

func newHostClient(sock, token string) *hostClient {
	return &hostClient{
		token: token,
		c: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", sock)
				},
			},
		},
	}
}

func (h *hostClient) get(path string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, "http://host"+path, nil)
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("host %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func page(w http.ResponseWriter, host *hostClient) {
	var status struct {
		Role       string `json:"role"`
		DeviceName string `json:"device_name"`
		Version    string `json:"version"`
		WANUp      bool   `json:"wan_up"`
	}
	var devices struct {
		Devices []struct {
			Label  string `json:"label"`
			IP     string `json:"ip"`
			Online bool   `json:"online"`
		} `json:"devices"`
	}
	statusErr := host.get("/host/v1/status", &status)
	devErr := host.get("/host/v1/devices", &devices)

	online := 0
	for _, d := range devices.Devices {
		if d.Online {
			online++
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<style>
 body{font-family:system-ui,sans-serif;margin:0;padding:2rem;background:#fafafa;color:#18181b}
 .card{max-width:640px;margin:0 auto;background:#fff;border:1px solid #e4e4e7;border-radius:14px;padding:1.5rem}
 h1{margin:.2rem 0 1rem;font-size:1.4rem}
 .row{display:flex;justify-content:space-between;padding:.5rem 0;border-bottom:1px solid #f4f4f5}
 .k{color:#71717a}.v{font-weight:600}
 .pill{display:inline-block;padding:.1rem .5rem;border-radius:999px;font-size:.8rem}
 .ok{background:#dcfce7;color:#166534}.no{background:#fee2e2;color:#991b1b}
 .err{color:#b91c1c;font-size:.85rem;margin-top:1rem}
</style></head><body><div class=card>
 <h1>👋 Hello from a KnotOS plugin</h1>
 <p style="color:#52525b">This page is served by a separate process (<code>example-hello</code>) and proxied by knotd. The numbers below come from the host API.</p>
 <div class=row><span class=k>Device</span><span class=v>%s</span></div>
 <div class=row><span class=k>Role</span><span class=v>%s</span></div>
 <div class=row><span class=k>knotd version</span><span class=v>%s</span></div>
 <div class=row><span class=k>WAN</span><span class="pill %s">%s</span></div>
 <div class=row><span class=k>Devices online</span><span class=v>%d / %d</span></div>`,
		html.EscapeString(status.DeviceName),
		html.EscapeString(status.Role),
		html.EscapeString(status.Version),
		pill(status.WANUp), upDown(status.WANUp),
		online, len(devices.Devices),
	)
	if statusErr != nil {
		fmt.Fprintf(w, `<div class=err>status: %s</div>`, html.EscapeString(statusErr.Error()))
	}
	if devErr != nil {
		fmt.Fprintf(w, `<div class=err>devices: %s</div>`, html.EscapeString(devErr.Error()))
	}
	fmt.Fprint(w, `</div></body></html>`)
}

func pill(up bool) string {
	if up {
		return "ok"
	}
	return "no"
}
func upDown(up bool) string {
	if up {
		return "up"
	}
	return "down"
}

// Package httpserver wires together the HTTP listener that exposes the
// REST API and the embedded SvelteKit UI.
package httpserver

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	knottls "github.com/knot-os/knot-os/core/internal/tls"
	"github.com/knot-os/knot-os/core/internal/web"
)

// Options configures the HTTP server.
type Options struct {
	// Addr is the plain-HTTP listen address (e.g. ":80", ":8080").
	// Empty disables the HTTP listener (HTTPS-only mode).
	Addr string
	// TLSAddr is the HTTPS listen address (e.g. ":443"). Empty
	// disables the HTTPS listener.
	TLSAddr string
	// TLS supplies the cert+key for the HTTPS listener and the PEM
	// bytes for the public /setup-ca.crt download. nil disables
	// HTTPS regardless of TLSAddr.
	TLS *knottls.Materials
	// Logger receives structured log lines. If nil, log.Default() is used.
	Logger *log.Logger
}

// Server is the HTTP front door of knotd. It serves:
//   - /api/*  — REST API (mounted by callers via Mount)
//   - /*      — embedded SvelteKit UI with SPA fallback
//
// In dual-listener mode (TLS materials present + both Addr and
// TLSAddr set) the plain-HTTP listener redirects to HTTPS for
// every path EXCEPT /healthz and /setup-ca.crt — the latter must
// stay plaintext because the user is reaching it specifically to
// install the trust root that would let them hit HTTPS without a
// browser warning. The redirect is gated by a "redirect mode"
// flag the daemon flips off in setup role: with no installed cert
// yet, redirecting to HTTPS would land the user on a self-signed
// page in the middle of the wizard, which is exactly the trust
// dance we're trying to avoid during onboarding.
type Server struct {
	opts   Options
	router chi.Router
	srv    *http.Server
	tlsSrv *http.Server

	// redirectHTTPS, when 1, makes the plain-HTTP listener 301 to
	// HTTPS for every non-allowlisted path. Atomic so SetRedirect
	// is safe from any goroutine.
	redirectHTTPS atomic.Bool

	// blockedPageFn, when set, is consulted per request: if it returns
	// (page, true) for the client IP, that HTML is served for any path
	// instead of the app — the "blocked / awaiting approval" landing.
	// Set once before Start.
	blockedPageFn func(clientIP string) ([]byte, bool)
}

// SetBlockedPageFn installs the blocked-device landing hook. Call
// before Start.
func (s *Server) SetBlockedPageFn(fn func(clientIP string) ([]byte, bool)) {
	s.blockedPageFn = fn
}

// New constructs a Server but does not start it.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(opts.Logger))

	s := &Server{opts: opts, router: r}

	// Blocked-device landing: a paused / awaiting-approval device's DNS
	// is captive-redirected here, so serve it the explanatory page for
	// any path. Exempts the public allowlist (healthz / CA download).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if s.blockedPageFn != nil && !plaintextAllowed(req.URL.Path) {
				if page, blocked := s.blockedPageFn(clientIP(req)); blocked {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Header().Set("Cache-Control", "no-store")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(page)
					return
				}
			}
			next.ServeHTTP(w, req)
		})
	})

	// Health endpoint — always available, never auth-gated, used by readiness
	// probes and CI smoke tests.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	// Public root-CA download. Always available on both listeners
	// without auth: the user is here precisely because they don't
	// trust the daemon yet. mime type chosen to make every browser
	// "save as" instead of trying to render binary PEM as text.
	if opts.TLS != nil {
		r.Get("/setup-ca.crt", func(w http.ResponseWriter, _ *http.Request) {
			pem := opts.TLS.RootCertPEM()
			w.Header().Set("Content-Type", "application/x-x509-ca-cert")
			w.Header().Set("Content-Disposition", `attachment; filename="knot-root-ca.crt"`)
			_, _ = w.Write(pem)
		})
	}

	// Static UI — registered last so /api routes mounted by callers take
	// precedence. The handler itself implements SPA fallback.
	r.Handle("/*", web.Handler())

	if opts.Addr != "" {
		s.srv = &http.Server{
			Addr:              opts.Addr,
			Handler:           s.httpHandler(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
	}
	if opts.TLSAddr != "" && opts.TLS != nil {
		s.tlsSrv = &http.Server{
			Addr:              opts.TLSAddr,
			Handler:           r,
			TLSConfig:         opts.TLS.TLSConfig(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
	}

	return s
}

// SetRedirectHTTPS turns the HTTP→HTTPS redirect on or off. Safe
// to call after Start.
func (s *Server) SetRedirectHTTPS(on bool) { s.redirectHTTPS.Store(on) }

// Mount registers an additional sub-router (typically /api/*).
// Call before Start.
func (s *Server) Mount(pattern string, handler http.Handler) {
	s.router.Mount(pattern, handler)
}

// httpHandler returns the handler for the plain-HTTP listener.
// When redirectHTTPS is on, every path except the public allowlist
// is 301'd to https://<host>/<path>; otherwise the full router is
// served (used in setup role and dev mode).
func (s *Server) httpHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.redirectHTTPS.Load() && !plaintextAllowed(r.URL.Path) {
			target := "https://" + redirectHost(r) + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		s.router.ServeHTTP(w, r)
	})
}

// clientIP returns the request's source IP without the port. With
// middleware.RealIP upstream, RemoteAddr already reflects the real
// client; we just drop the port if present.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// plaintextAllowed reports whether the path is exempt from the
// HTTPS redirect. Kept narrow on purpose.
func plaintextAllowed(path string) bool {
	return path == "/healthz" || path == "/setup-ca.crt"
}

// redirectHost picks the hostname to use in the redirect target.
// Strips the source port if present; the HTTPS listener may live
// on a different one. Falls back to the request host if we can't
// parse it.
func redirectHost(r *http.Request) string {
	h := r.Host
	if idx := indexLast(h, ':'); idx >= 0 {
		// Don't strip the colon for IPv6 literals like [::1]:80; only
		// strip when the colon is *not* inside brackets.
		if h[0] != '[' {
			h = h[:idx]
		}
	}
	return h
}

func indexLast(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// Start blocks until ctx is cancelled or both listeners stop.
func (s *Server) Start(ctx context.Context) error {
	if !web.IsBuilt() {
		s.opts.Logger.Printf("warning: embedded UI bundle is empty — run scripts/build-ui.sh before building knotd")
	}

	errCh := make(chan error, 2)

	if s.srv != nil {
		s.opts.Logger.Printf("HTTP listening on %s", s.opts.Addr)
		go func() {
			err := s.srv.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		}()
	}
	if s.tlsSrv != nil {
		s.opts.Logger.Printf("HTTPS listening on %s", s.opts.TLSAddr)
		go func() {
			// Cert/key come from TLSConfig.GetCertificate, so the
			// path args are intentionally empty.
			err := s.tlsSrv.ListenAndServeTLS("", "")
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		}()
	}

	if s.srv == nil && s.tlsSrv == nil {
		return errors.New("httpserver: no listeners configured")
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if s.srv != nil {
			_ = s.srv.Shutdown(shutdownCtx)
		}
		if s.tlsSrv != nil {
			_ = s.tlsSrv.Shutdown(shutdownCtx)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func requestLogger(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Printf("%s %s %d %s", r.Method, r.URL.Path, ww.Status(), time.Since(start))
		})
	}
}

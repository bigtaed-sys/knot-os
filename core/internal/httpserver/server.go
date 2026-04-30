// Package httpserver wires together the HTTP listener that exposes the
// REST API and the embedded SvelteKit UI.
package httpserver

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/knot-os/knot-os/core/internal/web"
)

// Options configures the HTTP server.
type Options struct {
	// Addr is the listen address (e.g. ":80", ":8080").
	Addr string
	// Logger receives structured log lines. If nil, log.Default() is used.
	Logger *log.Logger
}

// Server is the HTTP front door of knotd. It serves:
//   - /api/*  — REST API (mounted by callers via Mount)
//   - /*      — embedded SvelteKit UI with SPA fallback
type Server struct {
	opts   Options
	router chi.Router
	srv    *http.Server
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

	// Health endpoint — always available, never auth-gated, used by readiness
	// probes and CI smoke tests.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	// Static UI — registered last so /api routes mounted by callers take
	// precedence. The handler itself implements SPA fallback.
	r.Handle("/*", web.Handler())

	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	return &Server{opts: opts, router: r, srv: srv}
}

// Mount registers an additional sub-router (typically /api/*).
// Call before Start.
func (s *Server) Mount(pattern string, handler http.Handler) {
	s.router.Mount(pattern, handler)
}

// Start blocks until the server stops. Stop the server by cancelling ctx.
func (s *Server) Start(ctx context.Context) error {
	if !web.IsBuilt() {
		s.opts.Logger.Printf("warning: embedded UI bundle is empty — run scripts/build-ui.sh before building knotd")
	}
	s.opts.Logger.Printf("HTTP listening on %s", s.opts.Addr)

	errCh := make(chan error, 1)
	go func() {
		err := s.srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
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

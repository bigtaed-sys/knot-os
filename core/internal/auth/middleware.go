package auth

import (
	"context"
	"net/http"
)

// CookieName is the name of the session cookie set after login.
const CookieName = "knot_session"

// contextKey is unexported so external callers cannot fish a value out.
type contextKey struct{}

// FromContext returns the session attached to ctx by Middleware, or false
// if the request was unauthenticated.
func FromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(contextKey{}).(Session)
	return s, ok
}

// Middleware returns an HTTP middleware that gates the wrapped handler:
// requests without a valid session cookie receive 401 with a JSON body.
//
// Routes that should be reachable unauthenticated (e.g. /api/auth/login,
// the setup endpoints) must not be wrapped by this middleware — they go
// onto a separate sub-router.
func (s *Sessions) Middleware(unauthorized http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(CookieName)
			if err != nil {
				unauthorized(w, r)
				return
			}
			sess, ok := s.Lookup(cookie.Value)
			if !ok {
				// Stale or forged token. Clear the bad cookie so the
				// browser stops sending it.
				http.SetCookie(w, &http.Cookie{
					Name:     CookieName,
					Value:    "",
					Path:     "/",
					MaxAge:   -1,
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				})
				unauthorized(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), contextKey{}, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

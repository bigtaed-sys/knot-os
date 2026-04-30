package auth

import (
	"sync"
	"time"
)

// SessionTTL is how long a login persists. Sessions live in memory only
// — restarting knotd logs everyone out. That is acceptable given the
// expected usage pattern (rare logins from the LAN) and avoids having
// session state survive bad shutdowns.
const SessionTTL = 24 * time.Hour

// Session is the server-side state behind a session cookie.
type Session struct {
	Token     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Sessions stores active session tokens. The zero value is not usable;
// construct via NewSessions.
type Sessions struct {
	mu      sync.Mutex
	tokens  map[string]Session
	now     func() time.Time
}

// NewSessions returns an empty session store.
func NewSessions() *Sessions {
	return &Sessions{
		tokens: make(map[string]Session),
		now:    time.Now,
	}
}

// Issue creates a fresh session and returns its token. The caller is
// responsible for setting it as a cookie on the response.
func (s *Sessions) Issue() (Session, error) {
	token, err := generateToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	sess := Session{
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(SessionTTL),
	}
	s.mu.Lock()
	s.tokens[token] = sess
	s.mu.Unlock()
	return sess, nil
}

// Lookup returns the session for token, or false if it does not exist or
// has expired. Expired sessions are evicted as a side effect.
func (s *Sessions) Lookup(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.tokens[token]
	if !ok {
		return Session{}, false
	}
	if !now.Before(sess.ExpiresAt) {
		delete(s.tokens, token)
		return Session{}, false
	}
	return sess, true
}

// Revoke removes a session, e.g. on logout. It is safe to call with an
// unknown token.
func (s *Sessions) Revoke(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

// RevokeAll wipes every session. Used when the password changes — any
// existing sessions should no longer be trusted.
func (s *Sessions) RevokeAll() {
	s.mu.Lock()
	s.tokens = make(map[string]Session)
	s.mu.Unlock()
}

// Count returns the number of currently held (not necessarily unexpired)
// sessions. Useful for tests and metrics.
func (s *Sessions) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tokens)
}

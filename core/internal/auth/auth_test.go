package auth

import (
	"testing"
	"time"
)

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := CheckPassword(hash, "correct-horse"); err != nil {
		t.Errorf("correct password failed: %v", err)
	}
	if err := CheckPassword(hash, "wrong"); err == nil {
		t.Error("wrong password accepted")
	}
}

func TestHashRejectsShort(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Error("short password accepted")
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := NewSessions()
	sess, err := s.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("empty token")
	}
	if got, ok := s.Lookup(sess.Token); !ok || got.Token != sess.Token {
		t.Errorf("Lookup after Issue: ok=%v token=%q", ok, got.Token)
	}
	s.Revoke(sess.Token)
	if _, ok := s.Lookup(sess.Token); ok {
		t.Error("Lookup after Revoke should fail")
	}
}

func TestSessionExpires(t *testing.T) {
	s := NewSessions()
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fakeNow }

	sess, err := s.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, ok := s.Lookup(sess.Token); !ok {
		t.Fatal("fresh session should be valid")
	}

	// Jump past expiry.
	s.now = func() time.Time { return fakeNow.Add(SessionTTL + time.Second) }
	if _, ok := s.Lookup(sess.Token); ok {
		t.Error("expired session should not validate")
	}
	if got := s.Count(); got != 0 {
		t.Errorf("expired session should be evicted: count=%d", got)
	}
}

func TestRevokeAll(t *testing.T) {
	s := NewSessions()
	for i := 0; i < 3; i++ {
		if _, err := s.Issue(); err != nil {
			t.Fatalf("Issue: %v", err)
		}
	}
	if s.Count() != 3 {
		t.Fatalf("expected 3 sessions, got %d", s.Count())
	}
	s.RevokeAll()
	if s.Count() != 0 {
		t.Errorf("RevokeAll left %d sessions", s.Count())
	}
}

func TestTokensAreUnique(t *testing.T) {
	s := NewSessions()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		sess, err := s.Issue()
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if seen[sess.Token] {
			t.Fatalf("duplicate token after %d issues", i)
		}
		seen[sess.Token] = true
	}
}

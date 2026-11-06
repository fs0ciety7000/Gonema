package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTest(t *testing.T) (*Service, *clock, *store.Store) {
	t.Helper()
	st := storetest.New(t)
	c := &clock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	s, err := New(Options{Store: st, Now: c.now, Params: FastParams})
	if err != nil {
		t.Fatal(err)
	}
	return s, c, st
}

var ci = ClientInfo{IP: "192.0.2.1", UserAgent: "test"}

func TestPasswordHash(t *testing.T) {
	h, err := hashPassword(FastParams, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("hash = %q", h)
	}
	if ok, err := verifyPassword(h, "correct horse battery"); !ok || err != nil {
		t.Errorf("verify right = %v %v", ok, err)
	}
	if ok, err := verifyPassword(h, "correct horse batterY"); ok || err != nil {
		t.Errorf("verify wrong = %v %v", ok, err)
	}
	h2, _ := hashPassword(FastParams, "correct horse battery")
	if h == h2 {
		t.Error("same salt twice")
	}
	for _, bad := range []string{"", "$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA", "$argon2id$v=19$m=64,t=1$x$y"} {
		if _, err := verifyPassword(bad, "x"); err == nil {
			t.Errorf("verify(%q) accepted", bad)
		}
	}
	// The default parameters are the RFC 9106 ones.
	if DefaultParams.Memory != 64*1024 || DefaultParams.Time != 3 || DefaultParams.Threads != 4 {
		t.Errorf("default params = %+v", DefaultParams)
	}
}

func TestSetupLoginLogout(t *testing.T) {
	s, c, _ := newTest(t)
	ctx := context.Background()
	if need, err := s.NeedsSetup(ctx); !need || err != nil {
		t.Fatalf("needs setup = %v %v", need, err)
	}
	if _, _, _, err := s.Login(ctx, "admin", "whatever-password", ci, ""); !errors.Is(err, ErrNeedsSetup) {
		t.Fatalf("login before setup: %v", err)
	}
	var inv *InvalidError
	if _, err := s.Setup(ctx, "admin", "short", ci); !errors.As(err, &inv) || inv.Field != "password" {
		t.Fatalf("short password: %v", err)
	}
	if _, err := s.Setup(ctx, "  ", "long enough password", ci); !errors.As(err, &inv) || inv.Field != "username" {
		t.Fatalf("empty username: %v", err)
	}
	cr, err := s.Setup(ctx, " Admin ", "long enough password", ci)
	if err != nil {
		t.Fatal(err)
	}
	if cr.User.Username != "Admin" || cr.Token == "" || !strings.HasPrefix(cr.APIKey, APIKeyPrefix) || cr.KeyInfo.Last4 != cr.APIKey[len(cr.APIKey)-4:] {
		t.Fatalf("created = %+v", cr)
	}
	if _, err := s.Setup(ctx, "other", "long enough password", ci); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("second setup: %v", err)
	}
	p, err := s.SessionPrincipal(ctx, cr.Token)
	if err != nil || p.Method != MethodSession || p.UserID != cr.User.ID || p.SessionID != cr.Session.ID {
		t.Fatalf("setup session = %+v %v", p, err)
	}
	if p, err := s.APIKeyPrincipal(ctx, cr.APIKey); err != nil || p.Method != MethodAPIKey {
		t.Fatalf("api key = %+v %v", p, err)
	}
	if _, err := s.APIKeyPrincipal(ctx, cr.APIKey+"x"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("wrong key: %v", err)
	}

	// Login is case-insensitive on the username and rotates the session.
	if _, _, _, err := s.Login(ctx, "admin", "wrong password!", ci, ""); !errors.Is(err, ErrCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, _, err := s.Login(ctx, "nobody", "long enough password", ci, ""); !errors.Is(err, ErrCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	tok, sess, u, err := s.Login(ctx, "ADMIN", "long enough password", ci, cr.Token)
	if err != nil || tok == cr.Token || sess.ID == 0 || u.LastLoginAt == nil {
		t.Fatalf("login = %q %+v %+v %v", tok, sess, u, err)
	}
	if _, err := s.SessionPrincipal(ctx, cr.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("old session still valid after login: %v", err)
	}
	// Idle timeout and absolute expiry.
	c.add(DefaultIdleTimeout + time.Minute)
	if _, err := s.SessionPrincipal(ctx, tok); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("idle session valid: %v", err)
	}
	tok, _, _, _ = s.Login(ctx, "admin", "long enough password", ci, "")
	for range 4 { // used every week: still expires after the absolute TTL
		c.add(7 * 24 * time.Hour)
		if _, err := s.SessionPrincipal(ctx, tok); err != nil {
			t.Fatalf("active session refused: %v", err)
		}
	}
	c.add(3 * 24 * time.Hour)
	if _, err := s.SessionPrincipal(ctx, tok); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("session valid past its TTL: %v", err)
	}
	tok, _, _, _ = s.Login(ctx, "admin", "long enough password", ci, "")
	if err := s.Logout(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionPrincipal(ctx, tok); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("session valid after logout: %v", err)
	}
	if err := s.Logout(ctx, "unknown"); err != nil {
		t.Errorf("logout unknown: %v", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	s, c, _ := newTest(t)
	ctx := context.Background()
	if _, err := s.Setup(ctx, "admin", "long enough password", ci); err != nil {
		t.Fatal(err)
	}
	for i := range ipFailures {
		if _, _, _, err := s.Login(ctx, "admin", "wrong password "+string(rune('a'+i)), ci, ""); !errors.Is(err, ErrCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var rl *RateLimitedError
	// Even the right password is refused while throttled.
	if _, _, _, err := s.Login(ctx, "admin", "long enough password", ci, ""); !errors.As(err, &rl) || rl.RetryAfter <= 0 || rl.RetryAfter > failureWindow {
		t.Fatalf("throttled: %v", err)
	}
	// Another IP is not throttled by the IP limit (the account limit is higher).
	other := ClientInfo{IP: "198.51.100.7"}
	if _, _, _, err := s.Login(ctx, "admin", "long enough password", other, ""); err != nil {
		t.Fatalf("other IP: %v", err)
	}
	// Per account: failures from many IPs.
	for i := range accountFailures {
		_, _, _, _ = s.Login(ctx, "admin", "wrong password!!", ClientInfo{IP: "203.0.113." + string(rune('0'+i%10)) + string(rune('0'+i/10))}, "")
	}
	if _, _, _, err := s.Login(ctx, "admin", "long enough password", ClientInfo{IP: "192.0.2.200"}, ""); !errors.As(err, &rl) {
		t.Fatalf("account throttled: %v", err)
	}
	c.add(failureWindow)
	if _, _, _, err := s.Login(ctx, "admin", "long enough password", ci, ""); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestChangePasswordAndSessions(t *testing.T) {
	s, _, _ := newTest(t)
	ctx := context.Background()
	cr, err := s.Setup(ctx, "admin", "long enough password", ci)
	if err != nil {
		t.Fatal(err)
	}
	other, _, _, err := s.Login(ctx, "admin", "long enough password", ClientInfo{IP: "198.51.100.1", UserAgent: "phone"}, "")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Sessions(ctx, cr.User.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions = %+v %v", list, err)
	}
	p, _ := s.SessionPrincipal(ctx, cr.Token)
	var inv *InvalidError
	if _, _, err := s.ChangePassword(ctx, p, "not the password", "a brand new password", ci); !errors.As(err, &inv) || inv.Field != "currentPassword" {
		t.Fatalf("wrong current: %v", err)
	}
	if _, _, err := s.ChangePassword(ctx, p, "long enough password", "short", ci); !errors.As(err, &inv) || inv.Field != "password" {
		t.Fatalf("short new: %v", err)
	}
	tok, info, err := s.ChangePassword(ctx, p, "long enough password", "a brand new password", ci)
	if err != nil || tok == "" || info.ID == 0 {
		t.Fatalf("change = %q %v", tok, err)
	}
	for _, old := range []string{cr.Token, other} {
		if _, err := s.SessionPrincipal(ctx, old); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("session survived the password change: %v", err)
		}
	}
	if _, err := s.SessionPrincipal(ctx, tok); err != nil {
		t.Errorf("new session: %v", err)
	}
	if _, _, _, err := s.Login(ctx, "admin", "a brand new password", ci, ""); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
	list, _ = s.Sessions(ctx, cr.User.ID)
	if len(list) != 2 {
		t.Fatalf("sessions after = %d", len(list))
	}
	if err := s.RevokeSession(ctx, cr.User.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, cr.User.ID, list[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke twice: %v", err)
	}
	if err := s.RevokeSession(ctx, cr.User.ID+1, list[1].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke of another user's session: %v", err)
	}

	// API key regeneration: the old key stops working.
	key, kinfo, err := s.RegenerateAPIKey(ctx, cr.User.ID)
	if err != nil || key == cr.APIKey || kinfo.Last4 != key[len(key)-4:] {
		t.Fatalf("regenerate = %v", err)
	}
	if _, err := s.APIKeyPrincipal(ctx, cr.APIKey); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("old key works: %v", err)
	}
	if p, err := s.APIKeyPrincipal(ctx, key); err != nil {
		t.Errorf("new key: %v", err)
	} else if tok, _, err := s.ChangePassword(ctx, p, "a brand new password", "yet another password", ci); err != nil || tok != "" {
		t.Errorf("change by API key = %q %v", tok, err)
	}
	if info, ok, err := s.APIKey(ctx, cr.User.ID); !ok || err != nil || info.LastUsedAt == nil {
		t.Errorf("key info = %+v %v %v", info, ok, err)
	}
}

func TestStoredSecretsAreHashed(t *testing.T) {
	s, _, st := newTest(t)
	ctx := context.Background()
	cr, err := s.Setup(ctx, "admin", "long enough password", ci)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := st.Read().GetUser(ctx, cr.User.ID)
	k, _ := st.Read().GetUserAPIKey(ctx, cr.User.ID)
	sess, _ := st.Read().ListUserSessions(ctx, db.ListUserSessionsParams{UserID: cr.User.ID})
	blob := u.PasswordHash + string(k.KeyHash)
	for _, x := range sess {
		blob += string(x.TokenHash)
	}
	for _, secret := range []string{"long enough password", cr.APIKey, cr.Token} {
		if strings.Contains(blob, secret) {
			t.Errorf("stored in clear: %q", secret)
		}
	}
}

func TestLimiterBounded(t *testing.T) {
	l := newLimiter(2, time.Minute)
	now := time.Now()
	for i := range maxKeys + 10 {
		l.fail(string(rune(i)), now)
	}
	if len(l.hits) > maxKeys {
		t.Errorf("%d keys", len(l.hits))
	}
	l.fail("a", now)
	l.fail("a", now.Add(time.Second))
	if w := l.wait("a", now.Add(2*time.Second)); w != 58*time.Second {
		t.Errorf("wait = %s", w)
	}
	if w := l.wait("a", now.Add(61*time.Second)); w != 0 {
		t.Errorf("wait after = %s", w)
	}
}

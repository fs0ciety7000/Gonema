// Package auth is the authentication of Gonema: one admin account created at
// first run, browser sessions stored server-side, and an API key.
//
//   - Passwords are hashed with argon2id (PHC string, parameters in the hash),
//     compared in constant time; an unknown username costs the same hash.
//   - Session tokens and the API key are 256-bit random values; only their
//     SHA-256 is stored, so a stolen database copy holds no usable credential.
//   - A login always creates a new session (rotation) and ends the one the
//     client presented; a password change ends every other session.
//   - Failed logins are throttled per client IP and per account.
//
// The package knows nothing of HTTP: package api reads cookies and headers.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Defaults.
const (
	DefaultSessionTTL  = 30 * 24 * time.Hour // absolute lifetime of a session
	DefaultIdleTimeout = 14 * 24 * time.Hour // a session unused this long ends
	// touchEvery throttles the writes of last_seen_at / last_used_at.
	touchEvery = 5 * time.Minute

	// Login throttling: failures allowed per window.
	ipFailures      = 10
	accountFailures = 20
	failureWindow   = 15 * time.Minute
	// trustedIPFor: an address that signed in successfully this recently is
	// exempt from the per-account limit, so that an attacker hammering the
	// account cannot lock the admin out (the per-address limit still applies).
	trustedIPFor = DefaultSessionTTL

	// Argon2 computations: 2 at a time, at most 8 callers waiting up to 3 s
	// for a slot; beyond that, ErrBusy (429).
	hashSlots   = 2
	hashWaiters = 8
	hashWaitMax = 3 * time.Second

	// APIKeyPrefix starts every API key, so it is recognizable in a config
	// file or a secret scanner.
	APIKeyPrefix = "gnm_"
)

// Errors. Messages are plain sentences safe to show.
var (
	ErrNeedsSetup  = errors.New("no account exists yet: create it first")
	ErrSetupDone   = errors.New("the account already exists")
	ErrCredentials = errors.New("wrong username or password")
	// ErrUnauthenticated: no valid session or API key.
	ErrUnauthenticated = errors.New("not signed in")
	ErrNotFound        = errors.New("not found")
	// ErrBusy: too many password checks at once; retry in a moment.
	ErrBusy = errors.New("the server is busy checking other sign-ins: try again in a moment")
)

// BusyRetry is the Retry-After advised with ErrBusy.
const BusyRetry = 2 * time.Second

// InvalidError is a rejected input; Field names it.
type InvalidError struct{ Field, Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// RateLimitedError reports too many failures; retry after RetryAfter.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("too many failed attempts: try again in %d seconds", int(e.RetryAfter.Seconds())+1)
}

// Method is how a request was authenticated.
type Method string

// Authentication methods.
const (
	MethodSession Method = "session"
	MethodAPIKey  Method = "apikey"
	MethodLocal   Method = "local" // GONEMA_AUTH=disabled_for_local, request from a local address
)

// Principal is an authenticated caller.
type Principal struct {
	UserID    int64 // 0 for MethodLocal without an account
	Username  string
	Method    Method
	SessionID int64 // MethodSession only
}

// ClientInfo describes the client of a login (shown in the session list).
type ClientInfo struct {
	IP        string
	UserAgent string
}

// User is the account, without its hash.
type User struct {
	ID          int64
	Username    string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// SessionInfo is a session as listed (never its token).
type SessionInfo struct {
	ID         int64
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
}

// APIKeyInfo describes the API key without revealing it.
type APIKeyInfo struct {
	Last4      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Options configures the service.
type Options struct {
	Store       *store.Store
	Logger      *slog.Logger
	Now         func() time.Time
	Params      Params        // zero: DefaultParams
	SessionTTL  time.Duration // zero: DefaultSessionTTL
	IdleTimeout time.Duration // zero: DefaultIdleTimeout
}

// Service authenticates callers. Safe for concurrent use.
type Service struct {
	st     *store.Store
	log    *slog.Logger
	now    func() time.Time
	params Params
	ttl    time.Duration
	idle   time.Duration

	gate      *hashGate
	byIP      *limiter
	byAccount *limiter
	dummy     func() string // hash compared when the username is unknown

	goodMu  sync.Mutex
	goodIPs map[string]time.Time // last successful sign-in per client address

	setupMu    sync.Mutex
	tokenMu    sync.Mutex
	setupToken string // one-time setup token while no account exists
}

// New returns a service. It does no I/O (startup stays fast).
func New(o Options) (*Service, error) {
	if o.Store == nil {
		return nil, errors.New("auth: store is required")
	}
	s := &Service{
		st: o.Store, log: o.Logger, now: o.Now, params: o.Params, ttl: o.SessionTTL, idle: o.IdleTimeout,
		gate: newHashGate(hashSlots, hashWaiters, hashWaitMax), goodIPs: make(map[string]time.Time), byIP: newLimiter(ipFailures, failureWindow), byAccount: newLimiter(accountFailures, failureWindow),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.params == (Params{}) {
		s.params = DefaultParams
	}
	if s.ttl <= 0 {
		s.ttl = DefaultSessionTTL
	}
	if s.idle <= 0 {
		s.idle = DefaultIdleTimeout
	}
	s.dummy = sync.OnceValue(func() string {
		h, err := hashPassword(s.params, "gonema-dummy-password")
		if err != nil {
			return "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$" + strings.Repeat("A", 22)
		}
		return h
	})
	return s, nil
}

// SessionTTL is the absolute lifetime of a new session (the cookie Max-Age).
func (s *Service) SessionTTL() time.Duration { return s.ttl }

// ----------------------------------------------------------------- setup

// NeedsSetup reports whether no account exists yet.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := s.st.Read().CountUsers(ctx)
	if err != nil {
		return false, store.MapError(fmt.Errorf("auth: count users: %w", err))
	}
	return n == 0, nil
}

// Created is the result of Setup: the session of the new account and its API
// key, shown once.
type Created struct {
	User    User
	Token   string
	Session SessionInfo
	APIKey  string
	KeyInfo APIKeyInfo
}

// Setup creates the admin account, its first session and its API key. It
// fails with ErrSetupDone once an account exists.
func (s *Service) Setup(ctx context.Context, username, password string, ci ClientInfo) (*Created, error) {
	username, err := validUsername(username)
	if err != nil {
		return nil, err
	}
	if err := validPassword(password, username); err != nil {
		return nil, err
	}
	s.setupMu.Lock() // one hash at a time here; the transaction is the real guard
	defer s.setupMu.Unlock()
	if need, err := s.NeedsSetup(ctx); err != nil {
		return nil, err
	} else if !need {
		return nil, ErrSetupDone
	}
	var hash string
	if err := s.gate.run(ctx, func() { hash, err = hashPassword(s.params, password) }); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	token, tokenHash := newSecret("")
	key, keyHash := newSecret(APIKeyPrefix)
	now := s.now()
	out := &Created{Token: token, APIKey: key}
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrSetupDone
		}
		u, err := q.CreateUser(ctx, db.CreateUserParams{Username: username, PasswordHash: hash,
			CreatedAt: store.Millis(now), PasswordChangedAt: store.Millis(now)})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if err := q.SetUserLastLogin(ctx, db.SetUserLastLoginParams{LastLoginAt: ptr(store.Millis(now)), ID: u.ID}); err != nil {
			return err
		}
		sess, err := q.CreateSession(ctx, s.sessionParams(tokenHash, u.ID, now, ci))
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		k, err := q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: u.ID, KeyHash: keyHash, Last4: last4(key), CreatedAt: store.Millis(now)})
		if err != nil {
			return fmt.Errorf("create API key: %w", err)
		}
		u.LastLoginAt = ptr(store.Millis(now))
		out.User, out.Session, out.KeyInfo = userOf(&u), sessionOf(&sess), keyInfoOf(&k)
		return nil
	})
	if errors.Is(err, ErrSetupDone) {
		return nil, ErrSetupDone
	}
	if err != nil {
		return nil, fmt.Errorf("auth: setup: %w", err)
	}
	s.markGood(ci.IP, now)
	s.tokenMu.Lock()
	s.setupToken = ""
	s.tokenMu.Unlock()
	s.log.Info("admin account created")
	return out, nil
}

// ----------------------------------------------------------------- login

// Login checks the credentials and opens a new session. oldToken, the session
// the client presented if any, is ended (session rotation).
//
// Throttling: the attempt is reserved in the per-address and per-account
// limiters before the password is hashed, so concurrent attempts cannot exceed
// the limits. An address that signed in successfully recently is exempt from
// the per-account limit (an attacker cannot lock the admin out).
func (s *Service) Login(ctx context.Context, username, password string, ci ClientInfo, oldToken string) (string, SessionInfo, User, error) {
	now := s.now()
	username = strings.TrimSpace(username)
	acct := "u:" + strings.ToLower(username)
	ipKey := "ip:" + ci.IP
	if w := s.byIP.reserve(ipKey, now); w > 0 {
		return "", SessionInfo{}, User{}, &RateLimitedError{RetryAfter: w}
	}
	result := aborted
	useAcct := !s.isGood(ci.IP, now)
	if useAcct {
		if w := s.byAccount.reserve(acct, now); w > 0 {
			s.byIP.end(ipKey, now, aborted)
			return "", SessionInfo{}, User{}, &RateLimitedError{RetryAfter: w}
		}
	}
	defer func() {
		s.byIP.end(ipKey, now, result)
		if useAcct {
			s.byAccount.end(acct, now, result)
		} else if result == succeeded {
			s.byAccount.reset(acct)
		}
	}()
	if len(password) > MaxPasswordLen || len(username) > 256 {
		result = failed
		return "", SessionInfo{}, User{}, ErrCredentials
	}
	if need, err := s.NeedsSetup(ctx); err != nil {
		return "", SessionInfo{}, User{}, err
	} else if need {
		return "", SessionInfo{}, User{}, ErrNeedsSetup
	}
	u, err := s.st.Read().GetUserByUsername(ctx, username)
	known := err == nil
	if err != nil && !errors.Is(store.MapError(err), store.ErrNotFound) {
		return "", SessionInfo{}, User{}, store.MapError(fmt.Errorf("auth: read user: %w", err))
	}
	encoded := u.PasswordHash
	if !known {
		encoded = s.dummy() // same cost whether the username exists or not
	}
	var ok bool
	var verr error
	if err := s.gate.run(ctx, func() { ok, verr = verifyPassword(encoded, password) }); err != nil {
		return "", SessionInfo{}, User{}, err
	}
	if verr != nil {
		s.log.Error("stored password hash is unreadable", slog.Int64("user_id", u.ID), slog.Any("error", verr))
	}
	if !known || !ok || verr != nil {
		result = failed
		// The client address is logged on purpose (fail2ban and the like); the
		// username typed is not (it may be a password typed in the wrong field).
		s.log.Warn("failed login", slog.String("ip", ci.IP))
		return "", SessionInfo{}, User{}, ErrCredentials
	}
	result = succeeded
	s.markGood(ci.IP, now)

	token, tokenHash := newSecret("")
	var sess db.Session
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if oldToken != "" {
			if old, err := q.GetSessionByToken(ctx, hashOf(oldToken)); err == nil {
				if _, err := q.DeleteSession(ctx, old.Session.ID); err != nil {
					return err
				}
			}
		}
		if _, err := q.DeleteExpiredSessions(ctx, s.expiredParams(now)); err != nil {
			return err
		}
		var err error
		if sess, err = q.CreateSession(ctx, s.sessionParams(tokenHash, u.ID, now, ci)); err != nil {
			return err
		}
		return q.SetUserLastLogin(ctx, db.SetUserLastLoginParams{LastLoginAt: ptr(store.Millis(now)), ID: u.ID})
	})
	if err != nil {
		return "", SessionInfo{}, User{}, fmt.Errorf("auth: open session: %w", err)
	}
	u.LastLoginAt = ptr(store.Millis(now))
	s.log.Info("signed in", slog.Int64("user_id", u.ID), slog.Int64("session_id", sess.ID))
	return token, sessionOf(&sess), userOf(&u), nil
}

// markGood records a successful sign-in from ip.
func (s *Service) markGood(ip string, now time.Time) {
	if ip == "" {
		return
	}
	s.goodMu.Lock()
	defer s.goodMu.Unlock()
	if len(s.goodIPs) >= maxKeys {
		for k, t := range s.goodIPs {
			if now.Sub(t) >= trustedIPFor || len(s.goodIPs) >= maxKeys {
				delete(s.goodIPs, k)
			}
		}
	}
	s.goodIPs[ip] = now
}

// isGood reports whether ip signed in successfully recently.
func (s *Service) isGood(ip string, now time.Time) bool {
	if ip == "" {
		return false
	}
	s.goodMu.Lock()
	defer s.goodMu.Unlock()
	t, ok := s.goodIPs[ip]
	return ok && now.Sub(t) < trustedIPFor
}

// Logout ends the session of token (no error when it does not exist).
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	row, err := s.st.Read().GetSessionByToken(ctx, hashOf(token))
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return store.MapError(fmt.Errorf("auth: read session: %w", err))
	}
	if _, err := s.st.Write().DeleteSession(ctx, row.Session.ID); err != nil {
		return store.MapError(fmt.Errorf("auth: delete session: %w", err))
	}
	return nil
}

// ----------------------------------------------------------------- authenticate

// SessionPrincipal returns the caller of a session token, ErrUnauthenticated
// when it is unknown, expired or idle too long.
func (s *Service) SessionPrincipal(ctx context.Context, token string) (Principal, error) {
	if token == "" || len(token) > 128 {
		return Principal{}, ErrUnauthenticated
	}
	row, err := s.st.Read().GetSessionByToken(ctx, hashOf(token))
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, store.MapError(fmt.Errorf("auth: read session: %w", err))
	}
	now := s.now()
	ms := store.Millis(now)
	if row.Session.ExpiresAt <= ms || row.Session.LastSeenAt <= store.Millis(now.Add(-s.idle)) {
		return Principal{}, ErrUnauthenticated
	}
	if ms-row.Session.LastSeenAt >= touchEvery.Milliseconds() {
		if err := s.st.Write().TouchSession(ctx, db.TouchSessionParams{LastSeenAt: ms, ID: row.Session.ID}); err != nil {
			s.log.Warn("cannot record session activity", slog.Any("error", err))
		}
	}
	return Principal{UserID: row.User.ID, Username: row.User.Username, Method: MethodSession, SessionID: row.Session.ID}, nil
}

// APIKeyPrincipal returns the owner of an API key, ErrUnauthenticated when
// the key is unknown.
func (s *Service) APIKeyPrincipal(ctx context.Context, key string) (Principal, error) {
	if !strings.HasPrefix(key, APIKeyPrefix) || len(key) > 128 {
		return Principal{}, ErrUnauthenticated
	}
	row, err := s.st.Read().GetAPIKeyByHash(ctx, hashOf(key))
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, store.MapError(fmt.Errorf("auth: read API key: %w", err))
	}
	now := store.Millis(s.now())
	if last := row.ApiKey.LastUsedAt; last == nil || now-*last >= touchEvery.Milliseconds() {
		if err := s.st.Write().TouchAPIKey(ctx, db.TouchAPIKeyParams{LastUsedAt: &now, ID: row.ApiKey.ID}); err != nil {
			s.log.Warn("cannot record API key use", slog.Any("error", err))
		}
	}
	return Principal{UserID: row.User.ID, Username: row.User.Username, Method: MethodAPIKey}, nil
}

// ----------------------------------------------------------------- account

// User returns the account.
func (s *Service) User(ctx context.Context, id int64) (User, error) {
	u, err := s.st.Read().GetUser(ctx, id)
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, store.MapError(fmt.Errorf("auth: read user: %w", err))
	}
	return userOf(&u), nil
}

// Admin returns the account; ok is false before setup.
func (s *Service) Admin(ctx context.Context) (User, bool, error) {
	u, err := s.st.Read().GetFirstUser(ctx)
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, store.MapError(fmt.Errorf("auth: read user: %w", err))
	}
	return userOf(&u), true, nil
}

// PasswordChange is the outcome of ChangePassword.
type PasswordChange struct {
	// Token and Session are the new session of a session caller (it stays
	// signed in with a fresh token); empty otherwise.
	Token   string
	Session SessionInfo
	// APIKey is the new API key when it was regenerated (shown once).
	APIKey  string
	KeyInfo APIKeyInfo
}

// ChangePassword checks current, stores next and ends every session of the
// user. For a session caller a new session is opened and its token returned.
// With regenerateKey, the API key is replaced in the same transaction (the
// old one stops working) and the new one returned.
func (s *Service) ChangePassword(ctx context.Context, p Principal, current, next string, ci ClientInfo, regenerateKey bool) (PasswordChange, error) {
	if p.UserID == 0 {
		return PasswordChange{}, ErrNeedsSetup
	}
	now := s.now()
	acct := "pw:" + fmt.Sprint(p.UserID)
	if w := s.byAccount.reserve(acct, now); w > 0 {
		return PasswordChange{}, &RateLimitedError{RetryAfter: w}
	}
	result := aborted
	defer func() { s.byAccount.end(acct, now, result) }()
	u, err := s.st.Read().GetUser(ctx, p.UserID)
	if err != nil {
		return PasswordChange{}, store.MapError(fmt.Errorf("auth: read user: %w", err))
	}
	wrong := &InvalidError{Field: "currentPassword", Msg: "The current password is not correct."}
	if len(current) > MaxPasswordLen {
		result = failed
		return PasswordChange{}, wrong
	}
	var ok bool
	var verr error
	if err := s.gate.run(ctx, func() { ok, verr = verifyPassword(u.PasswordHash, current) }); err != nil {
		return PasswordChange{}, err
	}
	if verr != nil || !ok {
		result = failed
		return PasswordChange{}, wrong
	}
	if err := validPassword(next, u.Username); err != nil {
		return PasswordChange{}, err
	}
	if next == current {
		return PasswordChange{}, &InvalidError{Field: "newPassword", Msg: "The new password must differ from the current one."}
	}
	var hash string
	if err := s.gate.run(ctx, func() { hash, err = hashPassword(s.params, next) }); err != nil {
		return PasswordChange{}, err
	}
	if err != nil {
		return PasswordChange{}, err
	}
	var out PasswordChange
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.SetUserPassword(ctx, db.SetUserPasswordParams{PasswordHash: hash, ChangedAt: store.Millis(now), ID: u.ID}); err != nil {
			return err
		}
		if _, err := q.DeleteUserSessionsExcept(ctx, db.DeleteUserSessionsExceptParams{UserID: u.ID, KeepID: 0}); err != nil {
			return err
		}
		if regenerateKey {
			key, h := newSecret(APIKeyPrefix)
			if _, err := q.DeleteUserAPIKey(ctx, u.ID); err != nil {
				return err
			}
			k, err := q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: u.ID, KeyHash: h, Last4: last4(key), CreatedAt: store.Millis(now)})
			if err != nil {
				return err
			}
			out.APIKey, out.KeyInfo = key, keyInfoOf(&k)
		}
		if p.Method != MethodSession {
			return nil
		}
		var h []byte
		out.Token, h = newSecret("")
		sess, err := q.CreateSession(ctx, s.sessionParams(h, u.ID, now, ci))
		out.Session = sessionOf(&sess)
		return err
	})
	if err != nil {
		return PasswordChange{}, fmt.Errorf("auth: change password: %w", err)
	}
	result = succeeded
	s.log.Info("password changed; every other session ended", slog.Int64("user_id", u.ID), slog.Bool("api_key_regenerated", regenerateKey))
	return out, nil
}

// ----------------------------------------------------------------- setup token

// SetupToken returns the one-time setup token, created on first use, while no
// account exists ("" once it exists). It is meant to be printed in the server
// logs: whoever creates the account remotely must have read them. Setup
// clears it.
func (s *Service) SetupToken(ctx context.Context) (string, error) {
	need, err := s.NeedsSetup(ctx)
	if err != nil || !need {
		return "", err
	}
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if s.setupToken == "" {
		s.setupToken = newSetupToken()
	}
	return s.setupToken, nil
}

// CheckSetupToken reports whether tok is the current setup token (case,
// spaces and dashes ignored; constant-time comparison).
func (s *Service) CheckSetupToken(tok string) bool {
	s.tokenMu.Lock()
	want := s.setupToken
	s.tokenMu.Unlock()
	if want == "" {
		return false
	}
	norm := func(x string) []byte {
		return []byte(strings.Map(func(r rune) rune {
			switch {
			case r == '-' || unicode.IsSpace(r):
				return -1
			case r >= 'a' && r <= 'z':
				return r - 'a' + 'A'
			}
			return r
		}, x))
	}
	got := norm(tok)
	if len(got) > 64 {
		return false
	}
	return subtle.ConstantTimeCompare(got, norm(want)) == 1
}

// newSetupToken returns 120 random bits as 24 base32 characters in groups of
// four (easy to copy from a log line).
func newSetupToken() string {
	var b [15]byte
	_, _ = rand.Read(b[:])
	raw := base32.StdEncoding.EncodeToString(b[:])
	var out strings.Builder
	for i := 0; i < len(raw); i += 4 {
		if i > 0 {
			out.WriteByte('-')
		}
		out.WriteString(raw[i : i+4])
	}
	return out.String()
}

// Sessions lists the live sessions of the user, most recently used first.
func (s *Service) Sessions(ctx context.Context, userID int64) ([]SessionInfo, error) {
	rows, err := s.st.Read().ListUserSessions(ctx, db.ListUserSessionsParams{UserID: userID, Now: store.Millis(s.now())})
	if err != nil {
		return nil, store.MapError(fmt.Errorf("auth: list sessions: %w", err))
	}
	idleBefore := store.Millis(s.now().Add(-s.idle))
	out := make([]SessionInfo, 0, len(rows))
	for i := range rows {
		if rows[i].LastSeenAt <= idleBefore {
			continue
		}
		out = append(out, sessionOf(&rows[i]))
	}
	return out, nil
}

// RevokeSession ends one session of the user.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID int64) error {
	n, err := s.st.Write().DeleteUserSession(ctx, db.DeleteUserSessionParams{ID: sessionID, UserID: userID})
	if err != nil {
		return store.MapError(fmt.Errorf("auth: revoke session: %w", err))
	}
	if n == 0 {
		return ErrNotFound
	}
	s.log.Info("session revoked", slog.Int64("user_id", userID), slog.Int64("session_id", sessionID))
	return nil
}

// APIKey describes the API key of the user; ok is false when there is none.
func (s *Service) APIKey(ctx context.Context, userID int64) (APIKeyInfo, bool, error) {
	k, err := s.st.Read().GetUserAPIKey(ctx, userID)
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return APIKeyInfo{}, false, nil
	}
	if err != nil {
		return APIKeyInfo{}, false, store.MapError(fmt.Errorf("auth: read API key: %w", err))
	}
	return keyInfoOf(&k), true, nil
}

// RegenerateAPIKey replaces the API key of the user and returns the new key,
// shown once. The previous key stops working at once.
func (s *Service) RegenerateAPIKey(ctx context.Context, userID int64) (string, APIKeyInfo, error) {
	if userID == 0 {
		return "", APIKeyInfo{}, ErrNeedsSetup
	}
	key, h := newSecret(APIKeyPrefix)
	var info APIKeyInfo
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.DeleteUserAPIKey(ctx, userID); err != nil {
			return err
		}
		k, err := q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: userID, KeyHash: h, Last4: last4(key), CreatedAt: store.Millis(s.now())})
		info = keyInfoOf(&k)
		return err
	})
	if err != nil {
		return "", APIKeyInfo{}, fmt.Errorf("auth: regenerate API key: %w", err)
	}
	s.log.Info("API key regenerated", slog.Int64("user_id", userID))
	return key, info, nil
}

// ----------------------------------------------------------------- helpers

func (s *Service) sessionParams(tokenHash []byte, userID int64, now time.Time, ci ClientInfo) db.CreateSessionParams {
	return db.CreateSessionParams{
		TokenHash: tokenHash, UserID: userID, CreatedAt: store.Millis(now), LastSeenAt: store.Millis(now),
		ExpiresAt: store.Millis(now.Add(s.ttl)), Ip: clip(ci.IP, 64), UserAgent: clip(ci.UserAgent, 256),
	}
}

func (s *Service) expiredParams(now time.Time) db.DeleteExpiredSessionsParams {
	return db.DeleteExpiredSessionsParams{Now: store.Millis(now), IdleBefore: store.Millis(now.Add(-s.idle))}
}

// newSecret returns a random 256-bit token (base64url, with prefix) and its
// SHA-256.
func newSecret(prefix string) (token string, sum []byte) {
	var b [32]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+)
	tok := prefix + base64.RawURLEncoding.EncodeToString(b[:])
	return tok, hashOf(tok)
}

func hashOf(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func last4(key string) string { return key[len(key)-4:] }

func validUsername(u string) (string, error) {
	u = strings.TrimSpace(u)
	if n := utf8.RuneCountInString(u); n < 1 || n > 64 || !utf8.ValidString(u) {
		return "", &InvalidError{Field: "username", Msg: "The username must be 1 to 64 characters long."}
	}
	for _, r := range u {
		if unicode.IsControl(r) {
			return "", &InvalidError{Field: "username", Msg: "The username cannot contain control characters."}
		}
	}
	return u, nil
}

func validPassword(p, username string) error {
	switch n := utf8.RuneCountInString(p); {
	case n < MinPasswordLen:
		return &InvalidError{Field: "password", Msg: fmt.Sprintf("The password must be at least %d characters long.", MinPasswordLen)}
	case len(p) > MaxPasswordLen:
		return &InvalidError{Field: "password", Msg: fmt.Sprintf("The password must be at most %d bytes long.", MaxPasswordLen)}
	case strings.EqualFold(p, username):
		return &InvalidError{Field: "password", Msg: "The password cannot be the username."}
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func ptr[T any](v T) *T { return &v }

func userOf(u *db.User) User {
	out := User{ID: u.ID, Username: u.Username, CreatedAt: store.FromMillis(u.CreatedAt)}
	if u.LastLoginAt != nil {
		t := store.FromMillis(*u.LastLoginAt)
		out.LastLoginAt = &t
	}
	return out
}

func sessionOf(s *db.Session) SessionInfo {
	return SessionInfo{ID: s.ID, CreatedAt: store.FromMillis(s.CreatedAt), LastSeenAt: store.FromMillis(s.LastSeenAt),
		ExpiresAt: store.FromMillis(s.ExpiresAt), IP: s.Ip, UserAgent: s.UserAgent}
}

func keyInfoOf(k *db.ApiKey) APIKeyInfo {
	out := APIKeyInfo{Last4: k.Last4, CreatedAt: store.FromMillis(k.CreatedAt)}
	if k.LastUsedAt != nil {
		t := store.FromMillis(*k.LastUsedAt)
		out.LastUsedAt = &t
	}
	return out
}

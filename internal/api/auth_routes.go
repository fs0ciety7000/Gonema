package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/auth"
	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/netx"
)

// ----------------------------------------------------------------- DTOs

// AuthStatus tells the web interface what to show: the setup screen, the
// login screen or the app.
type AuthStatus struct {
	SetupRequired bool   `json:"setupRequired" doc:"No account exists yet: show the account creation screen (POST /auth/setup)"`
	Authenticated bool   `json:"authenticated" doc:"The caller may use the API"`
	Method        string `json:"method" enum:"session,apikey,local,none" doc:"How the caller is authenticated; local = GONEMA_AUTH=disabled_for_local and a local address"`
	Username      string `json:"username,omitempty"`
	AuthMode      string `json:"authMode" enum:"enabled,disabled_for_local"`
}

// AuthUser is the account.
type AuthUser struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
}

// Credentials are a username and a password. Lengths are checked by the server
// (not by the schema, so that a refused password is never echoed in an error).
type Credentials struct {
	Username string `json:"username" maxLength:"256"`
	Password string `json:"password"`
}

// SetupResult is the new account, signed in, with its API key shown once.
type SetupResult struct {
	User        AuthUser  `json:"user"`
	APIKey      string    `json:"apiKey" doc:"The API key, shown only now: store it in the app that needs it"`
	APIKeyLast4 string    `json:"apiKeyLast4"`
	ExpiresAt   time.Time `json:"expiresAt" doc:"End of the session that was opened"`
}

// LoginResult is the signed-in account.
type LoginResult struct {
	User      AuthUser  `json:"user"`
	ExpiresAt time.Time `json:"expiresAt" doc:"End of the session (absolute); it also ends after 14 days without use"`
}

// PasswordChange changes the password.
type PasswordChange struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword" doc:"At least 10 characters"`
}

// PasswordChanged reports a password change.
type PasswordChanged struct {
	SessionsRevoked bool `json:"sessionsRevoked" doc:"Every other session was ended; the caller's session was renewed"`
}

// APIKeyCreated is a new API key, shown once.
//
//nolint:revive // stutters, but it is the OpenAPI schema name
type APIKeyCreated struct {
	APIKey    string    `json:"apiKey" doc:"Shown only now; the previous key no longer works"`
	Last4     string    `json:"last4"`
	CreatedAt time.Time `json:"createdAt"`
}

// APIKeyState describes the API key without revealing it.
//
//nolint:revive // stutters, but it is the OpenAPI schema name
type APIKeyState struct {
	Present    bool       `json:"present"`
	Last4      string     `json:"last4,omitempty"`
	CreatedAt  *time.Time `json:"createdAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// AuthSession is a signed-in browser.
type AuthSession struct {
	ID         int64     `json:"id"`
	Current    bool      `json:"current" doc:"The session of this request"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt" doc:"Updated at most every 5 minutes"`
	ExpiresAt  time.Time `json:"expiresAt"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"userAgent"`
}

// OutboundPolicy is the policy of the outgoing connections (package netx).
type OutboundPolicy struct {
	Integration   netx.Policy `json:"integration" doc:"Indexers, download clients, media servers, ntfy, Gotify, email"`
	Webhook       netx.Policy `json:"webhook" doc:"Webhooks, Discord, Slack"`
	AlwaysBlocked []string    `json:"alwaysBlocked" doc:"Destinations never contacted, whatever the policy"`
}

// SecuritySettings is Settings > Security. Startup options are read-only.
type SecuritySettings struct {
	AuthMode       string         `json:"authMode" enum:"enabled,disabled_for_local" doc:"GONEMA_AUTH"`
	CookieSecure   string         `json:"cookieSecure" enum:"auto,true,false" doc:"GONEMA_COOKIE_SECURE"`
	TrustedOrigins []string       `json:"trustedOrigins" doc:"GONEMA_TRUSTED_ORIGINS"`
	Method         string         `json:"method" enum:"session,apikey,local" doc:"How this request is authenticated"`
	User           *AuthUser      `json:"user" doc:"null before setup (local mode only)"`
	Sessions       []AuthSession  `json:"sessions"`
	APIKey         APIKeyState    `json:"apiKey"`
	Outbound       OutboundPolicy `json:"outbound"`
}

type setCookieOutput[T any] struct {
	SetCookie    string `header:"Set-Cookie"`
	CacheControl string `header:"Cache-Control"`
	Body         T
}

type noContentCookieOutput struct {
	SetCookie    string `header:"Set-Cookie"`
	CacheControl string `header:"Cache-Control"`
}

type noStore[T any] struct {
	CacheControl string `header:"Cache-Control"`
	Body         T
}

const authBodyLimit = 4 << 10

// ----------------------------------------------------------------- routes

func (s *Server) authRoutes() {
	tags := []string{"Auth"}
	huma.Register(s.api, huma.Operation{
		OperationID: "get-auth-status", Method: http.MethodGet, Path: "/auth/status", Tags: tags,
		Summary:     "Setup and sign-in state",
		Description: "Public. setupRequired: no account yet. authenticated: the cookie, the API key or the local mode lets the caller in.",
	}, func(ctx context.Context, _ *struct{}) (*noStore[AuthStatus], error) {
		a, err := s.authService()
		if err != nil {
			return nil, err
		}
		need, err := a.NeedsSetup(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		st := AuthStatus{SetupRequired: need, Method: "none", AuthMode: s.authMode()}
		if p, ok := PrincipalOf(ctx); ok {
			st.Authenticated, st.Method, st.Username = true, string(p.Method), p.Username
		}
		return &noStore[AuthStatus]{CacheControl: "no-store", Body: st}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "auth-setup", Method: http.MethodPost, Path: "/auth/setup", Tags: tags, DefaultStatus: http.StatusCreated,
		MaxBodyBytes: authBodyLimit,
		Summary:      "Create the admin account (first run only)",
		Description: "Public while no account exists, 409 afterwards. Signs the caller in (session cookie) and returns the API key, " +
			"shown only once. Password: 10 to 256 characters.",
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *struct{ Body Credentials }) (*setCookieOutput[SetupResult], error) {
		a, err := s.authService()
		if err != nil {
			return nil, err
		}
		c := clientOf(ctx)
		cr, err := a.Setup(ctx, in.Body.Username, in.Body.Password, auth.ClientInfo{IP: c.IP, UserAgent: c.UserAgent})
		if err != nil {
			return nil, s.authProblem(ctx, err)
		}
		return &setCookieOutput[SetupResult]{
			SetCookie: s.sessionCookie(c, cr.Token, a.SessionTTL()), CacheControl: "no-store",
			Body: SetupResult{User: authUserOf(cr.User), APIKey: cr.APIKey, APIKeyLast4: cr.KeyInfo.Last4, ExpiresAt: cr.Session.ExpiresAt},
		}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "auth-login", Method: http.MethodPost, Path: "/auth/login", Tags: tags, MaxBodyBytes: authBodyLimit,
		Summary: "Sign in",
		Description: "Public. Opens a new session (HttpOnly, SameSite=Lax cookie; Secure over HTTPS) and ends the one the browser presented. " +
			"Failed attempts are throttled per address and per account: 429 with Retry-After.",
		Errors: []int{http.StatusUnauthorized, http.StatusConflict, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *struct{ Body Credentials }) (*setCookieOutput[LoginResult], error) {
		a, err := s.authService()
		if err != nil {
			return nil, err
		}
		c := clientOf(ctx)
		token, sess, u, err := a.Login(ctx, in.Body.Username, in.Body.Password, auth.ClientInfo{IP: c.IP, UserAgent: c.UserAgent}, c.Token)
		if err != nil {
			return nil, s.authProblem(ctx, err)
		}
		return &setCookieOutput[LoginResult]{
			SetCookie: s.sessionCookie(c, token, a.SessionTTL()), CacheControl: "no-store",
			Body: LoginResult{User: authUserOf(u), ExpiresAt: sess.ExpiresAt},
		}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "auth-logout", Method: http.MethodPost, Path: "/auth/logout", Tags: tags, DefaultStatus: http.StatusNoContent,
		Summary: "Sign out", Description: "Public. Ends the session of the cookie (if any) and clears the cookie.",
	}, func(ctx context.Context, _ *struct{}) (*noContentCookieOutput, error) {
		a, err := s.authService()
		if err != nil {
			return nil, err
		}
		c := clientOf(ctx)
		if err := a.Logout(ctx, c.Token); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &noContentCookieOutput{SetCookie: s.sessionCookie(c, "", 0), CacheControl: "no-store"}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "auth-change-password", Method: http.MethodPut, Path: "/auth/password", Tags: tags, MaxBodyBytes: authBodyLimit,
		Summary:     "Change the password",
		Description: "Requires the current password. Ends every other session; a browser caller gets a fresh session cookie.",
		Errors:      []int{http.StatusUnprocessableEntity, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *struct{ Body PasswordChange }) (*setCookieOutput[PasswordChanged], error) {
		a, p, err := s.caller(ctx)
		if err != nil {
			return nil, err
		}
		c := clientOf(ctx)
		token, _, err := a.ChangePassword(ctx, p, in.Body.CurrentPassword, in.Body.NewPassword, auth.ClientInfo{IP: c.IP, UserAgent: c.UserAgent})
		if err != nil {
			return nil, s.authProblem(ctx, err)
		}
		out := &setCookieOutput[PasswordChanged]{CacheControl: "no-store", Body: PasswordChanged{SessionsRevoked: true}}
		if token != "" {
			out.SetCookie = s.sessionCookie(c, token, a.SessionTTL())
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "auth-regenerate-api-key", Method: http.MethodPost, Path: "/auth/api-key", Tags: tags,
		Summary:     "Generate a new API key",
		Description: "Replaces the API key: the previous one stops working at once. The new key is shown only in this response.",
	}, func(ctx context.Context, _ *struct{}) (*noStore[APIKeyCreated], error) {
		a, p, err := s.caller(ctx)
		if err != nil {
			return nil, err
		}
		key, info, err := a.RegenerateAPIKey(ctx, p.UserID)
		if err != nil {
			return nil, s.authProblem(ctx, err)
		}
		return &noStore[APIKeyCreated]{CacheControl: "no-store", Body: APIKeyCreated{APIKey: key, Last4: info.Last4, CreatedAt: info.CreatedAt}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "auth-revoke-session", Method: http.MethodDelete, Path: "/auth/sessions/{id}", Tags: tags,
		DefaultStatus: http.StatusNoContent,
		Summary:       "End a session", Description: "Signs a browser out. Ending the current session signs this browser out too.",
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		a, p, err := s.caller(ctx)
		if err != nil {
			return nil, err
		}
		if err := a.RevokeSession(ctx, p.UserID, in.ID); err != nil {
			return nil, s.authProblem(ctx, err)
		}
		return nil, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "get-security-settings", Method: http.MethodGet, Path: "/settings/security", Tags: []string{"Settings"},
		Summary:     "Security settings",
		Description: "Authentication mode, signed-in sessions, API key (last four characters only) and the outbound connection policy.",
	}, func(ctx context.Context, _ *struct{}) (*noStore[SecuritySettings], error) {
		a, err := s.authService()
		if err != nil {
			return nil, err
		}
		p, _ := PrincipalOf(ctx)
		cfg := netx.Current()
		out := SecuritySettings{
			AuthMode: s.authMode(), CookieSecure: s.cookieMode(), TrustedOrigins: append([]string{}, s.d.Security.TrustedOrigins...),
			Method: string(p.Method), Sessions: []AuthSession{},
			Outbound: OutboundPolicy{Integration: cfg.Integration, Webhook: cfg.Webhook, AlwaysBlocked: alwaysBlocked},
		}
		if p.UserID != 0 {
			u, err := a.User(ctx, p.UserID)
			if err != nil {
				return nil, s.authProblem(ctx, err)
			}
			au := authUserOf(u)
			out.User = &au
			sessions, err := a.Sessions(ctx, p.UserID)
			if err != nil {
				return nil, s.problem(ctx, err, false)
			}
			for _, x := range sessions {
				out.Sessions = append(out.Sessions, AuthSession{ID: x.ID, Current: x.ID == p.SessionID, CreatedAt: x.CreatedAt,
					LastSeenAt: x.LastSeenAt, ExpiresAt: x.ExpiresAt, IP: x.IP, UserAgent: x.UserAgent})
			}
			k, ok, err := a.APIKey(ctx, p.UserID)
			if err != nil {
				return nil, s.problem(ctx, err, false)
			}
			if ok {
				out.APIKey = APIKeyState{Present: true, Last4: k.Last4, CreatedAt: &k.CreatedAt, LastUsedAt: k.LastUsedAt}
			}
		}
		return &noStore[SecuritySettings]{CacheControl: "no-store", Body: out}, nil
	})
}

var alwaysBlocked = []string{
	"0.0.0.0/8 and ::", "169.254.0.0/16 and fe80::/10 (link-local)",
	"cloud metadata (169.254.169.254, 169.254.170.2, fd00:ec2::254, 100.100.100.200, 168.63.129.16, 192.0.0.192)",
	"multicast and broadcast", "reserved and documentation ranges", "schemes other than http and https",
}

func (s *Server) authMode() string {
	if s.d.Security.AuthMode == "" {
		return config.AuthEnabled
	}
	return s.d.Security.AuthMode
}

func (s *Server) cookieMode() string {
	if s.d.Security.CookieSecure == "" {
		return config.CookieSecureAuto
	}
	return s.d.Security.CookieSecure
}

func (s *Server) authService() (*auth.Service, error) {
	if s.d.Auth == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication is not configured on this server.")
	}
	return s.d.Auth, nil
}

// caller returns the service and the authenticated caller, which must have an
// account (the local mode before setup has none).
func (s *Server) caller(ctx context.Context) (*auth.Service, auth.Principal, error) {
	a, err := s.authService()
	if err != nil {
		return nil, auth.Principal{}, err
	}
	p, ok := PrincipalOf(ctx)
	if !ok {
		return nil, auth.Principal{}, huma.Error401Unauthorized("Sign in first.")
	}
	if p.UserID == 0 {
		return nil, auth.Principal{}, huma.Error409Conflict("No account exists yet: create it first.")
	}
	return a, p, nil
}

func authUserOf(u auth.User) AuthUser {
	return AuthUser{ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

// authProblem maps the errors of package auth.
func (s *Server) authProblem(ctx context.Context, err error) error {
	var inv *auth.InvalidError
	var rl *auth.RateLimitedError
	switch {
	case errors.As(err, &inv):
		return huma.Error422UnprocessableEntity(inv.Msg)
	case errors.As(err, &rl):
		return huma.ErrorWithHeaders(huma.Error429TooManyRequests("Too many failed attempts: wait a few minutes before trying again."),
			http.Header{"Retry-After": {strconv.Itoa(int(rl.RetryAfter.Seconds()) + 1)}})
	case errors.Is(err, auth.ErrCredentials):
		return huma.Error401Unauthorized("Wrong username or password.")
	case errors.Is(err, auth.ErrSetupDone):
		return huma.Error409Conflict("The account already exists: sign in instead.")
	case errors.Is(err, auth.ErrNeedsSetup):
		return huma.Error409Conflict("No account exists yet: create it first.")
	case errors.Is(err, auth.ErrNotFound):
		return huma.Error404NotFound("Not found.")
	case errors.Is(err, context.Canceled):
		return huma.Error503ServiceUnavailable("The request was canceled.")
	}
	s.log.Error("authentication request failed", slog.String("request_id", RequestID(ctx)), slog.Any("error", err))
	return huma.Error500InternalServerError("Something went wrong on the server; the logs have the details (request id " + RequestID(ctx) + ").")
}

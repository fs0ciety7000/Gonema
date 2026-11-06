package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/auth"
	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/webui"
)

// Authentication of the HTTP API.
//
// Every path under /api requires a caller, except the auth endpoints listed in
// publicAPIPaths; /health and the web interface files are public. A caller is
// identified, in this order, by:
//
//  1. the X-Api-Key header (scripts, other apps): never by the query string,
//     which ends up in proxy logs and browser history;
//  2. the session cookie set by login or setup (the web interface);
//  3. with GONEMA_AUTH=disabled_for_local only, a direct connection from a
//     loopback or private address without proxy headers.
//
// Cross-site request forgery: state-changing requests authenticated by the
// cookie (or by the local mode, which a browser on the LAN also carries) must
// pass http.CrossOriginProtection (Sec-Fetch-Site, else Origin compared with
// Host; GONEMA_TRUSTED_ORIGINS adds origins). API key requests are exempt:
// browsers never add that header by themselves.

// SessionCookie is the name of the session cookie.
const SessionCookie = "gonema_session"

// APIKeyHeader carries the API key.
const APIKeyHeader = "X-Api-Key"

var publicAPIPaths = map[string]bool{
	Prefix + "/auth/status": true,
	Prefix + "/auth/setup":  true,
	Prefix + "/auth/login":  true,
	Prefix + "/auth/logout": true,
}

const (
	principalKey ctxKey = iota + 100
	clientKey
)

// client is what the auth handlers need to know of the request.
type client struct {
	IP        string // for rate limiting and the session list
	UserAgent string
	Secure    bool   // the session cookie gets the Secure attribute
	Token     string // the session cookie presented, "" if none
}

// PrincipalOf returns the authenticated caller of ctx.
func PrincipalOf(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey).(auth.Principal)
	return p, ok
}

func clientOf(ctx context.Context) client {
	c, _ := ctx.Value(clientKey).(client)
	return c
}

// needsAuth reports whether path (without the base URL) requires a caller.
func needsAuth(path string) bool {
	if path != "/api" && !strings.HasPrefix(path, "/api/") {
		return false // /health and the web interface
	}
	return !publicAPIPaths[path]
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// withAuth authenticates the requests of the inner mux (paths without the
// base URL).
func (s *Server) withAuth(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	for _, o := range s.d.Security.TrustedOrigins {
		if err := cop.AddTrustedOrigin(o); err != nil {
			s.log.Warn("ignored trusted origin", slog.String("origin", o), slog.Any("error", err))
		}
	}
	csrf := func(w http.ResponseWriter, r *http.Request) bool {
		if !unsafeMethod(r.Method) {
			return true
		}
		if err := cop.Check(r); err != nil {
			writeProblem(w, r, http.StatusForbidden,
				"This request comes from another site and was refused (cross-site request forgery protection). "+
					"Behind a reverse proxy that rewrites the Host header, add the public origin to GONEMA_TRUSTED_ORIGINS.")
			return false
		}
		return true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := s.clientInfo(r)
		ctx := context.WithValue(r.Context(), clientKey, c)
		r = r.WithContext(ctx)
		required := needsAuth(r.URL.Path)
		if !required && !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r) // health, web interface
			return
		}
		if r.URL.Query().Has("apikey") || r.URL.Query().Has("api_key") {
			writeProblem(w, r, http.StatusBadRequest, "Send the API key in the "+APIKeyHeader+" header, never in the URL.")
			return
		}

		p, err := s.identify(r, c)
		switch {
		case err != nil && !errors.Is(err, auth.ErrUnauthenticated):
			s.log.Error("authentication failed", slog.String("request_id", RequestID(ctx)), slog.Any("error", err))
			writeProblem(w, r, http.StatusServiceUnavailable, "Authentication is unavailable right now; try again (request id "+RequestID(ctx)+").")
			return
		case err != nil && required:
			if r.Header.Get(APIKeyHeader) != "" {
				writeProblem(w, r, http.StatusUnauthorized, "The API key is not valid: copy it again from Settings > Security, or generate a new one.")
			} else {
				writeProblem(w, r, http.StatusUnauthorized, "Sign in to use Gonema (or send an API key in the "+APIKeyHeader+" header).")
			}
			return
		}
		if p.Method != auth.MethodAPIKey && !csrf(w, r) {
			return
		}
		if err == nil {
			r = r.WithContext(context.WithValue(ctx, principalKey, p))
		}
		next.ServeHTTP(w, r)
	})
}

// identify returns the caller of r, auth.ErrUnauthenticated when there is none.
func (s *Server) identify(r *http.Request, c client) (auth.Principal, error) {
	a := s.d.Auth
	if a == nil { // not wired: fail closed
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	ctx := r.Context()
	if key := r.Header.Get(APIKeyHeader); key != "" {
		return a.APIKeyPrincipal(ctx, strings.TrimSpace(key)) // a wrong key is not retried as a cookie
	}
	if c.Token != "" {
		p, err := a.SessionPrincipal(ctx, c.Token)
		if !errors.Is(err, auth.ErrUnauthenticated) {
			return p, err
		}
	}
	if s.d.Security.AuthMode == config.AuthDisabledForLocal && directLocal(r) {
		p := auth.Principal{Method: auth.MethodLocal}
		if u, ok, err := a.Admin(ctx); err != nil {
			return auth.Principal{}, err
		} else if ok {
			p.UserID, p.Username = u.ID, u.Username
		}
		return p, nil
	}
	return auth.Principal{}, auth.ErrUnauthenticated
}

// remoteAddr returns the address of the peer of the connection.
func remoteAddr(r *http.Request) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		host, _, herr := net.SplitHostPort(r.RemoteAddr)
		if herr != nil {
			return netip.Addr{}, false
		}
		a, aerr := netip.ParseAddr(host)
		return a.Unmap(), aerr == nil
	}
	return ap.Addr().Unmap(), true
}

func localAddr(a netip.Addr) bool { return a.IsLoopback() || a.IsPrivate() }

// directLocal reports whether r comes straight (no proxy header) from a
// loopback or private address. A request relayed by a reverse proxy on the
// same host would otherwise look local whatever its real origin.
func directLocal(r *http.Request) bool {
	a, ok := remoteAddr(r)
	if !ok || !localAddr(a) {
		return false
	}
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "X-Forwarded-Host", "Cf-Connecting-Ip", "True-Client-Ip"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	return true
}

// clientInfo reads the client of r. The IP is the connection peer; when that
// peer is a loopback or private address (a reverse proxy), the last
// X-Forwarded-For entry (the one the proxy added) is used instead. It only
// serves login throttling and the session list, never an access decision.
func (s *Server) clientInfo(r *http.Request) client {
	c := client{UserAgent: r.UserAgent()}
	if a, ok := remoteAddr(r); ok {
		c.IP = a.String()
		if localAddr(a) {
			if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
				parts := strings.Split(xff[len(xff)-1], ",")
				if fa, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
					c.IP = fa.Unmap().String()
				}
			}
		}
	}
	switch s.d.Security.CookieSecure {
	case config.CookieSecureAlways:
		c.Secure = true
	case config.CookieSecureNever:
	default:
		c.Secure = r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	if ck, err := r.Cookie(SessionCookie); err == nil {
		c.Token = ck.Value
	}
	return c
}

// sessionCookie returns the Set-Cookie value of a session (token "" clears it).
func (s *Server) sessionCookie(c client, token string, ttl time.Duration) string {
	ck := &http.Cookie{
		Name: SessionCookie, Value: token, Path: s.d.BaseURL + "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Secure,
	}
	if token == "" {
		ck.MaxAge = -1
	} else {
		ck.MaxAge = int(ttl.Seconds())
	}
	return ck.String()
}

// withSecurityHeaders sets the hardening headers of every response. API
// responses are data, never documents: their CSP forbids everything. The web
// interface replaces the CSP with its own (package webui).
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", webui.PermissionsPolicy)
		next.ServeHTTP(w, r)
	})
}

// init drops the request values Huma copies into validation errors: an
// invalid sign-in or settings body would otherwise be echoed back with its
// password or API key (and could end up in a proxy or browser log).
func init() {
	base := huma.NewError
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		for _, e := range errs {
			var d *huma.ErrorDetail
			if errors.As(e, &d) {
				d.Value = nil
			}
		}
		return base(status, msg, errs...)
	}
}

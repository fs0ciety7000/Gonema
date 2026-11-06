package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
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
//     loopback or private address (not a trusted proxy) without proxy
//     headers, whose Host header is an IP literal, localhost or an allowed
//     host (DNS rebinding: a public name that resolves to a LAN address must
//     not reach the instance with the browser's blessing).
//
// Host header: when GONEMA_ALLOWED_HOSTS is set, every /api request that does
// not carry an API key must name an allowed host (or an IP literal, or
// localhost); see hostAllowed.
//
// Cross-site request forgery: state-changing requests authenticated by the
// cookie (or by the local mode, which a browser on the LAN also carries) must
// pass http.CrossOriginProtection (Sec-Fetch-Site, else Origin compared with
// Host; GONEMA_TRUSTED_ORIGINS adds origins). API key requests are exempt:
// browsers never add that header by themselves.

// SessionCookie is the name of the session cookie over plain HTTP or under a
// base URL.
const SessionCookie = "gonema_session"

// HostSessionCookie is the name of the session cookie over HTTPS at the root
// of the host: the __Host- prefix makes browsers refuse it unless it is
// Secure, has Path=/ and no Domain, so a sibling subdomain cannot plant or
// overwrite it. Both names are read (transition from SessionCookie).
const HostSessionCookie = "__Host-gonema_session"

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
	// CookieName is the cookie Token came in ("" if none).
	CookieName string
	// HostOK: the Host header is an IP literal, localhost or an allowed host.
	HostOK bool
	// Loopback: a direct connection from a loopback address (not a trusted
	// proxy, no proxy header) with a loopback Host (localhost, 127.0.0.1, ::1).
	Loopback bool
	// Via is how the client connects (auth.ViaDirect, ViaLocal or ViaProxy),
	// for the session list only.
	Via string
}

// authClient is the client information the auth service records.
func (c *client) authClient() auth.ClientInfo {
	return auth.ClientInfo{IP: c.IP, UserAgent: c.UserAgent, Via: c.Via}
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
		if len(s.d.Security.AllowedHosts) > 0 && !c.HostOK && r.Header.Get(APIKeyHeader) == "" {
			writeProblem(w, r, http.StatusForbidden, hostRefused)
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
		p, err := a.SessionPrincipalFor(ctx, c.Token, c.authClient())
		if !errors.Is(err, auth.ErrUnauthenticated) {
			return p, err
		}
	}
	if s.d.Security.AuthMode == config.AuthDisabledForLocal && c.HostOK && s.directLocal(r) {
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

// hostRefused is the detail of a request whose Host header is not accepted.
const hostRefused = "This server does not answer to this host name. Use its IP address or localhost, " +
	"or add the name to GONEMA_ALLOWED_HOSTS (or its origin to GONEMA_TRUSTED_ORIGINS)."

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

// proxyHeaders are the headers a reverse proxy adds.
var proxyHeaders = []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "Cf-Connecting-Ip", "True-Client-Ip"}

func hasProxyHeader(r *http.Request) bool {
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// directLocal reports whether r comes straight (no proxy header) from a
// loopback or private address that is not a trusted proxy. A request relayed
// by a reverse proxy on the same host would otherwise look local whatever its
// real origin.
func (s *Server) directLocal(r *http.Request) bool {
	a, ok := remoteAddr(r)
	if !ok || !localAddr(a) || s.d.Security.ProxyTrusted(a) {
		return false
	}
	return !hasProxyHeader(r)
}

// requestHost returns the host of the Host header, lower case, without port,
// brackets or trailing dot.
func requestHost(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(h, "]"), "["), ".")
	return strings.ToLower(h)
}

// hostAllowed reports whether the Host header of r is an IP literal,
// localhost, a host of GONEMA_ALLOWED_HOSTS or the host of a trusted origin.
// A DNS rebinding attack reaches the server under the attacker's name: it is
// none of these.
func (s *Server) hostAllowed(r *http.Request) bool {
	h := requestHost(r)
	if h == "" {
		return false
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	if h == "localhost" {
		return true
	}
	if slices.Contains(s.d.Security.AllowedHosts, h) {
		return true
	}
	for _, o := range s.d.Security.TrustedOrigins {
		if u, err := url.Parse(o); err == nil && strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), h) {
			return true
		}
	}
	return false
}

// loopbackHost reports whether the Host header names this machine.
func loopbackHost(r *http.Request) bool {
	h := requestHost(r)
	if h == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(h)
	return err == nil && a.Unmap().IsLoopback()
}

// clientInfo reads the client of r. The IP is the connection peer; when that
// peer is a trusted proxy (GONEMA_TRUSTED_PROXIES), the right-most
// X-Forwarded-For entry that is not itself a trusted proxy is used instead.
// It only serves login throttling and the session list, never an access
// decision. X-Forwarded-Proto is read from trusted proxies only.
func (s *Server) clientInfo(r *http.Request) client {
	c := client{UserAgent: r.UserAgent(), HostOK: s.hostAllowed(r)}
	sec := &s.d.Security
	peer, ok := remoteAddr(r)
	trusted := ok && sec.ProxyTrusted(peer)
	c.Via = auth.ViaDirect
	if ok {
		c.IP = peer.String()
		c.Loopback = peer.IsLoopback() && !trusted && !hasProxyHeader(r) && loopbackHost(r)
		switch {
		case trusted:
			c.Via = auth.ViaProxy
		case peer.IsLoopback() || peer.IsPrivate() || peer.IsLinkLocalUnicast():
			c.Via = auth.ViaLocal
		}
	}
	if trusted {
		if ip, ok := forwardedFor(r, sec); ok {
			c.IP = ip.String()
		}
	}
	httpsProto := strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
	switch sec.CookieSecure {
	case config.CookieSecureAlways:
		c.Secure = true
	case config.CookieSecureNever:
	default:
		c.Secure = r.TLS != nil || (trusted && httpsProto)
		s.noteScheme(r, c.Secure, httpsProto && !trusted)
	}
	for _, name := range []string{HostSessionCookie, SessionCookie} {
		if ck, err := r.Cookie(name); err == nil && ck.Value != "" {
			c.Token, c.CookieName = ck.Value, name
			break
		}
	}
	return c
}

// forwardedFor walks X-Forwarded-For from the right and returns the first
// address that is not a trusted proxy (the client as seen by the outermost
// trusted proxy).
func forwardedFor(r *http.Request, sec *config.Security) (netip.Addr, bool) {
	vals := r.Header.Values("X-Forwarded-For")
	var parts []string
	for _, v := range vals {
		parts = append(parts, strings.Split(v, ",")...)
	}
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return netip.Addr{}, false
		}
		a = a.Unmap()
		if !sec.ProxyTrusted(a) {
			return a, true
		}
	}
	return netip.Addr{}, false
}

// noteScheme warns once (GONEMA_COOKIE_SECURE=auto) when X-Forwarded-Proto
// comes from a peer that is not a trusted proxy (ignored, the cookie may lose
// Secure), and once when HTTPS trusted origins are configured but the first
// requests never arrive over HTTPS.
func (s *Server) noteScheme(r *http.Request, secure, untrustedProto bool) {
	if untrustedProto && s.warnedProto.CompareAndSwap(false, true) {
		s.log.Warn("X-Forwarded-Proto ignored: the request does not come from a trusted proxy. With GONEMA_COOKIE_SECURE=auto the session " +
			"cookie is not marked Secure. Set GONEMA_TRUSTED_PROXIES to the address of the reverse proxy, or GONEMA_COOKIE_SECURE=true.")
	}
	if secure {
		s.sawHTTPS.Store(true)
		return
	}
	if r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/setup" {
		if s.httpsOrigins && !s.sawHTTPS.Load() && s.warnedHTTPS.CompareAndSwap(false, true) {
			s.log.Warn("GONEMA_TRUSTED_ORIGINS lists https origins but this sign-in arrived over plain HTTP as far as Gonema can tell: " +
				"the session cookie is not marked Secure. Set GONEMA_TRUSTED_PROXIES to the address of the reverse proxy, or GONEMA_COOKIE_SECURE=true.")
		}
	}
}

// cookieName is the session cookie name for a response to c.
func (s *Server) cookieName(c client) string {
	if c.Secure && s.d.BaseURL == "" {
		return HostSessionCookie
	}
	return SessionCookie
}

// sessionCookies returns the Set-Cookie values of a session (token "" clears
// it). The other cookie name, if the browser still has it, is cleared.
func (s *Server) sessionCookies(c client, token string, ttl time.Duration) []string {
	name := s.cookieName(c)
	ck := &http.Cookie{
		Name: name, Value: token, Path: s.d.BaseURL + "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Secure || name == HostSessionCookie,
	}
	if token == "" {
		ck.MaxAge = -1
	} else {
		ck.MaxAge = int(ttl.Seconds())
	}
	out := []string{ck.String()}
	if c.CookieName != "" && c.CookieName != name {
		old := &http.Cookie{Name: c.CookieName, Path: s.d.BaseURL + "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
			Secure: c.CookieName == HostSessionCookie, MaxAge: -1}
		out = append(out, old.String())
	}
	return out
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

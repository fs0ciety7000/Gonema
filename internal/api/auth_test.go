package api

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/auth"
	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/mediaserver"
	"github.com/fs0ciety7000/gonema/internal/notify"
	"github.com/fs0ciety7000/gonema/internal/outbound"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
)

const (
	testAdmin    = "admin"
	testPassword = "correct horse battery"
)

// withTestAuth wires an auth service (cheap argon2 parameters) into d, creates
// the admin account and returns its API key, which testEnv.do sends.
func withTestAuth(tb testing.TB, d *Deps) string {
	tb.Helper()
	a := newTestAuth(tb, d)
	cr, err := a.Setup(context.Background(), testAdmin, testPassword, auth.ClientInfo{IP: "127.0.0.1"})
	if err != nil {
		tb.Fatal(err)
	}
	return cr.APIKey
}

func newTestAuth(tb testing.TB, d *Deps) *auth.Service {
	tb.Helper()
	a, err := auth.New(auth.Options{Store: d.Store, Params: auth.FastParams})
	if err != nil {
		tb.Fatal(err)
	}
	d.Auth = a
	if d.Security.AuthMode == "" {
		d.Security = config.DefaultSecurity()
	}
	return a
}

// handler returns the server handler with the API key added to each request
// (for tests that call ServeHTTP directly).
func (e *testEnv) handler() http.Handler {
	h := e.srv.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(APIKeyHeader, e.apiKey)
		h.ServeHTTP(w, r)
	})
}

// browser is a cookie-carrying client of a test server that sends the headers
// a same-origin fetch of the web interface sends.
type browser struct {
	t      *testing.T
	base   string
	cookie string
	name   string // cookie name; default SessionCookie
}

func (b *browser) do(method, path, body string, header ...string) response {
	b.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, b.base+path, r)
	if err != nil {
		b.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", b.base)
	if b.cookie != "" {
		req.Header.Set("Cookie", cmp.Or(b.name, SessionCookie)+"="+b.cookie)
	}
	for i := 0; i+1 < len(header); i += 2 {
		if header[i] == "Host" {
			req.Host = header[i+1]
			continue
		}
		if header[i+1] == "" {
			req.Header.Del(header[i])
		} else {
			req.Header.Set(header[i], header[i+1])
		}
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	for _, c := range resp.Cookies() {
		if (c.Name == SessionCookie || c.Name == HostSessionCookie) && c.MaxAge >= 0 {
			b.cookie, b.name = c.Value, c.Name
		} else if c.Name == cmp.Or(b.name, SessionCookie) && c.MaxAge < 0 {
			b.cookie = ""
		}
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(data)}
}

// setupBody is the body of POST /auth/setup with the setup token (needed
// unless the request is a direct loopback one).
func setupBody(t *testing.T, e *testEnv) string {
	t.Helper()
	tok, err := e.srv.d.Auth.SetupToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return `{"username":"admin","password":"` + testPassword + `","setupToken":"` + tok + `"}`
}

// newFreshEnv is a test server whose account is not created yet.
func newFreshEnv(t *testing.T, sec config.Security) *testEnv {
	t.Helper()
	return newTestEnvWith(t, "", &sec, false)
}

func TestAuthFlow(t *testing.T) {
	e := newFreshEnv(t, config.DefaultSecurity())
	b := &browser{t: t, base: e.ts.URL}

	st := decode[AuthStatus](t, b.do("GET", "/api/v1/auth/status", ""))
	if !st.SetupRequired || st.Authenticated || st.AuthMode != "enabled" || st.Method != "none" {
		t.Fatalf("status before setup = %+v", st)
	}
	problemOK(t, b.do("GET", "/api/v1/movies", ""), 401)
	problemOK(t, b.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"whatever-password"}`), 409)
	// A malformed body is refused without echoing the password.
	if r := b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"S3CRET-echo-check","extra":1}`); r.status != 422 ||
		strings.Contains(r.body, "S3CRET") {
		t.Errorf("malformed setup = %d %s", r.status, r.body)
	}
	if d := problemOK(t, b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"short"}`), 422); !strings.Contains(d, "10") {
		t.Errorf("short password detail = %q", d)
	}

	r := b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"`+testPassword+`"}`)
	if r.status != 201 {
		t.Fatalf("setup = %d %s", r.status, r.body)
	}
	set := r.header.Get("Set-Cookie")
	for _, attr := range []string{"HttpOnly", "SameSite=Lax", "Path=/", "Max-Age="} {
		if !strings.Contains(set, attr) {
			t.Errorf("cookie %q lacks %s", set, attr)
		}
	}
	if strings.Contains(set, "Secure") || r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("plain HTTP cookie = %q, cache %q", set, r.header.Get("Cache-Control"))
	}
	created := decode[SetupResult](t, r)
	if !strings.HasPrefix(created.APIKey, auth.APIKeyPrefix) || created.User.Username != "admin" {
		t.Fatalf("setup result = %+v", created)
	}
	problemOK(t, b.do("POST", "/api/v1/auth/setup", `{"username":"x","password":"another password"}`), 409)
	if r := b.do("GET", "/api/v1/movies", ""); r.status != 200 {
		t.Fatalf("movies with the setup session = %d", r.status)
	}
	st = decode[AuthStatus](t, b.do("GET", "/api/v1/auth/status", ""))
	if st.SetupRequired || !st.Authenticated || st.Method != "session" || st.Username != "admin" {
		t.Errorf("status after setup = %+v", st)
	}

	// Logout, then login rotates the token.
	setupCookie := b.cookie
	if r := b.do("POST", "/api/v1/auth/logout", ""); r.status != 204 || !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout = %d %v", r.status, r.header)
	}
	b.cookie = setupCookie
	problemOK(t, b.do("GET", "/api/v1/movies", ""), 401)
	problemOK(t, b.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"not the password"}`), 401)
	r = b.do("POST", "/api/v1/auth/login", `{"username":"Admin","password":"`+testPassword+`"}`)
	if r.status != 200 || b.cookie == "" || b.cookie == setupCookie {
		t.Fatalf("login = %d %s", r.status, r.body)
	}

	// API key: header only.
	key := created.APIKey
	api := &browser{t: t, base: e.ts.URL}
	if r := api.do("GET", "/api/v1/movies", "", APIKeyHeader, key, "Sec-Fetch-Site", "", "Origin", ""); r.status != 200 {
		t.Errorf("API key = %d", r.status)
	}
	if d := problemOK(t, api.do("GET", "/api/v1/movies?apikey="+key, ""), 400); !strings.Contains(d, APIKeyHeader) {
		t.Errorf("query string key detail = %q", d)
	}
	problemOK(t, api.do("GET", "/api/v1/movies", "", APIKeyHeader, key+"x"), 401)

	// Settings > Security.
	other := &browser{t: t, base: e.ts.URL}
	other.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`"}`, "User-Agent", "phone")
	sec := decode[SecuritySettings](t, b.do("GET", "/api/v1/settings/security", ""))
	if sec.AuthMode != "enabled" || sec.Method != "session" || len(sec.Sessions) != 2 || !sec.APIKey.Present ||
		sec.APIKey.Last4 != key[len(key)-4:] || !sec.Outbound.Integration.AllowPrivate || sec.Outbound.Webhook.AllowPrivate || sec.User == nil {
		t.Fatalf("security settings = %+v", sec)
	}
	var current, phone int64
	for _, s := range sec.Sessions {
		if s.Current {
			current = s.ID
		} else {
			phone = s.ID
		}
	}
	if current == 0 || phone == 0 {
		t.Fatalf("sessions = %+v", sec.Sessions)
	}
	if r := b.do("DELETE", "/api/v1/auth/sessions/"+strconv.FormatInt(phone, 10), ""); r.status != 204 {
		t.Errorf("revoke = %d %s", r.status, r.body)
	}
	problemOK(t, other.do("GET", "/api/v1/movies", ""), 401)
	problemOK(t, b.do("DELETE", "/api/v1/auth/sessions/"+strconv.FormatInt(phone, 10), ""), 404)

	// Password change: other sessions end, this browser gets a new cookie.
	other.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`"}`)
	before := b.cookie
	problemOK(t, b.do("PUT", "/api/v1/auth/password", `{"currentPassword":"wrong one!!","newPassword":"a new long password"}`), 422)
	r = b.do("PUT", "/api/v1/auth/password", `{"currentPassword":"`+testPassword+`","newPassword":"a new long password"}`)
	if r.status != 200 || b.cookie == before || b.cookie == "" {
		t.Fatalf("change password = %d %s", r.status, r.body)
	}
	problemOK(t, other.do("GET", "/api/v1/movies", ""), 401)
	if r := b.do("GET", "/api/v1/movies", ""); r.status != 200 {
		t.Errorf("after the change = %d", r.status)
	}

	// Regenerated API key: shown once, the old one stops working.
	r = b.do("POST", "/api/v1/auth/api-key", "")
	nk := decode[APIKeyCreated](t, r)
	if r.status != 200 || nk.APIKey == key || nk.Last4 != nk.APIKey[len(nk.APIKey)-4:] {
		t.Fatalf("regenerate = %d %s", r.status, r.body)
	}
	problemOK(t, api.do("GET", "/api/v1/movies", "", APIKeyHeader, key), 401)
	if r := api.do("GET", "/api/v1/movies", "", APIKeyHeader, nk.APIKey); r.status != 200 {
		t.Errorf("new key = %d", r.status)
	}
	if body := b.do("GET", "/api/v1/settings/security", "").body; strings.Contains(body, nk.APIKey) || strings.Contains(body, b.cookie) {
		t.Error("the security settings reveal a credential")
	}
}

func TestLoginThrottled(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		sec := config.DefaultSecurity()
		if trusted {
			sec.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
		}
		e := newFreshEnv(t, sec)
		b := &browser{t: t, base: e.ts.URL}
		b.do("POST", "/api/v1/auth/setup", setupBody(t, e))
		b.cookie = ""
		var r response
		for i := range 11 {
			// The client rotates a forged X-Forwarded-For: ignored unless the
			// peer is a trusted proxy.
			r = b.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"not the password"}`, "X-Forwarded-For", "198.51.100."+strconv.Itoa(i)+", 192.0.2.1")
		}
		if !trusted {
			problemOK(t, r, 429)
			if ra, err := strconv.Atoi(r.header.Get("Retry-After")); err != nil || ra < 1 || ra > 15*60+1 {
				t.Errorf("Retry-After = %q", r.header.Get("Retry-After"))
			}
			// Without GONEMA_TRUSTED_PROXIES, another X-Forwarded-For changes nothing.
			problemOK(t, b.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`"}`, "X-Forwarded-For", "203.0.113.9"), 429)
			continue
		}
		// Behind a trusted proxy, the right-most untrusted entry is the client:
		// 192.0.2.1 failed 11 times, 203.0.113.9 is another client.
		problemOK(t, r, 429)
		if r := b.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`"}`, "X-Forwarded-For", "203.0.113.9"); r.status != 200 {
			t.Errorf("other client behind the trusted proxy = %d %s", r.status, r.body)
		}
	}
}

func TestCSRF(t *testing.T) {
	e := newFreshEnv(t, config.Security{AuthMode: config.AuthEnabled, TrustedOrigins: []string{"https://media.example.org"}})
	b := &browser{t: t, base: e.ts.URL}
	r := b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"`+testPassword+`"}`)
	key := decode[SetupResult](t, r).APIKey
	body := `{"label":"kids"}`
	// Same origin: allowed.
	if r := b.do("POST", "/api/v1/tags", body); r.status/100 != 2 {
		t.Fatalf("same-origin = %d %s", r.status, r.body)
	}
	for _, h := range [][]string{
		{"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"},
		{"Sec-Fetch-Site", "same-site", "Origin", "https://evil." + strings.TrimPrefix(e.ts.URL, "http://")},
		{"Sec-Fetch-Site", "", "Origin", "https://evil.example"}, // older browser: Origin only
	} {
		if d := problemOK(t, b.do("POST", "/api/v1/tags", `{"name":"x"}`, h...), 403); !strings.Contains(d, "cross-site") {
			t.Errorf("%v: detail %q", h, d)
		}
		problemOK(t, b.do("POST", "/api/v1/auth/logout", "", h...), 403) // public routes too (login CSRF)
	}
	// A configured origin (reverse proxy that rewrites Host).
	if r := b.do("DELETE", "/api/v1/auth/sessions/999", "", "Sec-Fetch-Site", "", "Origin", "https://media.example.org"); r.status != 404 {
		t.Errorf("trusted origin = %d %s", r.status, r.body)
	}
	// Reads are not checked; API key callers are exempt.
	if r := b.do("GET", "/api/v1/tags", "", "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"); r.status != 200 {
		t.Errorf("cross-site GET = %d", r.status)
	}
	if r := b.do("POST", "/api/v1/tags", `{"label":"api"}`, "Cookie", "", APIKeyHeader, key, "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"); r.status/100 != 2 {
		t.Errorf("API key cross-site = %d %s", r.status, r.body)
	}
}

func TestAuthDisabledForLocal(t *testing.T) {
	e := newFreshEnv(t, config.Security{AuthMode: config.AuthDisabledForLocal})
	b := &browser{t: t, base: e.ts.URL}
	// The test server listens on 127.0.0.1: a local caller, no login needed,
	// even before setup.
	if r := b.do("GET", "/api/v1/movies", ""); r.status != 200 {
		t.Fatalf("local = %d %s", r.status, r.body)
	}
	st := decode[AuthStatus](t, b.do("GET", "/api/v1/auth/status", ""))
	if !st.Authenticated || st.Method != "local" || st.AuthMode != "disabled_for_local" || !st.SetupRequired {
		t.Errorf("status = %+v", st)
	}
	problemOK(t, b.do("POST", "/api/v1/auth/api-key", ""), 409) // no account yet
	// Relayed by a reverse proxy: not local any more.
	problemOK(t, b.do("GET", "/api/v1/movies", "", "X-Forwarded-For", "203.0.113.9"), 401)
	problemOK(t, b.do("GET", "/api/v1/movies", "", "Forwarded", "for=203.0.113.9"), 401)
	// CSRF still applies to the local mode (a LAN browser carries it).
	problemOK(t, b.do("POST", "/api/v1/tags", `{"name":"x"}`, "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"), 403)
	// After setup, the local caller acts as the account.
	b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"`+testPassword+`"}`)
	b.cookie = ""
	sec := decode[SecuritySettings](t, b.do("GET", "/api/v1/settings/security", ""))
	if sec.Method != "local" || sec.User == nil || !sec.APIKey.Present {
		t.Errorf("security = %+v", sec)
	}
}

func TestSecureCookie(t *testing.T) {
	loopProxy := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	for _, tc := range []struct {
		mode    string
		proxies []netip.Prefix
		base    string
		header  []string
		want    bool
		name    string
	}{
		{config.CookieSecureAuto, nil, "", nil, false, SessionCookie},
		// X-Forwarded-Proto is believed from a trusted proxy only.
		{config.CookieSecureAuto, nil, "", []string{"X-Forwarded-Proto", "https"}, false, SessionCookie},
		{config.CookieSecureAuto, loopProxy, "", []string{"X-Forwarded-Proto", "https"}, true, HostSessionCookie},
		{config.CookieSecureAlways, nil, "", nil, true, HostSessionCookie},
		// __Host- requires Path=/: not under a base URL.
		{config.CookieSecureAlways, nil, "/gonema", nil, true, SessionCookie},
		{config.CookieSecureNever, loopProxy, "", []string{"X-Forwarded-Proto", "https"}, false, SessionCookie},
	} {
		sec := config.Security{AuthMode: config.AuthEnabled, CookieSecure: tc.mode, TrustedProxies: tc.proxies}
		e := newTestEnvWith(t, tc.base, &sec, false)
		b := &browser{t: t, base: e.ts.URL + tc.base}
		r := b.do("POST", "/api/v1/auth/setup", setupBody(t, e), tc.header...)
		set := r.header.Get("Set-Cookie")
		if got := strings.Contains(set, "; Secure"); got != tc.want || !strings.HasPrefix(set, tc.name+"=") || r.status != 201 {
			t.Errorf("%s %v %q: %d cookie %q", tc.mode, tc.header, tc.base, r.status, set)
		}
	}
}

// TestHostCookieTransition: over HTTPS the cookie is __Host-gonema_session;
// the old name is still read, and replaced (cleared) at the next sign-in.
func TestHostCookieTransition(t *testing.T) {
	e := newFreshEnv(t, config.Security{AuthMode: config.AuthEnabled, CookieSecure: config.CookieSecureAlways})
	b := &browser{t: t, base: e.ts.URL}
	b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"`+testPassword+`"}`)
	if b.name != HostSessionCookie || b.cookie == "" {
		t.Fatalf("cookie %q=%q", b.name, b.cookie)
	}
	// A browser that still holds the old name (session opened before the
	// upgrade) stays signed in.
	b.name = SessionCookie
	if r := b.do("GET", "/api/v1/movies", ""); r.status != 200 {
		t.Fatalf("old cookie name = %d", r.status)
	}
	r := b.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`"}`)
	var cleared, set bool
	for _, v := range r.header.Values("Set-Cookie") {
		cleared = cleared || (strings.HasPrefix(v, SessionCookie+"=;") && strings.Contains(v, "Max-Age=0"))
		set = set || (strings.HasPrefix(v, HostSessionCookie+"=") && strings.Contains(v, "; Secure") && strings.Contains(v, "Path=/;"))
	}
	if r.status != 200 || !cleared || !set {
		t.Errorf("login = %d %v", r.status, r.header.Values("Set-Cookie"))
	}
}

// TestEveryRouteRequiresAuth calls every operation of the OpenAPI document
// without credentials: only the public auth endpoints answer something else
// than 401. /health and the web interface stay public. It runs at the root
// and under a base URL (the auth handler must wrap both).
func TestEveryRouteRequiresAuth(t *testing.T) {
	for _, base := range []string{"", "/gonema"} {
		t.Run("base="+base, func(t *testing.T) { everyRouteRequiresAuth(t, base) })
	}
}

func everyRouteRequiresAuth(t *testing.T, base string) {
	e := newAcquireEnvBase(t, base)
	newIntegrationRoutes(t, e.testEnv)
	b := &browser{t: t, base: e.ts.URL + base}
	doc, err := json.Marshal(e.srv.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &spec); err != nil {
		t.Fatal(err)
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	public := map[string]bool{"/auth/status": true, "/auth/setup": true, "/auth/login": true, "/auth/logout": true}
	n := 0
	for path, ops := range spec.Paths {
		for method := range ops {
			m := strings.ToUpper(method)
			target := Prefix + param.ReplaceAllString(path, "1")
			r := b.do(m, target, "")
			n++
			if public[path] {
				if r.status == 401 {
					t.Errorf("%s %s is public but answered 401", m, path)
				}
				continue
			}
			if r.status != 401 {
				t.Errorf("%s %s without credentials = %d", m, path, r.status)
			}
		}
	}
	if n < 100 {
		t.Errorf("only %d operations checked", n)
	}
	for _, p := range []string{"/api/v1/openapi.json", "/api/v1/images/poster/w342/abc.jpg", "/api/v1/events", "/api/v1/nope"} {
		if r := b.do("GET", p, ""); r.status != 401 {
			t.Errorf("GET %s = %d", p, r.status)
		}
	}
	if r := (&browser{t: t, base: e.ts.URL}).do("GET", "/health", ""); r.status != 200 {
		t.Errorf("/health = %d", r.status)
	}
}

// TestBaseURLAuth is the regression test of the base URL handler that served
// the raw mux: under GONEMA_BASE_URL, requests without credentials get 401
// and cross-site writes get 403, as at the root.
func TestBaseURLAuth(t *testing.T) {
	e := newTestEnvWith(t, "/gonema", nil, false)
	b := &browser{t: t, base: e.ts.URL + "/gonema"}
	problemOK(t, b.do("GET", "/api/v1/movies", ""), 401)
	problemOK(t, b.do("GET", "/api/v1/openapi.json", ""), 401)
	if r := b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"`+testPassword+`"}`); r.status != 201 {
		t.Fatalf("setup = %d %s", r.status, r.body)
	}
	if !strings.Contains(b.do("GET", "/api/v1/auth/status", "").body, `"authenticated":true`) {
		t.Fatal("the setup session is not accepted under the base URL")
	}
	if d := problemOK(t, b.do("POST", "/api/v1/tags", `{"label":"x"}`, "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"), 403); !strings.Contains(d, "cross-site") {
		t.Errorf("cross-site detail = %q", d)
	}
	problemOK(t, b.do("POST", "/api/v1/auth/logout", "", "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"), 403)
	if r := b.do("POST", "/api/v1/tags", `{"label":"x"}`); r.status/100 != 2 {
		t.Errorf("same-origin write = %d %s", r.status, r.body)
	}
}

// TestHostHeader covers DNS rebinding: a page of evil.example whose name
// resolves to the LAN address of Gonema talks to it with Host evil.example.
func TestHostHeader(t *testing.T) {
	t.Run("local mode", func(t *testing.T) {
		e := newFreshEnv(t, config.Security{AuthMode: config.AuthDisabledForLocal, AllowedHosts: []string{"gonema.lan"},
			TrustedOrigins: []string{"https://media.example.org"}})
		b := &browser{t: t, base: e.ts.URL}
		for _, h := range []string{"", "localhost:7676", "gonema.lan", "GONEMA.LAN.", "media.example.org", "[::1]:7676", "192.168.1.5"} {
			hdr := []string{}
			if h != "" {
				hdr = []string{"Host", h}
			}
			if r := b.do("GET", "/api/v1/movies", "", hdr...); r.status != 200 {
				t.Errorf("Host %q = %d", h, r.status)
			}
		}
		problemOK(t, b.do("GET", "/api/v1/movies", "", "Host", "evil.example"), 403) // ALLOWED_HOSTS set: refused outright
		problemOK(t, b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"`+testPassword+`"}`, "Host", "evil.example"), 403)
		if st := decode[AuthStatus](t, b.do("GET", "/api/v1/auth/status", "")); !st.SetupRequired {
			t.Fatal("the account was created through a rebound name")
		}
	})
	t.Run("local mode without allowed hosts", func(t *testing.T) {
		e := newFreshEnv(t, config.Security{AuthMode: config.AuthDisabledForLocal})
		b := &browser{t: t, base: e.ts.URL}
		problemOK(t, b.do("GET", "/api/v1/movies", "", "Host", "evil.example:7676"), 401)
		if st := decode[AuthStatus](t, b.do("GET", "/api/v1/auth/status", "", "Host", "evil.example")); st.Authenticated {
			t.Errorf("status through a rebound name = %+v", st)
		}
		if d := problemOK(t, b.do("POST", "/api/v1/auth/setup", setupBody(t, e), "Host", "evil.example"), 403); !strings.Contains(d, "GONEMA_ALLOWED_HOSTS") {
			t.Errorf("setup detail = %q", d)
		}
	})
	t.Run("allowed hosts with sessions", func(t *testing.T) {
		e := newFreshEnv(t, config.Security{AuthMode: config.AuthEnabled, AllowedHosts: []string{"gonema.lan"}})
		b := &browser{t: t, base: e.ts.URL}
		r := b.do("POST", "/api/v1/auth/setup", setupBody(t, e), "Host", "gonema.lan")
		if r.status != 201 {
			t.Fatalf("setup = %d %s", r.status, r.body)
		}
		key := decode[SetupResult](t, r).APIKey
		if r := b.do("GET", "/api/v1/movies", "", "Host", "gonema.lan"); r.status != 200 {
			t.Errorf("allowed host = %d", r.status)
		}
		problemOK(t, b.do("GET", "/api/v1/movies", "", "Host", "evil.example"), 403)
		// API key callers are not browsers: any Host.
		if r := b.do("GET", "/api/v1/movies", "", "Host", "evil.example", "Cookie", "", APIKeyHeader, key); r.status != 200 {
			t.Errorf("API key with another Host = %d", r.status)
		}
	})
}

// TestSetupToken: from another machine (anything but a direct loopback
// connection with a loopback Host), the setup needs the token of the logs.
func TestSetupToken(t *testing.T) {
	e := newFreshEnv(t, config.Security{AuthMode: config.AuthEnabled, AllowedHosts: []string{"gonema.lan"}})
	b := &browser{t: t, base: e.ts.URL}
	if st := decode[AuthStatus](t, b.do("GET", "/api/v1/auth/status", "")); st.SetupTokenRequired {
		t.Errorf("loopback status = %+v", st)
	}
	if st := decode[AuthStatus](t, b.do("GET", "/api/v1/auth/status", "", "Host", "gonema.lan")); !st.SetupTokenRequired || !st.SetupRequired {
		t.Errorf("remote status = %+v", st)
	}
	body := func(tok string) string {
		return `{"username":"admin","password":"` + testPassword + `","setupToken":"` + tok + `"}`
	}
	for _, h := range [][]string{{"Host", "gonema.lan"}, {"X-Forwarded-For", "203.0.113.4"}} {
		if d := problemOK(t, b.do("POST", "/api/v1/auth/setup", body(""), h...), 403); !strings.Contains(d, "setup token") {
			t.Errorf("%v without token: %q", h, d)
		}
		problemOK(t, b.do("POST", "/api/v1/auth/setup", body("AAAA-BBBB-CCCC-DDDD-EEEE-FFFF"), h...), 403)
	}
	tok, _ := e.srv.d.Auth.SetupToken(context.Background())
	if r := b.do("POST", "/api/v1/auth/setup", body(strings.ToLower(tok)), "Host", "gonema.lan"); r.status != 201 {
		t.Fatalf("setup with the token = %d %s", r.status, r.body)
	}
	problemOK(t, b.do("POST", "/api/v1/auth/setup", body(tok), "Host", "gonema.lan"), 409)
	if st := decode[AuthStatus](t, b.do("GET", "/api/v1/auth/status", "", "Host", "gonema.lan")); st.SetupTokenRequired {
		t.Errorf("status after setup = %+v", st)
	}
}

// TestLocalModeBehindTrustedProxy: a trusted reverse proxy is never "local",
// even without forwarding headers (Docker's userland proxy adds none).
func TestLocalModeBehindTrustedProxy(t *testing.T) {
	e := newFreshEnv(t, config.Security{AuthMode: config.AuthDisabledForLocal, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}})
	b := &browser{t: t, base: e.ts.URL}
	problemOK(t, b.do("GET", "/api/v1/movies", ""), 401)
	e2 := newFreshEnv(t, config.Security{AuthMode: config.AuthDisabledForLocal, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	if r := (&browser{t: t, base: e2.ts.URL}).do("GET", "/api/v1/movies", ""); r.status != 200 {
		t.Errorf("untrusted local peer = %d", r.status)
	}
}

// TestPasswordChangeRegeneratesKey: PUT /auth/password with regenerateApiKey.
func TestPasswordChangeRegeneratesKey(t *testing.T) {
	e := newFreshEnv(t, config.DefaultSecurity())
	b := &browser{t: t, base: e.ts.URL}
	key := decode[SetupResult](t, b.do("POST", "/api/v1/auth/setup", `{"username":"admin","password":"`+testPassword+`"}`)).APIKey
	r := b.do("PUT", "/api/v1/auth/password", `{"currentPassword":"`+testPassword+`","newPassword":"a new long password","regenerateApiKey":true}`)
	got := decode[PasswordChanged](t, r)
	if r.status != 200 || !strings.HasPrefix(got.APIKey, auth.APIKeyPrefix) || got.APIKey == key || got.APIKeyLast4 != got.APIKey[len(got.APIKey)-4:] {
		t.Fatalf("change = %d %s", r.status, r.body)
	}
	api := &browser{t: t, base: e.ts.URL}
	problemOK(t, api.do("GET", "/api/v1/movies", "", APIKeyHeader, key), 401)
	if r := api.do("GET", "/api/v1/movies", "", APIKeyHeader, got.APIKey); r.status != 200 {
		t.Errorf("new key = %d", r.status)
	}
	// Without the option the key is kept and not returned.
	r = b.do("PUT", "/api/v1/auth/password", `{"currentPassword":"a new long password","newPassword":"another long password"}`)
	if r.status != 200 || strings.Contains(r.body, "apiKey") {
		t.Errorf("change without the option = %d %s", r.status, r.body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newTestEnv(t, "")
	r := e.do(t, "GET", "/api/v1/system/status", "")
	want := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for h, v := range want {
		if got := r.header.Get(h); got != v {
			t.Errorf("%s = %q, want %q", h, got, v)
		}
	}
	if csp := r.header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if pp := r.header.Get("Permissions-Policy"); !strings.Contains(pp, "camera=()") || !strings.Contains(pp, "geolocation=()") {
		t.Errorf("Permissions-Policy = %q", pp)
	}
	// Also on the errors written outside Huma.
	if r := (&browser{t: t, base: e.ts.URL}).do("GET", "/api/v1/movies", ""); r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("401 headers = %v", r.header)
	}
}

func newIntegrationRoutes(t *testing.T, e *testEnv) {
	t.Helper()
	key, _ := secretbox.NewKey()
	box, _ := secretbox.New(key)
	n, err := notify.NewService(notify.ServiceOptions{Store: e.srv.d.Store, Box: box, Bus: e.bus, HTTP: outbound.NewHTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	m, err := mediaserver.NewService(mediaserver.ServiceOptions{Store: e.srv.d.Store, Box: box, Bus: e.bus})
	if err != nil {
		t.Fatal(err)
	}
	e.srv.RegisterIntegrationRoutes(n, m)
}

// TestNoSecretInResponses stores every kind of secret, then reads every list
// and get endpoint (and runs the connection tests) and checks that no response
// and no log line contains one.
func TestNoSecretInResponses(t *testing.T) {
	e := newAcquireEnv(t)
	newIntegrationRoutes(t, e.testEnv)
	const (
		ixKey      = "IDX-SECRET-7f3a9c"
		sabKey     = "SAB-SECRET-0b1e2d"
		qbPass     = "QB-SECRET-5c6d7e"
		signing    = "HMAC-SECRET-1a2b3c"
		hdrSecret  = "HEADER-SECRET-4d5e6f"
		discordTok = "DISCORD-TOKEN-8g9h0i"
		jfToken    = "JELLYFIN-TOKEN-2j3k4l"
		ntfyPass   = "NTFY-SECRET-6m7n8o"
	)
	tr := acquiretest.NewTracker(t, ixKey, acquiretest.Item{Title: "Fight.Club.1999.1080p.BluRay.x264-GRP", Size: 10 << 30, Seeders: 50})
	qb := acquiretest.NewQBittorrent(t, "admin", qbPass)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	defer hook.Close()
	secrets := []string{ixKey, sabKey, qbPass, signing, hdrSecret, discordTok, jfToken, ntfyPass, testKey, e.apiKey, testPassword}

	ids := map[string]int64{}
	create := func(path, body string) {
		t.Helper()
		r := e.do(t, "POST", "/api/v1/"+path, body)
		if r.status != 201 {
			t.Fatalf("create %s = %d %s", path, r.status, r.body)
		}
		var v struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal([]byte(r.body), &v)
		ids[path] = v.ID
	}
	create("indexers", fmt.Sprintf(`{"name":"Tracker","kind":"torznab","baseUrl":%q,"apiKey":%q}`, tr.Srv.URL, ixKey))
	create("download-clients", fmt.Sprintf(`{"name":"qB","kind":"qbittorrent","host":"127.0.0.1","port":%d,"username":"admin","password":%q}`, qb.Port(), qbPass))
	e.do(t, "POST", "/api/v1/download-clients", fmt.Sprintf(`{"name":"SAB","kind":"sabnzbd","host":"127.0.0.1","port":1,"apiKey":%q}`, sabKey))
	create("notifications", `{"name":"Hook","kind":"webhook","settings":{"url":"`+hook.URL+`/in"},
		"secrets":{"signingSecret":"`+signing+`","headers":{"Authorization":"`+hdrSecret+`"}},"events":["grab"]}`)
	e.do(t, "POST", "/api/v1/notifications", `{"name":"Discord","kind":"discord","secrets":{"webhookUrl":"`+hook.URL+`/api/webhooks/1/`+discordTok+`"},"events":["grab"]}`)
	e.do(t, "POST", "/api/v1/notifications", `{"name":"ntfy","kind":"ntfy","settings":{"serverUrl":"`+hook.URL+`","topic":"gonema"},"secrets":{"password":"`+ntfyPass+`"},"events":["grab"]}`)
	create("media-servers", `{"name":"JF","kind":"jellyfin","baseUrl":"`+hook.URL+`","token":"`+jfToken+`"}`)

	var all strings.Builder
	read := func(method, path string) {
		r := e.do(t, method, path, "")
		all.WriteString(method + " " + path + " -> " + r.body + "\n")
	}
	// Every GET without path parameter (lists, settings, status...).
	doc, _ := json.Marshal(e.srv.OpenAPI())
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	_ = json.Unmarshal(doc, &spec)
	var gets []string
	for path, ops := range spec.Paths {
		if _, ok := ops["get"]; ok && !strings.Contains(path, "{") && path != "/events" {
			gets = append(gets, path)
		}
	}
	slices.Sort(gets)
	if len(gets) < 30 {
		t.Fatalf("only %d GET endpoints", len(gets))
	}
	for _, p := range gets {
		read("GET", Prefix+p)
	}
	for path, id := range ids {
		read("GET", fmt.Sprintf("%s/%s/%d", Prefix, path, id))
		read("POST", fmt.Sprintf("%s/%s/%d/test", Prefix, path, id)) // error messages of connection tests
	}
	for _, path := range []string{"notifications", "download-clients"} {
		rows := e.do(t, "GET", Prefix+"/"+path, "")
		var list []struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal([]byte(rows.body), &list)
		for _, x := range list {
			read("GET", fmt.Sprintf("%s/%s/%d", Prefix, path, x.ID))
			read("POST", fmt.Sprintf("%s/%s/%d/test", Prefix, path, x.ID))
		}
	}
	time.Sleep(50 * time.Millisecond) // let background log lines land
	out := all.String()
	if !strings.Contains(out, `"apiKeySet":true`) || !strings.Contains(out, `"tokenSet":true`) {
		t.Fatalf("the seeded objects are not in the responses:\n%.2000s", out)
	}
	for _, s := range secrets {
		if strings.Contains(out, s) {
			i := strings.Index(out, s)
			t.Errorf("a response contains the secret %q: …%s…", s, out[max(0, i-300):min(len(out), i+100)])
		}
		if strings.Contains(e.logs.String(), s) {
			t.Errorf("the logs contain the secret %q", s)
		}
	}
}

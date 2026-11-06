// Package netx is the hardened network plumbing of every outbound call to an
// address the user typed: indexers, download clients, media servers,
// notification services, webhooks and SMTP servers.
//
// Protection against server-side request forgery (SSRF):
//
//   - only http and https URLs are accepted, also after a redirect;
//   - the destination IP is checked when the connection is opened (the
//     net.Dialer Control function sees the address actually dialed, after DNS
//     resolution), so DNS rebinding cannot swap a checked name for a forbidden
//     address between the check and the connection;
//   - always refused: unspecified (0.0.0.0, ::), "this network" 0.0.0.0/8,
//     link-local (169.254.0.0/16, fe80::/10), the cloud metadata addresses
//     (169.254.169.254, 169.254.170.2, fd00:ec2::254, 100.100.100.200,
//     168.63.129.16, 192.0.0.192), multicast, broadcast and reserved ranges;
//     IPv6 forms that embed an IPv4 address (IPv4-mapped, NAT64, 6to4) are
//     judged by the embedded address, and IPv4-compatible addresses are refused;
//   - loopback and private ranges (RFC 1918, fc00::/7, 100.64.0.0/10) follow a
//     Policy: a self-hosted app legitimately talks to SABnzbd on 127.0.0.1 or a
//     NAS on 192.168.x, so integrations allow them by default, while webhooks
//     (Webhook class) are public-only unless the operator opts in;
//   - response bodies are capped (MaxResponseBytes) and every phase of a
//     connection has a timeout.
//
// When an HTTP(S) proxy is configured in the environment (HTTPS_PROXY...), the
// proxy resolves the destination: the target host is then resolved and
// checked before the request is handed to the proxy, but the proxy itself is
// trusted. See docs/security.md.
package netx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Limits shared by every client of this package.
const (
	// MaxResponseBytes caps any response body read through a client of this
	// package; callers keep their own, usually smaller, limits.
	MaxResponseBytes = 64 << 20
	// MaxRedirects bounds the redirects a client follows (each one re-checked).
	MaxRedirects = 5

	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 60 * time.Second
	idleConnTimeout       = 90 * time.Second
)

// Policy says which non-public destinations are allowed. Public addresses are
// always allowed; the always-refused ranges (link-local, metadata...) never are.
type Policy struct {
	AllowLoopback bool `json:"allowLoopback" doc:"127.0.0.0/8 and ::1 may be contacted"`
	AllowPrivate  bool `json:"allowPrivate" doc:"Private ranges (10/8, 172.16/12, 192.168/16, 100.64/10, fc00::/7) may be contacted"`
}

// Class is the kind of destination a client is for.
type Class int

const (
	// Integration is for indexers, download clients, media servers, push services
	// that are often self-hosted (ntfy, Gotify), SMTP. LAN allowed by default.
	Integration Class = iota
	// Webhook is for generic webhooks and chat webhooks (Discord, Slack). Public
	// only by default.
	Webhook
	// Public is for fixed public services (TMDB and its image server): public
	// addresses only, whatever the configuration.
	Public
)

func (c Class) String() string {
	switch c {
	case Webhook:
		return "webhook"
	case Public:
		return "public"
	}
	return "integration"
}

// Config is the policy of each class.
type Config struct {
	Integration Policy `json:"integration"`
	Webhook     Policy `json:"webhook"`
}

// DefaultConfig lets integrations reach loopback and LAN, webhooks public only.
func DefaultConfig() Config {
	return Config{
		Integration: Policy{AllowLoopback: true, AllowPrivate: true},
		Webhook:     Policy{},
	}
}

var current atomic.Pointer[Config]

func init() {
	c := DefaultConfig()
	current.Store(&c)
}

// Configure sets the process-wide policies (at startup, from the
// configuration). Clients already built follow the change: the policy is read
// at each connection.
func Configure(c Config) { current.Store(&c) }

// Current returns the process-wide policies.
func Current() Config { return *current.Load() }

// Policy returns the current policy of the class.
func (c Class) Policy() Policy {
	cfg := current.Load()
	switch c {
	case Webhook:
		return cfg.Webhook
	case Public:
		return Policy{}
	}
	return cfg.Integration
}

// ErrBlocked is matched (errors.Is) by every refusal of this package.
var ErrBlocked = errors.New("netx: destination not allowed")

// BlockedError is a refused destination. Its message is a plain sentence
// naming the address (never a URL, which may carry credentials).
type BlockedError struct {
	Addr   netip.Addr // zero for a refused scheme
	Reason string     // "a link-local address", "a private (LAN) address"...
	Hint   string     // how to allow it, when it can be allowed
}

func (e *BlockedError) Error() string {
	msg := "Gonema refuses to connect to "
	if e.Addr.IsValid() {
		msg += e.Addr.String() + ", "
	}
	msg += e.Reason
	if e.Hint != "" {
		msg += " (" + e.Hint + ")"
	}
	return msg
}

// Is matches ErrBlocked.
func (e *BlockedError) Is(target error) bool { return target == ErrBlocked }

// Address categories.
type category int

const (
	catPublic category = iota
	catLoopback
	catPrivate
	catNever
)

var (
	metadataAddrs = []netip.Addr{
		netip.MustParseAddr("169.254.169.254"), // AWS, GCP, Azure, OpenStack, DigitalOcean...
		netip.MustParseAddr("169.254.170.2"),   // AWS ECS task metadata
		netip.MustParseAddr("fd00:ec2::254"),   // AWS IPv6 metadata (inside fc00::/7)
		netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud (inside 100.64/10)
		netip.MustParseAddr("168.63.129.16"),   // Azure wire server (public range)
		netip.MustParseAddr("192.0.0.192"),     // Oracle Cloud classic
	}
	neverPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
		netip.MustParsePrefix("192.0.2.0/24"),    // documentation
		netip.MustParsePrefix("198.51.100.0/24"), // documentation
		netip.MustParsePrefix("203.0.113.0/24"),  // documentation
		netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes 255.255.255.255
		netip.MustParsePrefix("100::/64"),        // discard-only
		netip.MustParsePrefix("2001:db8::/32"),   // documentation
		netip.MustParsePrefix("::/96"),           // IPv4-compatible (deprecated), :: and ::1 handled before
	}
	privatePrefixes = []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, Tailscale
		netip.MustParsePrefix("198.18.0.0/15"), // benchmarking, used by some local DNS proxies
		netip.MustParsePrefix("fec0::/10"),     // deprecated site-local
	}
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	// nat64Local is the local-use NAT64 prefix (RFC 8215). Its operators pick
	// the prefix length (/48 to /96), so the IPv4 address may sit anywhere:
	// only the /96 form 64:ff9b:1::a.b.c.d is judged by its embedded address,
	// the rest is refused.
	nat64Local   = netip.MustParsePrefix("64:ff9b:1::/48")
	nat64Local96 = netip.MustParsePrefix("64:ff9b:1::/96")
	sixToFour    = netip.MustParsePrefix("2002::/16")
)

// classify returns the category of ip and a description for errors.
func classify(ip netip.Addr) (cat category, why string) {
	ip = ip.Unmap() // ::ffff:a.b.c.d is a.b.c.d
	if ip.Is6() && ip.Zone() != "" {
		ip = ip.WithZone("")
	}
	if ip.Is6() {
		b := ip.As16()
		if nat64WellKnown.Contains(ip) || nat64Local96.Contains(ip) {
			return classify(netip.AddrFrom4([4]byte(b[12:16])))
		}
		if nat64Local.Contains(ip) {
			return catNever, "a local-use NAT64 address"
		}
		if sixToFour.Contains(ip) {
			return classify(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	for _, m := range metadataAddrs {
		if ip == m {
			return catNever, "a cloud metadata address"
		}
	}
	switch {
	case !ip.IsValid():
		return catNever, "an invalid address"
	case ip.IsUnspecified():
		return catNever, "the unspecified address"
	case ip.IsLoopback():
		return catLoopback, "a loopback address"
	case ip.IsLinkLocalUnicast():
		return catNever, "a link-local address"
	case ip.IsMulticast(), ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast():
		return catNever, "a multicast address"
	}
	for _, p := range neverPrefixes {
		if p.Contains(ip) {
			return catNever, "a reserved address"
		}
	}
	if ip.IsPrivate() {
		return catPrivate, "a private (LAN) address"
	}
	for _, p := range privatePrefixes {
		if p.Contains(ip) {
			return catPrivate, "a private (LAN) address"
		}
	}
	return catPublic, ""
}

// IsPublic reports whether ip is a public address (neither loopback, private
// nor always refused).
func IsPublic(ip netip.Addr) bool {
	cat, _ := classify(ip)
	return cat == catPublic
}

// CheckAddr returns nil when the policy allows connecting to ip, a
// *BlockedError otherwise.
func CheckAddr(p Policy, ip netip.Addr) error {
	return checkAddr(p, ip, "")
}

func checkAddr(p Policy, ip netip.Addr, hint string) error {
	cat, why := classify(ip)
	switch cat {
	case catPublic:
		return nil
	case catLoopback:
		if p.AllowLoopback {
			return nil
		}
	case catPrivate:
		if p.AllowPrivate {
			return nil
		}
	case catNever:
		return &BlockedError{Addr: ip.Unmap(), Reason: why}
	}
	return &BlockedError{Addr: ip.Unmap(), Reason: why, Hint: hint}
}

func hintFor(c Class) string {
	if c == Public {
		return ""
	}
	if c == Webhook {
		return "webhooks reach public addresses only; set GONEMA_WEBHOOK_ALLOW_PRIVATE=true to allow local ones"
	}
	return "allowed with GONEMA_OUTBOUND_ALLOW_PRIVATE / GONEMA_OUTBOUND_ALLOW_LOOPBACK"
}

// CheckURL validates the scheme (http or https) and the host of u.
func CheckURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return &BlockedError{Reason: "a URL that is not http:// or https://"}
	}
	if u.Hostname() == "" {
		return &BlockedError{Reason: "a URL without a host"}
	}
	return nil
}

// Control returns a net.Dialer Control function enforcing the policy of class
// on the address actually dialed (after DNS resolution).
func Control(class Class) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		return controlContext(class)(context.Background(), network, address, c)
	}
}

// controlContext is Control with the Scope of the dial context.
func controlContext(class Class) func(ctx context.Context, network, address string, _ syscall.RawConn) error {
	return func(ctx context.Context, network, address string, _ syscall.RawConn) error {
		switch network {
		case "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6":
		default:
			return &BlockedError{Reason: "a non-IP network (" + network + ")"}
		}
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return &BlockedError{Reason: "an address that cannot be parsed"}
		}
		return checkScoped(ctx, class, ap.Addr())
	}
}

// checkScoped applies the policy of class, then the Scope of ctx if any.
func checkScoped(ctx context.Context, class Class, ip netip.Addr) error {
	if err := checkAddr(class.Policy(), ip, hintFor(class)); err != nil {
		return err
	}
	sc, ok := ctx.Value(scopeKey{}).(*Scope)
	if !ok || sc == nil {
		return nil
	}
	if cat, _ := classify(ip); cat == catPublic {
		return nil
	}
	u := ip.Unmap().WithZone("")
	for _, a := range sc.allow {
		if a == u {
			return nil
		}
	}
	return &BlockedError{Addr: u, Reason: "a local address other than the one of the service that gave the link"}
}

// Scope narrows the destinations of the requests made with a context (see
// WithScope): public addresses stay allowed; a loopback or private address
// only when it is one of the scope's addresses (and the class policy allows
// it). It serves links handed out by a remote service, such as the download
// links of an indexer: a public indexer must not make Gonema fetch from the
// LAN, a LAN indexer may only point back to itself.
type Scope struct {
	allow []netip.Addr
}

type scopeKey struct{}

// WithScope returns a context whose requests and dials follow sc.
func WithScope(ctx context.Context, sc *Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, sc)
}

// ScopeOfHost returns the Scope of links given by the service at host: no
// local address when host resolves to public addresses only, else the local
// addresses host resolves to.
func ScopeOfHost(ctx context.Context, host string) (*Scope, error) {
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		ips = []netip.Addr{ip}
	} else {
		ctx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		ips, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("netx: resolve %s: %w", host, err)
		}
	}
	sc := &Scope{}
	for _, ip := range ips {
		if cat, _ := classify(ip); cat != catPublic {
			sc.allow = append(sc.allow, ip.Unmap().WithZone(""))
		}
	}
	return sc, nil
}

// PublicOnly reports whether the scope allows public addresses only.
func (sc *Scope) PublicOnly() bool { return len(sc.allow) == 0 }

// NewDialer returns a dialer enforcing the policy of class (and the Scope of
// the dial context, if any).
func NewDialer(class Class) *net.Dialer {
	return &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second, ControlContext: controlContext(class)}
}

// NewTransport returns an HTTP transport whose connections follow the policy
// of class: proxy from the environment (see the package comment), TLS 1.2
// minimum, timeouts on every phase. Callers may adjust TLSClientConfig.
func NewTransport(class Class) *http.Transport {
	return newTransport(class, http.ProxyFromEnvironment)
}

func newTransport(class Class, envProxy func(*http.Request) (*url.URL, error)) *http.Transport {
	guarded := NewDialer(class)
	plain := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			pu, err := envProxy(req)
			if err != nil || pu == nil {
				return pu, err
			}
			// The proxy resolves the target: check it here, the dialer only
			// sees the proxy.
			if err := precheckHost(req.Context(), class, req.URL.Hostname()); err != nil {
				return nil, err
			}
			// Only requests that went through Wrap carry a mark: the proxy
			// connection of other requests is checked like any destination.
			if m, ok := req.Context().Value(proxyMarkKey{}).(*proxyMark); ok {
				m.addr.Store(canonicalAddr(pu))
			}
			return pu, nil
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// The dial context carries the values of the request that asked for
			// the connection: only the proxy connection of a request whose
			// Proxy function chose that proxy skips the check (L3). A direct
			// connection to the same address does not.
			if m, ok := ctx.Value(proxyMarkKey{}).(*proxyMark); ok {
				if p, _ := m.addr.Load().(string); p != "" && p == addr {
					return plain.DialContext(ctx, network, addr)
				}
			}
			return guarded.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
	}
	tr.TLSClientConfig = defaultTLS()
	return tr
}

// proxyMark records, per request, the proxy its Proxy function chose.
type proxyMark struct{ addr atomic.Value } // string "host:port"

type proxyMarkKey struct{}

func defaultTLS() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }

// precheckHost resolves host and checks every address (requests through a
// proxy, redirects of scoped requests).
func precheckHost(ctx context.Context, class Class, host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		return checkScoped(ctx, class, ip)
	}
	rctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(rctx, "ip", host)
	if err != nil {
		return fmt.Errorf("netx: resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if err := checkScoped(ctx, class, ip); err != nil {
			return err
		}
	}
	return nil
}

// CheckHost resolves host and checks every address against the policy of
// class and the Scope of ctx. The dialer checks again at connection time; this
// early check also covers connections reused from the pool.
func CheckHost(ctx context.Context, class Class, host string) error {
	return precheckHost(ctx, class, host)
}

func canonicalAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// Wrap adds the scheme check and the MaxResponseBytes cap to rt.
func Wrap(rt http.RoundTripper) http.RoundTripper { return &guard{next: rt, max: MaxResponseBytes} }

type guard struct {
	next http.RoundTripper
	max  int64
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := CheckURL(req.URL); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	req = req.WithContext(context.WithValue(req.Context(), proxyMarkKey{}, &proxyMark{}))
	resp, err := g.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &capped{rc: resp.Body, left: g.max}
	return resp, nil
}

// ErrTooLarge is returned when a response body exceeds MaxResponseBytes.
var ErrTooLarge = errors.New("netx: response too large")

type capped struct {
	rc   io.ReadCloser
	left int64
}

func (c *capped) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.rc.Read(p)
	c.left -= int64(n)
	return n, err
}

func (c *capped) Close() error { return c.rc.Close() }

// CheckRedirect follows at most MaxRedirects redirects, to http(s) URLs only.
// The destination address is checked again by the dialer.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxRedirects {
		return errors.New("netx: too many redirects")
	}
	return CheckURL(req.URL)
}

// NoRedirect does not follow redirects: the 3xx response is returned.
func NoRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// NewClient returns a client for class: hardened transport, response cap,
// re-checked redirects. It has no global timeout: callers bound each request
// with a context.
func NewClient(class Class) *http.Client {
	return &http.Client{Transport: Wrap(NewTransport(class)), CheckRedirect: CheckRedirect}
}

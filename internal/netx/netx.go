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
	"sync"
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
)

func (c Class) String() string {
	if c == Webhook {
		return "webhook"
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
	if c == Webhook {
		return cfg.Webhook
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
	nat64Prefixes = []netip.Prefix{
		netip.MustParsePrefix("64:ff9b::/96"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
	}
	sixToFour = netip.MustParsePrefix("2002::/16")
)

// classify returns the category of ip and a description for errors.
func classify(ip netip.Addr) (cat category, why string) {
	ip = ip.Unmap() // ::ffff:a.b.c.d is a.b.c.d
	if ip.Is6() && ip.Zone() != "" {
		ip = ip.WithZone("")
	}
	if ip.Is6() {
		b := ip.As16()
		for _, p := range nat64Prefixes {
			if p.Contains(ip) {
				return classify(netip.AddrFrom4([4]byte(b[12:16])))
			}
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
	return func(network, address string, _ syscall.RawConn) error {
		switch network {
		case "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6":
		default:
			return &BlockedError{Reason: "a non-IP network (" + network + ")"}
		}
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return &BlockedError{Reason: "an address that cannot be parsed"}
		}
		return checkAddr(class.Policy(), ap.Addr(), hintFor(class))
	}
}

// NewDialer returns a dialer enforcing the policy of class.
func NewDialer(class Class) *net.Dialer {
	return &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second, Control: Control(class)}
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
	var proxies sync.Map // "host:port" of the proxies in use: dialed without checks
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
			proxies.Store(canonicalAddr(pu), struct{}{})
			return pu, nil
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if _, ok := proxies.Load(addr); ok {
				return plain.DialContext(ctx, network, addr)
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

func defaultTLS() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }

// precheckHost resolves host and checks every address (requests through a
// proxy only).
func precheckHost(ctx context.Context, class Class, host string) error {
	p := class.Policy()
	if ip, err := netip.ParseAddr(host); err == nil {
		return checkAddr(p, ip, hintFor(class))
	}
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("netx: resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if err := checkAddr(p, ip, hintFor(class)); err != nil {
			return err
		}
	}
	return nil
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

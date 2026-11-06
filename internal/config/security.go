package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/netx"
)

// Security variables (see docs/security.md):
//
//	GONEMA_AUTH                    enabled (default) | disabled_for_local: requests
//	                               from loopback or private addresses skip the
//	                               login. Explicit opt-in for a trusted LAN.
//	GONEMA_COOKIE_SECURE           auto (default: Secure when the request came over
//	                               HTTPS or X-Forwarded-Proto is https) | true | false.
//	GONEMA_TRUSTED_ORIGINS         comma-separated origins (https://media.example.org)
//	                               accepted by the cross-origin check besides the
//	                               request's own host (reverse proxies that rewrite Host).
//	GONEMA_ALLOWED_HOSTS           comma-separated host names (media.example.org)
//	                               the server answers to besides IP literals and
//	                               localhost (DNS rebinding protection). When set,
//	                               every request without an API key must use one.
//	GONEMA_TRUSTED_PROXIES         comma-separated addresses or CIDR ranges of the
//	                               reverse proxies whose X-Forwarded-For and
//	                               X-Forwarded-Proto are believed; default empty.
//	GONEMA_OUTBOUND_ALLOW_LOOPBACK integrations may reach 127.0.0.0/8 and ::1; default true.
//	GONEMA_OUTBOUND_ALLOW_PRIVATE  integrations may reach private ranges; default true.
//	GONEMA_WEBHOOK_ALLOW_PRIVATE   webhooks may reach loopback and private ranges;
//	                               default false (public only).
const (
	EnvAuth                  = "GONEMA_AUTH"
	EnvCookieSecure          = "GONEMA_COOKIE_SECURE"
	EnvTrustedOrigins        = "GONEMA_TRUSTED_ORIGINS"
	EnvAllowedHosts          = "GONEMA_ALLOWED_HOSTS"
	EnvTrustedProxies        = "GONEMA_TRUSTED_PROXIES"
	EnvOutboundAllowLoopback = "GONEMA_OUTBOUND_ALLOW_LOOPBACK"
	EnvOutboundAllowPrivate  = "GONEMA_OUTBOUND_ALLOW_PRIVATE"
	EnvWebhookAllowPrivate   = "GONEMA_WEBHOOK_ALLOW_PRIVATE"
)

// Authentication modes.
const (
	AuthEnabled          = "enabled"
	AuthDisabledForLocal = "disabled_for_local"
)

// Cookie Secure modes.
const (
	CookieSecureAuto   = "auto"
	CookieSecureAlways = "true"
	CookieSecureNever  = "false"
)

// Security is the security configuration. It holds no secret.
type Security struct {
	AuthMode       string // AuthEnabled or AuthDisabledForLocal
	CookieSecure   string // CookieSecureAuto, CookieSecureAlways or CookieSecureNever
	TrustedOrigins []string
	// AllowedHosts are the host names (lower case, without port) accepted in
	// the Host header besides IP literals and localhost.
	AllowedHosts []string
	// TrustedProxies are the reverse proxies whose forwarding headers are read.
	TrustedProxies []netip.Prefix
	Outbound       netx.Config
}

// DefaultSecurity is: authentication on, automatic Secure cookie, integrations
// reach loopback and LAN, webhooks public addresses only.
func DefaultSecurity() Security {
	return Security{AuthMode: AuthEnabled, CookieSecure: CookieSecureAuto, Outbound: netx.DefaultConfig()}
}

type securityFlags struct {
	auth, cookie, origins, hosts, proxies, loopback, private, webhook *string
}

func newSecurityFlags(fs *flag.FlagSet, str func(name, fallback string) string) *securityFlags {
	return &securityFlags{
		auth: fs.String("auth", str(EnvAuth, AuthEnabled),
			"authentication: enabled, or disabled_for_local to skip the login for loopback and private addresses ("+EnvAuth+")"),
		cookie: fs.String("cookie-secure", str(EnvCookieSecure, CookieSecureAuto),
			"Secure attribute of the session cookie: auto, true or false ("+EnvCookieSecure+")"),
		origins: fs.String("trusted-origins", str(EnvTrustedOrigins, ""),
			"extra origins accepted by the cross-origin check, comma-separated ("+EnvTrustedOrigins+")"),
		hosts: fs.String("allowed-hosts", str(EnvAllowedHosts, ""),
			"host names the server answers to besides IP addresses and localhost, comma-separated ("+EnvAllowedHosts+")"),
		proxies: fs.String("trusted-proxies", str(EnvTrustedProxies, ""),
			"addresses or CIDR ranges of the reverse proxies whose X-Forwarded-* headers are believed, comma-separated ("+EnvTrustedProxies+")"),
		loopback: fs.String("outbound-allow-loopback", str(EnvOutboundAllowLoopback, "true"),
			"integrations may connect to loopback addresses ("+EnvOutboundAllowLoopback+")"),
		private: fs.String("outbound-allow-private", str(EnvOutboundAllowPrivate, "true"),
			"integrations may connect to private (LAN) addresses ("+EnvOutboundAllowPrivate+")"),
		webhook: fs.String("webhook-allow-private", str(EnvWebhookAllowPrivate, "false"),
			"webhooks may connect to loopback and private addresses ("+EnvWebhookAllowPrivate+")"),
	}
}

func (f *securityFlags) finish() (Security, error) {
	var errs []error
	s := Security{AuthMode: strings.ToLower(strings.TrimSpace(*f.auth)), CookieSecure: strings.ToLower(strings.TrimSpace(*f.cookie))}
	if s.AuthMode != AuthEnabled && s.AuthMode != AuthDisabledForLocal {
		errs = append(errs, fmt.Errorf("config: auth %q: want %s or %s", *f.auth, AuthEnabled, AuthDisabledForLocal))
	}
	switch s.CookieSecure {
	case CookieSecureAuto, CookieSecureAlways, CookieSecureNever:
	default:
		errs = append(errs, fmt.Errorf("config: cookie-secure %q: want auto, true or false", *f.cookie))
	}
	for o := range strings.SplitSeq(*f.origins, ",") {
		if o = strings.TrimSpace(o); o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
			errs = append(errs, fmt.Errorf("config: trusted origin %q: want scheme://host[:port], e.g. https://media.example.org", o))
			continue
		}
		s.TrustedOrigins = append(s.TrustedOrigins, u.Scheme+"://"+u.Host)
	}
	for h := range strings.SplitSeq(*f.hosts, ",") {
		if h = strings.TrimSpace(h); h == "" {
			continue
		}
		n, ok := normalHost(h)
		if !ok {
			errs = append(errs, fmt.Errorf("config: allowed host %q: want a host name such as media.example.org (no scheme, port or path)", h))
			continue
		}
		if !slices.Contains(s.AllowedHosts, n) {
			s.AllowedHosts = append(s.AllowedHosts, n)
		}
	}
	for p := range strings.SplitSeq(*f.proxies, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		pfx, err := parsePrefix(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("config: trusted proxy %q: want an IP address or a CIDR range such as 172.18.0.0/16", p))
			continue
		}
		s.TrustedProxies = append(s.TrustedProxies, pfx)
	}
	b := func(name, v string) bool {
		x, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			errs = append(errs, fmt.Errorf("config: %s %q: want true or false", name, v))
		}
		return x
	}
	s.Outbound = netx.Config{
		Integration: netx.Policy{AllowLoopback: b("outbound-allow-loopback", *f.loopback), AllowPrivate: b("outbound-allow-private", *f.private)},
	}
	if b("webhook-allow-private", *f.webhook) {
		s.Outbound.Webhook = netx.Policy{AllowLoopback: true, AllowPrivate: true}
	}
	return s, errors.Join(errs...)
}

// parsePrefix reads "10.0.0.1" or "10.0.0.0/8".
func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() {
			return netip.Prefix{}, errors.New("IPv4-mapped prefix")
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap().WithZone("")
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// normalHost validates a host name of GONEMA_ALLOWED_HOSTS: lower case,
// without trailing dot.
func normalHost(h string) (string, bool) {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if h == "" || len(h) > 253 {
		return "", false
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
				return "", false
			}
		}
	}
	return h, true
}

// ProxyTrusted reports whether a is one of the trusted reverse proxies.
func (s *Security) ProxyTrusted(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	for _, p := range s.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (s *Security) logAttrs() []any {
	proxies := make([]string, len(s.TrustedProxies))
	for i, p := range s.TrustedProxies {
		proxies[i] = p.String()
	}
	return []any{
		slog.String("auth", s.AuthMode),
		slog.String("cookie_secure", s.CookieSecure),
		slog.String("allowed_hosts", strings.Join(s.AllowedHosts, ",")),
		slog.String("trusted_proxies", strings.Join(proxies, ",")),
		slog.Bool("outbound_allow_loopback", s.Outbound.Integration.AllowLoopback),
		slog.Bool("outbound_allow_private", s.Outbound.Integration.AllowPrivate),
		slog.Bool("webhook_allow_private", s.Outbound.Webhook.AllowPrivate),
	}
}

package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
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
//	GONEMA_OUTBOUND_ALLOW_LOOPBACK integrations may reach 127.0.0.0/8 and ::1; default true.
//	GONEMA_OUTBOUND_ALLOW_PRIVATE  integrations may reach private ranges; default true.
//	GONEMA_WEBHOOK_ALLOW_PRIVATE   webhooks may reach loopback and private ranges;
//	                               default false (public only).
const (
	EnvAuth                  = "GONEMA_AUTH"
	EnvCookieSecure          = "GONEMA_COOKIE_SECURE"
	EnvTrustedOrigins        = "GONEMA_TRUSTED_ORIGINS"
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
	Outbound       netx.Config
}

// DefaultSecurity is: authentication on, automatic Secure cookie, integrations
// reach loopback and LAN, webhooks public addresses only.
func DefaultSecurity() Security {
	return Security{AuthMode: AuthEnabled, CookieSecure: CookieSecureAuto, Outbound: netx.DefaultConfig()}
}

type securityFlags struct {
	auth, cookie, origins, loopback, private, webhook *string
}

func newSecurityFlags(fs *flag.FlagSet, str func(name, fallback string) string) *securityFlags {
	return &securityFlags{
		auth: fs.String("auth", str(EnvAuth, AuthEnabled),
			"authentication: enabled, or disabled_for_local to skip the login for loopback and private addresses ("+EnvAuth+")"),
		cookie: fs.String("cookie-secure", str(EnvCookieSecure, CookieSecureAuto),
			"Secure attribute of the session cookie: auto, true or false ("+EnvCookieSecure+")"),
		origins: fs.String("trusted-origins", str(EnvTrustedOrigins, ""),
			"extra origins accepted by the cross-origin check, comma-separated ("+EnvTrustedOrigins+")"),
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

func (s *Security) logAttrs() []any {
	return []any{
		slog.String("auth", s.AuthMode),
		slog.String("cookie_secure", s.CookieSecure),
		slog.Bool("outbound_allow_loopback", s.Outbound.Integration.AllowLoopback),
		slog.Bool("outbound_allow_private", s.Outbound.Integration.AllowPrivate),
		slog.Bool("webhook_allow_private", s.Outbound.Webhook.AllowPrivate),
	}
}

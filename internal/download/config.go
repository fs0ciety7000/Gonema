package download

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/netx"
)

// DefaultTimeout bounds each HTTP request to a client.
const DefaultTimeout = 30 * time.Second

// Config is the connection setting of a download client (download_clients row
// with its decrypted secrets). Only the fields relevant to a kind are used.
type Config struct {
	Host    string // host name or IP, without scheme or path
	Port    int    // 0 selects the kind's default port
	UseTLS  bool   // https
	URLBase string // path prefix when the client sits behind a reverse proxy, e.g. "/sabnzbd"

	Username string // NZBGet, qBittorrent, Transmission
	Password string // NZBGet, qBittorrent, Deluge (Web UI password), Transmission
	APIKey   string // SABnzbd

	Category string // category (label) given to Gonema's downloads; empty for none

	// Directory is the Transmission download directory of Gonema's downloads;
	// when set, each category gets its own sub-directory. Empty keeps the
	// client's default directory.
	Directory string

	// HTTPClient replaces the default client (tests, custom transports). When it
	// is set, InsecureSkipVerify must be false: configure TLS on it instead.
	HTTPClient *http.Client
	Timeout    time.Duration // per request, default DefaultTimeout

	// InsecureSkipVerify disables the verification of the client's TLS
	// certificate. It is an explicit opt-in for self-signed certificates on a
	// trusted network: it allows a man in the middle to read the credentials.
	InsecureSkipVerify bool
}

// BaseURL returns scheme://host:port/urlbase (no trailing slash).
func (c *Config) BaseURL(defaultPort int) (*url.URL, error) {
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return nil, fmt.Errorf("download: %w: host missing", ErrInvalidRequest)
	}
	if strings.ContainsAny(host, "/?#@ ") || strings.Contains(host, "://") {
		return nil, fmt.Errorf("download: %w: host must be a name or an IP address, without scheme, path or credentials", ErrInvalidRequest)
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	port := c.Port
	if port == 0 {
		port = defaultPort
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("download: %w: port out of range", ErrInvalidRequest)
	}
	scheme := "http"
	if c.UseTLS {
		scheme = "https"
	}
	base := strings.Trim(strings.TrimSpace(c.URLBase), "/")
	if strings.ContainsAny(base, "?#") || strings.Contains(base, "..") {
		return nil, fmt.Errorf("download: %w: URL base must be a plain path", ErrInvalidRequest)
	}
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	if base != "" {
		u.Path = "/" + base
	}
	return u, nil
}

// Client returns the HTTP client to use.
func (c *Config) Client() (*http.Client, error) {
	if c.HTTPClient != nil {
		if c.InsecureSkipVerify {
			return nil, errors.New("download: InsecureSkipVerify cannot be combined with a custom HTTPClient")
		}
		return c.HTTPClient, nil
	}
	// Destinations are checked by netx (integration policy: loopback and LAN
	// allowed by default, link-local and metadata addresses never).
	tr := netx.NewTransport(netx.Integration)
	if c.InsecureSkipVerify {
		// security: explicit, documented opt-in for self-signed certificates.
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} //nolint:gosec // opt-in
	}
	// No global timeout: each request gets its own (Config.Timeout). Redirects
	// are not followed: a client API never redirects, a proxy login page does.
	return &http.Client{Transport: netx.Wrap(tr), CheckRedirect: netx.NoRedirect}, nil
}

// RequestTimeout returns the per-request timeout.
func (c *Config) RequestTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

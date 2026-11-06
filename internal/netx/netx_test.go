package netx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCheckAddr(t *testing.T) {
	lan := Policy{AllowLoopback: true, AllowPrivate: true}
	public := Policy{}
	tests := []struct {
		ip           string
		lan, public  bool // allowed under each policy
		wantInReason string
	}{
		{"93.184.216.34", true, true, ""},
		{"2606:4700::6810:85e5", true, true, ""},
		{"127.0.0.1", true, false, "loopback"},
		{"127.8.9.10", true, false, "loopback"},
		{"::1", true, false, "loopback"},
		{"::ffff:127.0.0.1", true, false, "loopback"}, // IPv4-mapped
		{"10.0.0.5", true, false, "private"},
		{"172.16.3.4", true, false, "private"},
		{"192.168.1.20", true, false, "private"},
		{"100.101.102.103", true, false, "private"}, // CGNAT / Tailscale
		{"fd12:3456::1", true, false, "private"},
		{"::ffff:192.168.1.1", true, false, "private"},
		{"64:ff9b::c0a8:0101", true, false, "private"},    // NAT64 of 192.168.1.1
		{"2002:c0a8:0101::1", true, false, "private"},     // 6to4 of 192.168.1.1
		{"64:ff9b:1::c0a8:0101", true, false, "private"},  // local-use NAT64, /96 form, of 192.168.1.1
		{"64:ff9b:1::5db8:d822", true, true, ""},          // local-use NAT64, /96 form, of 93.184.216.34
		{"64:ff9b:1:c0a8:1:100::", false, false, "NAT64"}, // local-use NAT64, /48 form: refused
		{"64:ff9b:1:ffff::a9fe:a9fe", false, false, "NAT64"},
		{"169.254.169.254", false, false, "metadata"},
		{"::ffff:169.254.169.254", false, false, "metadata"},
		{"64:ff9b::a9fe:a9fe", false, false, "metadata"}, // NAT64 of 169.254.169.254
		{"2002:a9fe:a9fe::", false, false, "metadata"},   // 6to4 of 169.254.169.254
		{"169.254.170.2", false, false, "metadata"},
		{"fd00:ec2::254", false, false, "metadata"},
		{"100.100.100.200", false, false, "metadata"},
		{"168.63.129.16", false, false, "metadata"},
		{"169.254.1.1", false, false, "link-local"},
		{"fe80::1", false, false, "link-local"},
		{"fe80::1%eth0", false, false, "link-local"},
		{"0.0.0.0", false, false, "unspecified"},
		{"::", false, false, "unspecified"},
		{"0.1.2.3", false, false, "reserved"},
		{"224.0.0.1", false, false, "multicast"},
		{"239.255.255.250", false, false, "multicast"},
		{"ff02::1", false, false, "multicast"},
		{"255.255.255.255", false, false, "reserved"},
		{"240.0.0.1", false, false, "reserved"},
		{"::7f00:1", false, false, "reserved"}, // IPv4-compatible ::127.0.0.1
		{"2001:db8::1", false, false, "reserved"},
	}
	for _, tt := range tests {
		ip := netip.MustParseAddr(tt.ip)
		for _, c := range []struct {
			p     Policy
			allow bool
		}{{lan, tt.lan}, {public, tt.public}} {
			err := CheckAddr(c.p, ip)
			if (err == nil) != c.allow {
				t.Errorf("CheckAddr(%+v, %s) = %v, want allowed=%v", c.p, tt.ip, err, c.allow)
				continue
			}
			if err != nil {
				if !errors.Is(err, ErrBlocked) || !strings.Contains(err.Error(), tt.wantInReason) {
					t.Errorf("CheckAddr(%s) error %q, want ErrBlocked mentioning %q", tt.ip, err, tt.wantInReason)
				}
			}
		}
	}
}

func FuzzCheckAddr(f *testing.F) {
	for _, s := range []string{"127.0.0.1", "::ffff:169.254.169.254", "64:ff9b::a9fe:a9fe", "8.8.8.8", "fe80::1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ip, err := netip.ParseAddr(s)
		if err != nil {
			return
		}
		// Whatever the encoding, a link-local IPv4 target is never reachable.
		if v4 := ip.Unmap(); v4.Is4() && netip.MustParsePrefix("169.254.0.0/16").Contains(v4) {
			if CheckAddr(Policy{AllowLoopback: true, AllowPrivate: true}, ip) == nil {
				t.Fatalf("%s allowed", s)
			}
		}
	})
}

func withConfig(t *testing.T, c Config) {
	t.Helper()
	old := Current()
	Configure(c)
	t.Cleanup(func() { Configure(old) })
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, c *http.Client, u string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func TestClientPolicyAtDialTime(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	ts := okServer(t)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))

	// Default: integrations reach loopback, webhooks do not.
	withConfig(t, DefaultConfig())
	if body, err := get(t, NewClient(Integration), ts.URL); err != nil || body != "ok" {
		t.Fatalf("integration to loopback: %q %v", body, err)
	}
	_, err := get(t, NewClient(Webhook), ts.URL)
	var be *BlockedError
	if !errors.As(err, &be) || !strings.Contains(err.Error(), "GONEMA_WEBHOOK_ALLOW_PRIVATE") {
		t.Fatalf("webhook to loopback: %v", err)
	}
	// A host name is checked after resolution: "localhost" is not a loophole.
	if _, err := get(t, NewClient(Webhook), "http://localhost:"+port); !errors.Is(err, ErrBlocked) {
		t.Fatalf("webhook to localhost: %v", err)
	}
	// The opt-in.
	withConfig(t, Config{Integration: DefaultConfig().Integration, Webhook: Policy{AllowLoopback: true, AllowPrivate: true}})
	if body, err := get(t, NewClient(Webhook), ts.URL); err != nil || body != "ok" {
		t.Fatalf("webhook with opt-in: %q %v", body, err)
	}
	// The policy is read at each connection: clients built earlier follow it.
	c := NewClient(Integration)
	c.Transport.(*guard).next.(*http.Transport).DisableKeepAlives = true
	withConfig(t, Config{Integration: Policy{}})
	if _, err := get(t, c, ts.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("integration after the loopback opt-out: %v", err)
	}
	// Metadata addresses are refused before any packet is sent.
	withConfig(t, Config{Integration: Policy{AllowLoopback: true, AllowPrivate: true}})
	if _, err := get(t, NewClient(Integration), "http://169.254.169.254/latest/meta-data/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("metadata: %v", err)
	}
	if _, err := get(t, NewClient(Integration), "http://[::ffff:169.254.169.254]/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("mapped metadata: %v", err)
	}
	if _, err := get(t, NewClient(Integration), "http://0.0.0.0:"+port); !errors.Is(err, ErrBlocked) {
		t.Fatalf("0.0.0.0: %v", err)
	}
}

func TestRedirectsRechecked(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	withConfig(t, DefaultConfig())
	target := okServer(t)
	hops := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/meta":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		case "/file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/loop":
			hops++
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			http.Redirect(w, r, target.URL, http.StatusFound)
		}
	}))
	defer ts.Close()
	c := NewClient(Integration)
	if body, err := get(t, c, ts.URL+"/ok"); err != nil || body != "ok" {
		t.Fatalf("allowed redirect: %q %v", body, err)
	}
	if _, err := get(t, c, ts.URL+"/meta"); !errors.Is(err, ErrBlocked) {
		t.Errorf("redirect to metadata: %v", err)
	}
	if _, err := get(t, c, ts.URL+"/file"); !errors.Is(err, ErrBlocked) {
		t.Errorf("redirect to file: %v", err)
	}
	if _, err := get(t, c, ts.URL+"/loop"); err == nil || hops > MaxRedirects+1 {
		t.Errorf("redirect loop: %v after %d hops", err, hops)
	}
	if _, err := get(t, c, "ftp://example.com/x"); err == nil {
		t.Error("ftp accepted")
	}
}

func TestResponseCap(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	withConfig(t, DefaultConfig())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 100))
	}))
	defer ts.Close()
	c := &http.Client{Transport: &guard{next: NewTransport(Integration), max: 10}}
	if _, err := get(t, c, ts.URL); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("cap: %v", err)
	}
}

func TestProxyTargetChecked(t *testing.T) {
	withConfig(t, Config{Integration: Policy{}, Webhook: Policy{}})
	var proxied int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxied++
		_, _ = io.WriteString(w, "via proxy")
	}))
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)
	// The proxy (on loopback) is reachable; a private target behind it is not.
	c := &http.Client{Transport: Wrap(newTransport(Webhook, func(*http.Request) (*url.URL, error) { return pu, nil }))}
	if body, err := get(t, c, "http://93.184.216.34/"); err != nil || body != "via proxy" {
		t.Fatalf("public target through the proxy: %q %v", body, err)
	}
	if _, err := get(t, c, "http://192.168.1.1/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("private target through the proxy: %v", err)
	}
	if proxied != 1 {
		t.Errorf("proxied = %d", proxied)
	}
}

// TestProxyDialOnlyForProxiedRequests: the unchecked dial to the proxy is only
// for requests the Proxy function sent to it; a direct request to the same
// address (NO_PROXY, loopback targets) is checked like any other.
func TestProxyDialOnlyForProxiedRequests(t *testing.T) {
	withConfig(t, Config{Integration: Policy{}, Webhook: Policy{}})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "proxy")
	}))
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)
	env := func(r *http.Request) (*url.URL, error) {
		if r.URL.Hostname() == pu.Hostname() { // like ProxyFromEnvironment: no proxy for loopback
			return nil, nil
		}
		return pu, nil
	}
	c := &http.Client{Transport: Wrap(newTransport(Webhook, env))}
	if body, err := get(t, c, "http://93.184.216.34/"); err != nil || body != "proxy" {
		t.Fatalf("proxied: %q %v", body, err)
	}
	if _, err := get(t, c, proxy.URL+"/admin"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("direct request to the proxy address: %v, want blocked", err)
	}
	// Without Wrap there is no mark: the proxy connection itself is checked.
	raw := &http.Client{Transport: newTransport(Webhook, func(*http.Request) (*url.URL, error) { return pu, nil })}
	if _, err := get(t, raw, "http://93.184.216.34/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("unwrapped transport: %v, want blocked", err)
	}
}

func TestPublicClass(t *testing.T) {
	withConfig(t, Config{Integration: Policy{AllowLoopback: true, AllowPrivate: true}, Webhook: Policy{AllowLoopback: true, AllowPrivate: true}})
	if p := Public.Policy(); p.AllowLoopback || p.AllowPrivate {
		t.Fatalf("public policy = %+v", p)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer ts.Close()
	if _, err := get(t, NewClient(Public), ts.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("public client to loopback: %v", err)
	}
}

func TestScope(t *testing.T) {
	withConfig(t, DefaultConfig())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer ts.Close()
	c := NewClient(Integration)
	fetch := func(scopeHost string) error {
		sc, err := ScopeOfHost(context.Background(), scopeHost)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(WithScope(context.Background(), sc), "GET", ts.URL, http.NoBody)
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	if err := fetch("93.184.216.34"); !errors.Is(err, ErrBlocked) {
		t.Errorf("public service, local link: %v", err)
	}
	if err := fetch("127.0.0.2"); !errors.Is(err, ErrBlocked) {
		t.Errorf("another local service: %v", err)
	}
	if err := fetch("127.0.0.1"); err != nil {
		t.Errorf("same local service: %v", err)
	}
	if err := CheckHost(WithScope(context.Background(), &Scope{}), Integration, "127.0.0.1"); !errors.Is(err, ErrBlocked) {
		t.Errorf("CheckHost: %v", err)
	}
}

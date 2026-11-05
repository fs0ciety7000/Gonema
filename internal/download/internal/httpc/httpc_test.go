package httpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/download"
)

const secret = "p@ss word/+="

func cfgFor(t *testing.T, srvURL string) *download.Config {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	return &download.Config{Host: u.Hostname(), Port: port, UseTLS: u.Scheme == "https", APIKey: secret}
}

func TestTLS(t *testing.T) {
	t.Parallel()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	t.Cleanup(tlsSrv.Close)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	t.Cleanup(plain.Close)

	t.Run("self-signed refused by default", func(t *testing.T) {
		t.Parallel()
		r, err := New(download.KindSABnzbd, cfgFor(t, tlsSrv.URL), 1)
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.Do(context.Background(), &Request{Op: "test", Path: "api", Query: url.Values{"apikey": {secret}}})
		if !errors.Is(err, download.ErrTLS) {
			t.Fatalf("err = %v, want ErrTLS", err)
		}
		if strings.Contains(err.Error(), "p@ss") || strings.Contains(err.Error(), url.QueryEscape(secret)) {
			t.Fatalf("secret leaked: %v", err)
		}
	})
	t.Run("explicit opt-in accepts it", func(t *testing.T) {
		t.Parallel()
		cfg := cfgFor(t, tlsSrv.URL)
		cfg.InsecureSkipVerify = true
		r, err := New(download.KindSABnzbd, cfg, 1)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := r.Do(context.Background(), &Request{Op: "test", Path: "api"})
		if err != nil || string(resp.Body) != "ok" {
			t.Fatalf("Do = %v, %v", resp, err)
		}
	})
	t.Run("https against plain http", func(t *testing.T) {
		t.Parallel()
		cfg := cfgFor(t, plain.URL)
		cfg.UseTLS = true
		r, err := New(download.KindSABnzbd, cfg, 1)
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.Do(context.Background(), &Request{Op: "test", Path: "api"})
		if !errors.Is(err, download.ErrTLS) {
			t.Fatalf("err = %v, want ErrTLS", err)
		}
	})
	t.Run("opt-in with custom client refused", func(t *testing.T) {
		t.Parallel()
		cfg := cfgFor(t, tlsSrv.URL)
		cfg.InsecureSkipVerify = true
		cfg.HTTPClient = tlsSrv.Client()
		if _, err := New(download.KindSABnzbd, cfg, 1); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestUnreachableRedacted(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	r, err := New(download.KindSABnzbd, cfgFor(t, "http://"+addr), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Do(context.Background(), &Request{Op: "items", Path: "api", Query: url.Values{"apikey": {secret}}})
	if !errors.Is(err, download.ErrUnreachable) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), url.QueryEscape(secret)) || strings.Contains(err.Error(), "p@ss") {
		t.Fatalf("secret leaked: %v", err)
	}
}

func TestRedactEncodings(t *testing.T) {
	t.Parallel()
	r := &Requester{}
	r.AddSecret(secret)
	r.AddSecret("")
	in := "raw " + secret + " query " + url.QueryEscape(secret) + " path " + url.PathEscape(secret)
	if got := r.Redact(in); strings.Contains(got, "p@ss") || strings.Contains(got, "p%40ss") {
		t.Fatalf("Redact = %q", got)
	}
	err := r.Err("op", download.ErrRejected, "detail "+secret, errors.New("cause "+secret))
	if strings.Contains(err.Error(), "p@ss") {
		t.Fatalf("Err leaks: %v", err)
	}
}

func TestStatusError(t *testing.T) {
	t.Parallel()
	r := &Requester{Kind: download.KindNZBGet}
	tests := map[int]error{
		200: nil, 204: nil, 401: download.ErrAuth, 403: download.ErrAuth, 404: download.ErrURLBase,
		302: download.ErrURLBase, 500: download.ErrUnexpected, 418: download.ErrUnexpected,
	}
	for code, want := range tests {
		if err := r.StatusError("op", &Response{Status: code}); !errors.Is(err, want) || (want == nil) != (err == nil) {
			t.Errorf("StatusError(%d) = %v, want %v", code, err, want)
		}
	}
}

func TestBodyLimit(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		buf := make([]byte, 1<<20)
		for range MaxBodyBytes/len(buf) + 1 {
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	r, err := New(download.KindSABnzbd, cfgFor(t, srv.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Do(context.Background(), &Request{Op: "items"}); !errors.Is(err, download.ErrUnexpected) {
		t.Fatalf("err = %v", err)
	}
}

func TestNoRedirect(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	r, err := New(download.KindSABnzbd, cfgFor(t, srv.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.Do(context.Background(), &Request{Op: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.StatusError("test", resp); !errors.Is(err, download.ErrURLBase) {
		t.Fatalf("err = %v", err)
	}
}

func TestMultipart(t *testing.T) {
	t.Parallel()
	body, ct, err := Multipart([]Field{{"a", "1"}}, File{Field: "f", Filename: "../../x\"\r\n.nzb", Data: []byte("data")})
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(strings.NewReader(string(body)), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if form.Value["a"][0] != "1" || form.File["f"][0].Filename != "x.nzb" {
		t.Fatalf("form = %v %v", form.Value, form.File["f"][0].Filename)
	}
}

func TestNum(t *testing.T) {
	t.Parallel()
	var v struct {
		A, B, C, D Num
	}
	if err := json.Unmarshal([]byte(`{"A":"1277.65","B":42,"C":null,"D":"x"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != 1277.65 || v.B != 42 || v.C != 0 || v.D != 0 {
		t.Fatalf("v = %+v", v)
	}
	if Num(1e30).Int64() <= 0 || Num(-1e30).Int64() >= 0 {
		t.Fatal("Int64 must saturate")
	}
}

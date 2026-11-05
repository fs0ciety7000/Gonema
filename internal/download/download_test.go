package download

import (
	"crypto/sha1" //nolint:gosec // BEP 3 info hash
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/decision"
)

func TestPathMapper(t *testing.T) {
	t.Parallel()
	m := NewPathMapper([]PathMapping{
		{Host: "nas", RemotePath: "/downloads/", LocalPath: "/mnt/nas/downloads/"},
		{Host: "nas", RemotePath: "/downloads/movies", LocalPath: "/srv/movies"},
		{Host: "NAS.lan.", RemotePath: "/data", LocalPath: "/data2"},
		{Host: "win", RemotePath: `D:\Downloads\`, LocalPath: "/mnt/win/downloads"},
		{Host: "win", RemotePath: `\\server\share\dl`, LocalPath: `Z:\dl`},
		{Host: "linux", RemotePath: "/complete", LocalPath: `C:\Media\complete\`},
		{Host: "root", RemotePath: "/", LocalPath: "/mnt/root"},
		{Host: "", RemotePath: "/x", LocalPath: "/y"},    // ignored
		{Host: "empty", RemotePath: "", LocalPath: "/y"}, // ignored
	})
	tests := []struct {
		name, host, remote, want string
		ok                       bool
	}{
		{"prefix", "nas", "/downloads/tv/Show", "/mnt/nas/downloads/tv/Show", true},
		{"longest wins", "nas", "/downloads/movies/Film (2020)/a.mkv", "/srv/movies/Film (2020)/a.mkv", true},
		{"exact", "nas", "/downloads/movies", "/srv/movies", true},
		{"exact with slash", "nas", "/downloads/movies/", "/srv/movies", true},
		{"component boundary", "nas", "/downloads2/x", "/downloads2/x", false},
		{"host case and dot", "nas.lan", "/data/a", "/data2/a", true},
		{"other host", "other", "/downloads/x", "/downloads/x", false},
		{"windows remote", "win", `d:\downloads\Film\a.mkv`, "/mnt/win/downloads/Film/a.mkv", true},
		{"windows remote slashes", "win", `D:/Downloads/Film`, "/mnt/win/downloads/Film", true},
		{"unc", "win", `\\SERVER\share\dl\Film`, `Z:\dl\Film`, true},
		{"unix is case sensitive", "nas", "/Downloads/x", "/Downloads/x", false},
		{"windows local", "linux", "/complete/Film/a.mkv", `C:\Media\complete\Film\a.mkv`, true},
		{"dot components", "nas", "/downloads/./tv//x", "/mnt/nas/downloads/tv/x", true},
		{"parent escape refused", "nas", "/downloads/../etc/passwd", "/downloads/../etc/passwd", false},
		{"root mapping", "root", "/a/b", "/mnt/root/a/b", true},
		{"empty", "nas", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := m.Map(tt.host, tt.remote)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("Map(%q, %q) = %q, %v; want %q, %v", tt.host, tt.remote, got, ok, tt.want, tt.ok)
			}
		})
	}
	var nilMapper *PathMapper
	if got, ok := nilMapper.Map("nas", "/a"); ok || got != "/a" {
		t.Fatal("nil mapper must be a no-op")
	}
}

func FuzzPathMapper(f *testing.F) {
	m := NewPathMapper([]PathMapping{
		{Host: "h", RemotePath: "/downloads", LocalPath: "/mnt/dl"},
		{Host: "h", RemotePath: `C:\dl`, LocalPath: "/mnt/c"},
	})
	f.Add("/downloads/a/b")
	f.Add(`C:\dl\x`)
	f.Fuzz(func(t *testing.T, remote string) {
		got, ok := m.Map("h", remote)
		if !ok {
			if got != remote {
				t.Fatalf("unmapped path changed: %q -> %q", remote, got)
			}
			return
		}
		if !strings.HasPrefix(got, "/mnt/dl") && !strings.HasPrefix(got, "/mnt/c") {
			t.Fatalf("%q mapped outside the local prefixes: %q", remote, got)
		}
		for c := range strings.SplitSeq(got, "/") {
			if c == ".." {
				t.Fatalf("%q mapped to a climbing path %q", remote, got)
			}
		}
	})
}

func TestInfoHash(t *testing.T) {
	t.Parallel()
	info := "d6:lengthi1000e4:name9:movie.mkv12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaae"
	torrent := []byte("d8:announce23:http://tracker/announce13:creation datei1700000000e4:info" + info + "e")
	sum := sha1.Sum([]byte(info)) //nolint:gosec // BEP 3
	want := hex.EncodeToString(sum[:])
	got, err := InfoHash(torrent)
	if err != nil || got != want {
		t.Fatalf("InfoHash = %q, %v; want %q", got, err, want)
	}
	for name, bad := range map[string]string{
		"empty":       "",
		"not a dict":  "l4:infoe",
		"no info":     "d8:announce3:urle",
		"info list":   "d4:infol1:aee",
		"truncated":   "d4:infod4:name",
		"bad length":  "d4:infod4:name99:xee",
		"huge length": "d4:infod4:name99999999999999999999:xee",
	} {
		if _, err := InfoHash([]byte(bad)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func FuzzInfoHash(f *testing.F) {
	f.Add([]byte("d4:infod4:name1:xee"))
	f.Add([]byte("d1:ai1e4:infod1:bli1ei2eeee"))
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := InfoHash(b)
		if err == nil && len(h) != 40 {
			t.Fatalf("hash %q", h)
		}
	})
}

func TestMagnetInfoHash(t *testing.T) {
	t.Parallel()
	const hexHash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	tests := map[string]string{
		"magnet:?xt=urn:btih:C12FE1C06BBA254A9DC9F519B335AA7C1367A88A&dn=x": hexHash,
		"MAGNET:?dn=x&xt=urn:btih:" + hexHash:                               hexHash,
		"magnet:?xt=urn:btih:YEX6DQDLXISUVHOJ6UM3GNNKPQJWPKEK":              hexHash, // base32
		"magnet:?xt=urn:btmh:1220abcd":                                      "",
		"magnet:?xt=urn:btih:xyz":                                           "",
		"https://example.com":                                               "",
	}
	for in, want := range tests {
		got, err := MagnetInfoHash(in)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("MagnetInfoHash(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		p    decision.Protocol
		req  AddRequest
		ok   bool
	}{
		{"url", decision.Usenet, AddRequest{Protocol: decision.Usenet, URL: "https://x.example/a.nzb"}, true},
		{"data", decision.Torrent, AddRequest{Protocol: decision.Torrent, Data: []byte("d"), Filename: "a.torrent"}, true},
		{"magnet", decision.Torrent, AddRequest{Protocol: decision.Torrent, URL: "magnet:?xt=urn:btih:abc"}, true},
		{"magnet on usenet", decision.Usenet, AddRequest{Protocol: decision.Usenet, URL: "magnet:?xt=urn:btih:abc"}, false},
		{"protocol mismatch", decision.Usenet, AddRequest{Protocol: decision.Torrent, URL: "https://x.example"}, false},
		{"both", decision.Usenet, AddRequest{Protocol: decision.Usenet, URL: "https://x.example", Data: []byte("x"), Filename: "a"}, false},
		{"none", decision.Usenet, AddRequest{Protocol: decision.Usenet}, false},
		{"file scheme", decision.Usenet, AddRequest{Protocol: decision.Usenet, URL: "file:///etc/passwd"}, false},
		{"no filename", decision.Usenet, AddRequest{Protocol: decision.Usenet, Data: []byte("x")}, false},
		{"bad priority", decision.Usenet, AddRequest{Protocol: decision.Usenet, URL: "https://x.example", Priority: "urgent"}, false},
		{"negative ratio", decision.Torrent, AddRequest{Protocol: decision.Torrent, URL: "https://x.example", SeedRatio: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.req.Validate(tt.p)
			if (err == nil) != tt.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error %v does not match ErrInvalidRequest", err)
			}
		})
	}
}

func TestBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cfg  Config
		want string
	}{
		{Config{Host: "nas", UseTLS: true, URLBase: "/sabnzbd/"}, "https://nas:8080/sabnzbd"},
		{Config{Host: "10.0.0.2", Port: 9091}, "http://10.0.0.2:9091"},
		{Config{Host: "[::1]", Port: 1}, "http://[::1]:1"},
		{Config{Host: "::1"}, "http://[::1]:8080"},
	}
	for _, tt := range tests {
		u, err := tt.cfg.BaseURL(8080)
		if err != nil || u.String() != tt.want {
			t.Errorf("BaseURL(%+v) = %v, %v; want %s", tt.cfg, u, err, tt.want)
		}
	}
	for _, bad := range []Config{
		{}, {Host: "http://nas"}, {Host: "nas/x"}, {Host: "user@nas"}, {Host: "nas", Port: 70000},
		{Host: "nas", URLBase: "/a?b"}, {Host: "nas", URLBase: "/../x"},
	} {
		if _, err := bad.BaseURL(8080); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("BaseURL(%+v) = %v, want ErrInvalidRequest", bad, err)
		}
	}
}

func TestErrorMatching(t *testing.T) {
	t.Parallel()
	cause := errors.New("dial tcp: refused")
	err := NewError(KindSABnzbd, "test", ErrUnreachable, "connection refused", cause)
	if !errors.Is(err, ErrUnreachable) || !errors.Is(err, cause) || errors.Is(err, ErrAuth) {
		t.Fatalf("matching broken: %v", err)
	}
	want := "sabnzbd: test: cannot reach the download client: check the host, the port and that it is running (connection refused): dial tcp: refused"
	if err.Error() != want {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestKindProtocol(t *testing.T) {
	t.Parallel()
	if KindNZBGet.Protocol() != decision.Usenet || KindDeluge.Protocol() != decision.Torrent || Kind("x").Protocol() != "" {
		t.Fatal("Kind.Protocol")
	}
}

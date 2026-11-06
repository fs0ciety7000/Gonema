package secretbox_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/secretbox"
)

// keyFileName is the name of the temporary key files written by these tests.
const keyFileName = "master-keyfile"

func mustKey(t *testing.T) secretbox.Key {
	t.Helper()
	k, err := secretbox.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func mustBox(t *testing.T, k secretbox.Key) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New(k)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpen(t *testing.T) {
	b := mustBox(t, mustKey(t))
	ad := []byte("indexers/3")
	for _, pt := range [][]byte{nil, {}, []byte("api-key-123"), bytes.Repeat([]byte{0xff}, 4096)} {
		ct, err := b.Seal(pt, ad)
		if err != nil {
			t.Fatal(err)
		}
		if len(ct) != len(pt)+1+12+16 || ct[0] != 1 {
			t.Fatalf("ciphertext length %d / version %d", len(ct), ct[0])
		}
		got, err := b.Open(ct, ad)
		if err != nil || !bytes.Equal(got, pt) {
			t.Fatalf("Open = %q, %v; want %q", got, err, pt)
		}
	}
	c1, _ := b.Seal([]byte("x"), nil)
	c2, _ := b.Seal([]byte("x"), nil)
	if bytes.Equal(c1, c2) {
		t.Error("two seals of the same plaintext are equal: nonce reuse")
	}
}

func TestOpenRejects(t *testing.T) {
	k := mustKey(t)
	b := mustBox(t, k)
	ct, err := b.Seal([]byte("secret"), []byte("indexers/3"))
	if err != nil {
		t.Fatal(err)
	}
	flip := func(i int) []byte {
		c := bytes.Clone(ct)
		c[i] ^= 0x01
		return c
	}
	tests := []struct {
		name string
		box  *secretbox.Box
		ct   []byte
		ad   string
		want error
	}{
		{"wrong key", mustBox(t, mustKey(t)), ct, "indexers/3", secretbox.ErrDecrypt},
		{"other row (associated data)", b, ct, "indexers/4", secretbox.ErrDecrypt},
		{"tampered nonce", b, flip(1), "indexers/3", secretbox.ErrDecrypt},
		{"tampered body", b, flip(14), "indexers/3", secretbox.ErrDecrypt},
		{"tampered tag", b, flip(len(ct) - 1), "indexers/3", secretbox.ErrDecrypt},
		{"truncated", b, ct[:len(ct)-1], "indexers/3", secretbox.ErrDecrypt},
		{"too short", b, ct[:5], "indexers/3", secretbox.ErrDecrypt},
		{"empty", b, nil, "indexers/3", secretbox.ErrDecrypt},
		{"unknown version", b, append([]byte{7}, ct[1:]...), "indexers/3", secretbox.ErrUnknownVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt, err := tt.box.Open(tt.ct, []byte(tt.ad))
			if !errors.Is(err, tt.want) || pt != nil {
				t.Errorf("Open = %q, %v; want %v", pt, err, tt.want)
			}
		})
	}
}

func TestKeyring(t *testing.T) {
	k1, k2 := mustKey(t), mustKey(t)
	old := mustBox(t, k1)
	ct1, _ := old.Seal([]byte("v1"), nil)

	ring, err := secretbox.NewKeyring(2, map[byte]secretbox.Key{1: k1, 2: k2})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ring.Open(ct1, nil); err != nil || string(got) != "v1" {
		t.Errorf("open old value = %q, %v", got, err)
	}
	if !ring.NeedsRotation(ct1) {
		t.Error("NeedsRotation(v1) = false")
	}
	ct2, _ := ring.Seal([]byte("v2"), nil)
	if ct2[0] != 2 || ring.NeedsRotation(ct2) {
		t.Errorf("new value version = %d", ct2[0])
	}
	if _, err := old.Open(ct2, nil); !errors.Is(err, secretbox.ErrUnknownVersion) {
		t.Errorf("old box opening v2: %v", err)
	}
	for _, tc := range []struct {
		cur  byte
		keys map[byte]secretbox.Key
	}{{0, map[byte]secretbox.Key{0: k1}}, {3, map[byte]secretbox.Key{1: k1}}, {1, map[byte]secretbox.Key{1: k1, 0: k2}}} {
		if _, err := secretbox.NewKeyring(tc.cur, tc.keys); !errors.Is(err, secretbox.ErrInvalidKey) {
			t.Errorf("NewKeyring(%d) err = %v, want ErrInvalidKey", tc.cur, err)
		}
	}
}

func TestKeyNeverPrinted(t *testing.T) {
	k := mustKey(t)
	enc := secretbox.EncodeKey(k)
	hexed := fmt.Sprintf("%x", k[:])
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%d", "%q"} {
		out := fmt.Sprintf(verb, k)
		if strings.Contains(out, enc) || strings.Contains(out, hexed) || out != "[redacted]" {
			t.Errorf("Sprintf(%q, key) = %q, want [redacted]", verb, out)
		}
	}
	type cfg struct{ Key secretbox.Key }
	if out := fmt.Sprintf("%+v", cfg{k}); strings.Contains(out, hexed) || !strings.Contains(out, "[redacted]") {
		t.Errorf("struct print = %q", out)
	}
}

func noEnv(string) string { return "" }

func TestLoadKeyCreatesThenReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", keyFileName)
	k1, src, err := secretbox.LoadKey(path, noEnv)
	if err != nil || src != secretbox.SourceCreated {
		t.Fatalf("first LoadKey = %v, %v", src, err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
	k2, src, err := secretbox.LoadKey(path, noEnv)
	if err != nil || src != secretbox.SourceFile || k1 != k2 {
		t.Fatalf("second LoadKey = %v, %v, same key %v", src, err, k1 == k2)
	}
}

func TestLoadKeyFromEnv(t *testing.T) {
	k := mustKey(t)
	path := filepath.Join(t.TempDir(), keyFileName)
	env := func(name string) string {
		if name == secretbox.EnvMasterKey {
			return secretbox.EncodeKey(k)
		}
		return ""
	}
	got, src, err := secretbox.LoadKey(path, env)
	if err != nil || src != secretbox.SourceEnv || got != k {
		t.Fatalf("LoadKey(env) = %v, %v", src, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("key file created although the variable is set")
	}
}

func TestLoadKeyRejects(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	good := secretbox.EncodeKey(mustKey(t))
	tests := []struct {
		name string
		path string
		env  string
	}{
		{"world readable file", write("loose", good, 0o644), ""},
		{"short key in file", write("short", short, 0o600), ""},
		{"not base64 in file", write("garbage", "%%%not base64%%%", 0o600), ""},
		{"empty file", write("empty", "", 0o600), ""},
		{"directory", dir, ""},
		{"bad env", filepath.Join(dir, "unused"), "c2hvcnQ="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := func(string) string { return tt.env }
			if _, _, err := secretbox.LoadKey(tt.path, env); err == nil {
				t.Error("LoadKey succeeded, want error")
			} else if strings.Contains(err.Error(), good) {
				t.Errorf("error leaks the key: %v", err)
			}
		})
	}
}

func TestParseKeyRoundTrip(t *testing.T) {
	k := mustKey(t)
	got, err := secretbox.ParseKey([]byte("  " + secretbox.EncodeKey(k) + "\n"))
	if err != nil || got != k {
		t.Fatalf("ParseKey = %v", err)
	}
}

// FuzzOpen: arbitrary bytes never open, except the genuine ciphertext.
func FuzzOpen(f *testing.F) {
	var k secretbox.Key
	b, err := secretbox.New(k)
	if err != nil {
		f.Fatal(err)
	}
	ct, _ := b.Seal([]byte("seed"), nil)
	f.Add(ct)
	f.Add([]byte{1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := b.Open(data, nil); err == nil && !bytes.Equal(data, ct) {
			t.Fatalf("forged ciphertext opened: %x", data)
		}
	})
}

package secretbox

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// EnvMasterKey names the environment variable holding the base64 master key. When
// set it takes precedence over the key file.
const EnvMasterKey = "GONEMA_MASTER_KEY"

// KeySource tells where LoadKey found the key (for a startup log line; the key
// itself is never logged).
type KeySource string

// Key sources.
const (
	SourceEnv     KeySource = "env"
	SourceFile    KeySource = "file"
	SourceCreated KeySource = "created"
)

// LoadKey returns the master key: from the GONEMA_MASTER_KEY variable (looked up
// with getenv, usually os.Getenv) when set, else from the file at path, which is
// created with a new random key (mode 0600) if it does not exist.
//
// The key file holds the base64 key (std encoding) and a newline, the same form
// as the variable. A file readable by group or others is refused.
func LoadKey(path string, getenv func(string) string) (Key, KeySource, error) {
	if v := getenv(EnvMasterKey); v != "" {
		k, err := ParseKey([]byte(v))
		if err != nil {
			return Key{}, "", fmt.Errorf("secretbox: %s: %w", EnvMasterKey, err)
		}
		return k, SourceEnv, nil
	}
	k, err := readKeyFile(path)
	if err == nil {
		return k, SourceFile, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Key{}, "", err
	}
	k, err = createKeyFile(path)
	if err != nil {
		return Key{}, "", err
	}
	return k, SourceCreated, nil
}

// ParseKey decodes a base64 (standard, padded) master key.
func ParseKey(b []byte) (Key, error) {
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(b)))
	if err != nil {
		return Key{}, fmt.Errorf("%w: not valid base64", ErrInvalidKey) // no detail: it may quote the key
	}
	if len(raw) != KeySize {
		return Key{}, fmt.Errorf("%w: %d bytes, want %d", ErrInvalidKey, len(raw), KeySize)
	}
	var k Key
	copy(k[:], raw)
	clear(raw)
	return k, nil
}

// EncodeKey returns the base64 form of k, as stored in the key file.
func EncodeKey(k Key) string { return base64.StdEncoding.EncodeToString(k[:]) }

func readKeyFile(path string) (Key, error) {
	f, err := os.Open(path)
	if err != nil {
		return Key{}, fmt.Errorf("secretbox: open key file: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Key{}, fmt.Errorf("secretbox: stat key file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return Key{}, fmt.Errorf("secretbox: key file %s is not a regular file", path)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		return Key{}, fmt.Errorf("secretbox: key file %s has mode %04o, want 0600 (chmod 600 it)", path, perm)
	}
	buf := make([]byte, 256)
	n, err := f.Read(buf)
	if err != nil {
		return Key{}, fmt.Errorf("secretbox: read key file: %w", err)
	}
	k, err := ParseKey(buf[:n])
	clear(buf)
	if err != nil {
		return Key{}, fmt.Errorf("secretbox: key file %s: %w", path, err)
	}
	return k, nil
}

// createKeyFile writes a new key with O_EXCL (never overwrites an existing key)
// and syncs it, so that a crash cannot leave secrets sealed with a lost key.
func createKeyFile(path string) (Key, error) {
	k, err := NewKey()
	if err != nil {
		return Key{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Key{}, fmt.Errorf("secretbox: create key directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Key{}, fmt.Errorf("secretbox: create key file: %w", err)
	}
	_, werr := f.WriteString(EncodeKey(k) + "\n")
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(path)
		return Key{}, fmt.Errorf("secretbox: write key file: %w", err)
	}
	return k, nil
}

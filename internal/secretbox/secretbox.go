// Package secretbox encrypts the secrets stored in the database (indexer API keys,
// download client passwords...) with AES-256-GCM under the master key.
//
// Ciphertext format (stored as-is in the `secrets BLOB` columns):
//
//	version (1 byte) | nonce (12 bytes) | AES-GCM ciphertext and tag (16 bytes)
//
// The version byte identifies the key that sealed the value, so a later key
// rotation can open old values with the previous key while sealing new ones with
// the current key. Version 0 is never valid.
//
// Callers should pass associated data naming where the value lives (for example
// "indexers/3"): the ciphertext then fails to open if copied to another row.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize is the master key size: 32 bytes, AES-256.
const KeySize = 32

const (
	nonceSize = 12
	tagSize   = 16
	overhead  = 1 + nonceSize + tagSize
)

var (
	// ErrDecrypt is returned when a ciphertext does not open: wrong key, tampered
	// or truncated data, or different associated data. Deliberately vague.
	ErrDecrypt = errors.New("secretbox: cannot decrypt")
	// ErrUnknownVersion is returned for a ciphertext sealed with a key version
	// this Box does not hold.
	ErrUnknownVersion = errors.New("secretbox: unknown key version")
	// ErrInvalidKey is returned for a key of the wrong size or a bad version.
	ErrInvalidKey = errors.New("secretbox: invalid key")
)

// Key is a master key. Its String and GoString methods never reveal the bytes, so
// a key logged by mistake prints as "[redacted]".
type Key [KeySize]byte

// String implements fmt.Stringer without revealing the key.
func (Key) String() string { return "[redacted]" }

// GoString implements fmt.GoStringer without revealing the key.
func (Key) GoString() string { return "secretbox.Key{[redacted]}" }

// Format implements fmt.Formatter so that no verb (%x, %v, %d...) prints the key.
func (Key) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte("[redacted]")) }

// NewKey returns a random key from crypto/rand.
func NewKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, fmt.Errorf("secretbox: generate key: %w", err)
	}
	return k, nil
}

// Box seals with its current key and opens with any key it holds.
type Box struct {
	current byte
	aeads   map[byte]cipher.AEAD
}

// New returns a Box holding a single key, version 1.
func New(key Key) (*Box, error) {
	return NewKeyring(1, map[byte]Key{1: key})
}

// NewKeyring returns a Box sealing with keys[current] and opening with any key of
// keys (key rotation).
func NewKeyring(current byte, keys map[byte]Key) (*Box, error) {
	if _, ok := keys[current]; !ok || current == 0 {
		return nil, fmt.Errorf("%w: current version %d missing or zero", ErrInvalidKey, current)
	}
	b := &Box{current: current, aeads: make(map[byte]cipher.AEAD, len(keys))}
	for v, k := range keys {
		if v == 0 {
			return nil, fmt.Errorf("%w: version 0", ErrInvalidKey)
		}
		block, err := aes.NewCipher(k[:])
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
		}
		b.aeads[v] = aead
	}
	return b, nil
}

// Seal encrypts plaintext bound to the associated data ad (may be nil) with a
// fresh random nonce.
func (b *Box) Seal(plaintext, ad []byte) ([]byte, error) {
	out := make([]byte, 1+nonceSize, overhead+len(plaintext))
	out[0] = b.current
	if _, err := rand.Read(out[1 : 1+nonceSize]); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	return b.aeads[b.current].Seal(out, out[1:1+nonceSize], plaintext, ad), nil
}

// Open decrypts a value produced by Seal with the same associated data.
func (b *Box) Open(ciphertext, ad []byte) ([]byte, error) {
	if len(ciphertext) < overhead {
		return nil, ErrDecrypt
	}
	aead, ok := b.aeads[ciphertext[0]]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrUnknownVersion, ciphertext[0])
	}
	pt, err := aead.Open(nil, ciphertext[1:1+nonceSize], ciphertext[1+nonceSize:], ad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// NeedsRotation reports whether ciphertext was sealed with a key other than the
// current one (to re-seal during a key rotation).
func (b *Box) NeedsRotation(ciphertext []byte) bool {
	return len(ciphertext) == 0 || ciphertext[0] != b.current
}

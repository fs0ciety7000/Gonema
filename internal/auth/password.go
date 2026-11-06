package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id parameters. The defaults follow the second
// recommendation of RFC 9106 (section 4): 64 MiB, 3 passes, 4 lanes; about
// 0.1 s on a desktop CPU and under 1 s on a Raspberry Pi 4.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultParams are the production parameters.
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 4, SaltLen: 16, KeyLen: 32}

// Limits of the password inputs (argon2 accepts any length: the upper bound
// keeps a request from making the server hash megabytes).
const (
	MinPasswordLen = 10
	MaxPasswordLen = 256
)

var b64 = base64.RawStdEncoding

// hashPassword returns the PHC string of password.
func hashPassword(p Params, password string) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("auth: malformed password hash")

// verifyPassword reports whether password matches the PHC string encoded. The
// comparison of the derived keys is constant-time. The parameters come from
// the stored hash, so hashes made with older parameters keep working.
func verifyPassword(encoded, password string) (bool, error) {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key))) //nolint:gosec // len(key) <= 1024, checked by decodeHash
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

func decodeHash(encoded string) (p Params, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Params{}, nil, nil, errBadHash
	}
	for kv := range strings.SplitSeq(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseUint(v, 10, 32)
		if !ok || err != nil {
			return Params{}, nil, nil, errBadHash
		}
		switch k {
		case "m":
			p.Memory = uint32(n)
		case "t":
			p.Time = uint32(n)
		case "p":
			if n > 255 {
				return Params{}, nil, nil, errBadHash
			}
			p.Threads = uint8(n)
		default:
			return Params{}, nil, nil, errBadHash
		}
	}
	// Bounds: a tampered hash must not make a login allocate gigabytes.
	if p.Memory < 8 || p.Memory > 1<<20 || p.Time < 1 || p.Time > 16 || p.Threads < 1 {
		return Params{}, nil, nil, errBadHash
	}
	salt, err = b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return Params{}, nil, nil, errBadHash
	}
	key, err = b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 1024 {
		return Params{}, nil, nil, errBadHash
	}
	return p, salt, key, nil
}

// hashGate bounds the concurrent argon2 computations: each one holds Memory
// KiB, so a burst of logins must not exhaust the server's memory.
type hashGate chan struct{}

func (g hashGate) run(ctx context.Context, fn func()) error {
	select {
	case g <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-g }()
	fn()
	return nil
}

// FastParams are cheap parameters for tests only: never use them in production.
var FastParams = Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

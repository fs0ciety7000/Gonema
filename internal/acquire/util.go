package acquire

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store"
)

// marshalObject encodes m as a JSON object ("{}" for nil).
func marshalObject(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// jsonArray encodes a slice as a JSON array ("[]" for nil).
func jsonArray[T any](v []T) string {
	if v == nil {
		return "[]"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// decodeArray decodes a JSON array column; invalid content gives an empty slice.
func decodeArray[T any](s string) []T {
	var out []T
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return []T{}
	}
	return out
}

// normalizeTags trims, lower-cases and deduplicates tag labels.
func normalizeTags(tags []string) ([]string, error) {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if len(t) > 64 {
			return nil, invalid("tag %q is longer than 64 characters", t[:64])
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out, nil
}

func timePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := store.FromMillis(*ms)
	return &t
}

func millisPtr(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	ms := store.Millis(t)
	return &ms
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func ptr[T any](v T) *T { return &v }

// sensitiveParams are query parameters that carry credentials in indexer and
// tracker links (API keys, passkeys, RSS tokens, session ids).
var sensitiveParams = []string{
	"apikey", "api_key", "api-key", "key", "passkey", "pass", "password", "token", "auth", "authkey",
	"torrent_pass", "rsskey", "uid", "user", "username", "sid", "secret", "sig", "signature", "r",
}

// RedactURL returns raw without credentials: user info is dropped and
// credential-like query values are replaced by REDACTED. A value that is not
// an http(s) URL (magnet URI, GUID) is returned as "" unless keepOpaque is set
// and it contains no "?" (a plain identifier).
func RedactURL(raw string, keepOpaque bool) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		if keepOpaque && !strings.ContainsAny(raw, "?&=") && !strings.HasPrefix(strings.ToLower(raw), "magnet:") {
			return raw
		}
		return ""
	}
	u.User = nil
	u.Fragment = ""
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if slices.Contains(sensitiveParams, strings.ToLower(k)) {
				q[k] = []string{"REDACTED"}
			}
		}
		u.RawQuery = q.Encode()
	}
	// Some trackers put the passkey in the path (/rss/<passkey>/...): long
	// hexadecimal or base64-like path segments are redacted too.
	segs := strings.Split(u.Path, "/")
	for i, seg := range segs {
		if len(seg) >= 24 && isTokenLike(seg) {
			segs[i] = "REDACTED"
		}
	}
	u.Path = strings.Join(segs, "/")
	u.RawPath = ""
	return u.String()
}

func isTokenLike(s string) bool {
	for _, c := range s {
		alnum := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !alnum && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

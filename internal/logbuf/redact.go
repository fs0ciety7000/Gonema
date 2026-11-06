package logbuf

import (
	"regexp"
	"strings"
)

// Redacted replaces a secret value.
const Redacted = "[redacted]"

// sensitiveKeys are attribute keys (lower case, '-' and '.' read as '_')
// whose value is always replaced.
var sensitiveKeys = map[string]bool{
	"password": true, "passwd": true, "pass": true, "secret": true, "secrets": true, "token": true,
	"apikey": true, "api_key": true, "x_api_key": true, "authorization": true, "proxy_authorization": true,
	"cookie": true, "set_cookie": true, "session": true, "csrf": true, "csrf_token": true,
	"access_token": true, "refresh_token": true, "client_secret": true, "private_key": true,
	"master_key": true, "nzbkey": true, "passkey": true, "webhook_url": true,
}

// sensitiveSuffixes match keys such as "tmdb_api_key", "user_password".
var sensitiveSuffixes = []string{"_password", "_passwd", "_secret", "_token", "_apikey", "_api_key", "_key", "_passkey"}

// SensitiveKey reports whether an attribute key names a secret.
func SensitiveKey(k string) bool {
	k = strings.ToLower(k)
	if i := strings.LastIndexByte(k, '.'); i >= 0 { // group prefix
		k = k[i+1:]
	}
	k = strings.ReplaceAll(k, "-", "_")
	if sensitiveKeys[k] {
		return true
	}
	for _, s := range sensitiveSuffixes {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

// patterns find secrets inside free text (messages, error strings, URLs).
// Each keeps its group 1 and replaces the rest of the match.
var patterns = []*regexp.Regexp{
	// Query strings and form values: apikey=..., ?token=..., &password=...
	regexp.MustCompile(`(?i)((?:^|[?&;\s"'(,])(?:api[_-]?key|apikey|token|access[_-]?token|refresh[_-]?token|auth|password|passwd|pass|secret|client[_-]?secret|nzbkey|passkey|key|sig|signature)=)[^&\s"'),;]+`),
	// Headers written as text: "Authorization: Bearer xyz", "X-Api-Key: xyz", "Cookie: a=b".
	regexp.MustCompile(`(?i)((?:proxy-)?authorization\s*[:=]\s*)(?:(?:bearer|basic|digest|token)\s+)?[^\s,;"']+`),
	regexp.MustCompile(`(?i)((?:x-api-key|x-plex-token|x-emby-token|x-mediabrowser-token|x-transmission-session-id|cookie|set-cookie|x-csrf-token)\s*[:=]\s*)[^\r\n"']+`),
	// Bare bearer tokens.
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
	// JSON fields: "password":"...", "apiKey": "...".
	regexp.MustCompile(`(?i)("(?:password|passwd|secret|token|api[_-]?key|apikey|access[_-]?token|client[_-]?secret|authorization|cookie)"\s*:\s*)"(?:[^"\\]|\\.)*"`),
	// User information in URLs: scheme://user:password@host.
	regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+:)[^@\s/]+(@)`),
}

// Redact returns s with the secrets it contains replaced by [redacted].
func Redact(s string) string {
	if s == "" || !mayHoldSecret(s) {
		return s
	}
	for i, re := range patterns {
		if i == len(patterns)-1 { // URL user information: keep the '@'
			s = re.ReplaceAllString(s, "${1}"+Redacted+"${2}")
			continue
		}
		if i == 4 { // JSON field: keep a valid JSON string
			s = re.ReplaceAllString(s, `${1}"`+Redacted+`"`)
			continue
		}
		s = re.ReplaceAllString(s, "${1}"+Redacted)
	}
	return s
}

// mayHoldSecret is a cheap pre-filter: most log lines hold none of the
// characters the patterns need.
func mayHoldSecret(s string) bool {
	return strings.ContainsAny(s, "=:@") || strings.Contains(s, "earer ") || strings.Contains(s, "EARER ")
}

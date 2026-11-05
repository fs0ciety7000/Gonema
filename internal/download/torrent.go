package download

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // BitTorrent v1 info hashes are SHA-1 by specification (BEP 3)
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ErrNoInfoHash is returned when a torrent file or magnet URI carries no v1
// info hash.
var ErrNoInfoHash = errors.New("download: no BitTorrent v1 info hash found")

const maxBencodeDepth = 64

// InfoHash returns the lowercase hex BitTorrent v1 info hash (SHA-1 of the
// bencoded "info" dictionary, BEP 3) of a .torrent file.
func InfoHash(torrent []byte) (string, error) {
	if len(torrent) == 0 || torrent[0] != 'd' {
		return "", ErrNoInfoHash
	}
	i := 1
	for i < len(torrent) && torrent[i] != 'e' {
		key, next, err := bString(torrent, i)
		if err != nil {
			return "", err
		}
		end, err := bSkip(torrent, next, 0)
		if err != nil {
			return "", err
		}
		if string(key) == "info" {
			if torrent[next] != 'd' {
				return "", ErrNoInfoHash
			}
			sum := sha1.Sum(torrent[next:end]) //nolint:gosec // see import
			return hex.EncodeToString(sum[:]), nil
		}
		i = end
	}
	return "", ErrNoInfoHash
}

// MagnetInfoHash returns the lowercase hex v1 info hash of a magnet URI
// (xt=urn:btih: in hex or base32 form, BEP 9).
func MagnetInfoHash(uri string) (string, error) {
	if !IsMagnet(uri) {
		return "", ErrNoInfoHash
	}
	rest := uri[len("magnet:"):]
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[i+1:]
	}
	q, err := url.ParseQuery(rest)
	if err != nil {
		return "", ErrNoInfoHash
	}
	for _, xt := range q["xt"] {
		const p = "urn:btih:"
		if len(xt) <= len(p) || !strings.EqualFold(xt[:len(p)], p) {
			continue
		}
		h := xt[len(p):]
		switch len(h) {
		case 40:
			if b, err := hex.DecodeString(h); err == nil {
				return hex.EncodeToString(b), nil
			}
		case 32:
			if b, err := base32.StdEncoding.DecodeString(strings.ToUpper(h)); err == nil {
				return hex.EncodeToString(b), nil
			}
		}
	}
	return "", ErrNoInfoHash
}

var errBencode = errors.New("download: malformed torrent file")

// bString parses a byte string at i and returns it and the next offset.
func bString(b []byte, i int) (s []byte, next int, err error) {
	colon := bytes.IndexByte(b[i:], ':')
	if colon <= 0 || colon > 20 {
		return nil, 0, errBencode
	}
	n, err := strconv.Atoi(string(b[i : i+colon]))
	if err != nil || n < 0 {
		return nil, 0, errBencode
	}
	start := i + colon + 1
	if n > len(b)-start {
		return nil, 0, errBencode
	}
	return b[start : start+n], start + n, nil
}

// bSkip returns the offset just past the value starting at i.
func bSkip(b []byte, i, depth int) (int, error) {
	if i >= len(b) || depth > maxBencodeDepth {
		return 0, errBencode
	}
	switch c := b[i]; {
	case c == 'i':
		e := bytes.IndexByte(b[i:], 'e')
		if e < 2 {
			return 0, errBencode
		}
		return i + e + 1, nil
	case c == 'l' || c == 'd':
		i++
		for {
			if i >= len(b) {
				return 0, errBencode
			}
			if b[i] == 'e' {
				return i + 1, nil
			}
			if c == 'd' {
				_, next, err := bString(b, i)
				if err != nil {
					return 0, err
				}
				i = next
			}
			next, err := bSkip(b, i, depth+1)
			if err != nil {
				return 0, err
			}
			i = next
		}
	case c >= '0' && c <= '9':
		_, next, err := bString(b, i)
		return next, err
	}
	return 0, errBencode
}

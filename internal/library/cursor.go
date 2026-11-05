package library

import (
	"encoding/base64"
	"encoding/json"
)

func encodeCursor(c cursor) string {
	raw, err := json.Marshal(c)
	if err != nil { // cannot happen: plain struct
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor parses a page cursor; "" is the first page.
func decodeCursor(s, sort string) (cursor, error) {
	if s == "" {
		return cursor{Sort: sort}, nil
	}
	bad := invalid("cursor", "the page cursor is invalid or belongs to another sort order; start again from the first page")
	if len(s) > 512 {
		return cursor{}, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, bad
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil || c.Sort != sort || c.ID <= 0 {
		return cursor{}, bad
	}
	return c, nil
}

package metadata

import (
	"fmt"
	"time"
)

// Date is a calendar date without time of day or zone, stored as YYYY-MM-DD
// (docs/data-model.md). The zero value means "unknown".
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseDate reads "YYYY-MM-DD", or the date part of an RFC 3339 timestamp
// ("2012-08-28T00:00:00.000Z"). An empty string is the zero Date.
func ParseDate(s string) (Date, error) {
	if s == "" {
		return Date{}, nil
	}
	if len(s) > 10 && s[10] == 'T' {
		s = s[:10]
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("metadata: invalid date %q", s)
	}
	return DateOf(t), nil
}

// DateOf returns the calendar date of t in t's location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// IsZero reports whether d is unknown.
func (d Date) IsZero() bool { return d == Date{} }

// String returns "YYYY-MM-DD", or "" for the zero Date.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// Compare returns -1, 0 or +1. The zero Date sorts after every known date.
func (d Date) Compare(o Date) int {
	switch {
	case d == o:
		return 0
	case d.IsZero():
		return 1
	case o.IsZero():
		return -1
	}
	a := d.Year*10000 + int(d.Month)*100 + d.Day
	b := o.Year*10000 + int(o.Month)*100 + o.Day
	if a < b {
		return -1
	}
	return 1
}

// Before reports whether d is a known date strictly before o (o known).
func (d Date) Before(o Date) bool { return !d.IsZero() && !o.IsZero() && d.Compare(o) < 0 }

// AddDays returns d shifted by n days; the zero Date stays zero.
func (d Date) AddDays(n int) Date {
	if d.IsZero() {
		return d
	}
	return DateOf(time.Date(d.Year, d.Month, d.Day+n, 0, 0, 0, 0, time.UTC))
}

// MarshalText implements encoding.TextMarshaler ("" for the zero Date).
func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Date) UnmarshalText(b []byte) error {
	v, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

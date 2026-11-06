// Package ical writes iCalendar documents (RFC 5545) for the read-only
// calendar feed: all-day events, text escaping, line folding at 75 octets
// without splitting a UTF-8 sequence, CRLF line endings.
//
// The package is pure: no clock, no I/O beyond the writer it is given. The
// DTSTAMP of the events is passed by the caller.
package ical

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Calendar is a VCALENDAR.
type Calendar struct {
	ProdID      string        // e.g. "-//Gonema//Calendar 1.1//EN"
	Name        string        // X-WR-CALNAME
	Description string        // X-WR-CALDESC
	Refresh     time.Duration // REFRESH-INTERVAL and X-PUBLISHED-TTL; 0 omits them
	Events      []Event
}

// Event is an all-day VEVENT.
type Event struct {
	// UID must be stable across feeds for the same event, so that calendar
	// apps update it instead of duplicating it.
	UID         string
	Date        time.Time // the day (only year, month and day are used)
	Summary     string
	Description string
	URL         string
	Categories  []string
	Stamp       time.Time // DTSTAMP, written in UTC
}

// maxLine is the maximum length of a content line in octets, CRLF excluded.
const maxLine = 75

// Escape escapes a TEXT value: backslash, semicolon, comma and newlines
// (RFC 5545 §3.3.11). Carriage returns are dropped, other control characters
// (forbidden in TEXT) are replaced by a space.
func Escape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == ';':
			b.WriteString(`\;`)
		case r == ',':
			b.WriteString(`\,`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
		case r < 0x20 || r == 0x7f:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Fold splits a content line into lines of at most 75 octets, each
// continuation starting with a space, never inside a UTF-8 sequence. The
// result ends with CRLF.
func Fold(line string) string {
	var b strings.Builder
	b.Grow(len(line) + len(line)/maxLine*3 + 2)
	limit := maxLine
	n := 0
	for line != "" {
		_, size := utf8.DecodeRuneInString(line)
		if n+size > limit {
			b.WriteString("\r\n ")
			n, limit = 0, maxLine-1 // the leading space counts
		}
		b.WriteString(line[:size])
		n += size
		line = line[size:]
	}
	b.WriteString("\r\n")
	return b.String()
}

// Write writes the calendar.
func (c *Calendar) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)
	line := func(name, value string) {
		_, _ = bw.WriteString(Fold(name + ":" + value))
	}
	line("BEGIN", "VCALENDAR")
	line("VERSION", "2.0")
	line("PRODID", Escape(c.ProdID))
	line("CALSCALE", "GREGORIAN")
	line("METHOD", "PUBLISH")
	if c.Name != "" {
		line("X-WR-CALNAME", Escape(c.Name))
	}
	if c.Description != "" {
		line("X-WR-CALDESC", Escape(c.Description))
	}
	if c.Refresh > 0 {
		d := duration(c.Refresh)
		_, _ = bw.WriteString(Fold("REFRESH-INTERVAL;VALUE=DURATION:" + d))
		line("X-PUBLISHED-TTL", d)
	}
	for i := range c.Events {
		e := &c.Events[i]
		line("BEGIN", "VEVENT")
		line("UID", Escape(e.UID))
		line("DTSTAMP", e.Stamp.UTC().Format("20060102T150405Z"))
		day := time.Date(e.Date.Year(), e.Date.Month(), e.Date.Day(), 0, 0, 0, 0, time.UTC)
		_, _ = bw.WriteString(Fold("DTSTART;VALUE=DATE:" + day.Format("20060102")))
		_, _ = bw.WriteString(Fold("DTEND;VALUE=DATE:" + day.AddDate(0, 0, 1).Format("20060102")))
		line("SUMMARY", Escape(e.Summary))
		if e.Description != "" {
			line("DESCRIPTION", Escape(e.Description))
		}
		if e.URL != "" {
			_, _ = bw.WriteString(Fold("URL;VALUE=URI:" + e.URL))
		}
		if len(e.Categories) > 0 {
			cats := make([]string, len(e.Categories))
			for j, c := range e.Categories {
				cats[j] = Escape(c)
			}
			line("CATEGORIES", strings.Join(cats, ","))
		}
		line("TRANSP", "TRANSPARENT") // a release day does not make the user busy
		line("END", "VEVENT")
	}
	line("END", "VCALENDAR")
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("ical: write: %w", err)
	}
	return nil
}

// duration formats a positive duration as an RFC 5545 DURATION in hours and
// minutes (PT1H, PT30M, PT1H30M); at least one minute.
func duration(d time.Duration) string {
	m := max(int(d.Minutes()), 1)
	h, m := m/60, m%60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("PT%dH%dM", h, m)
	case h > 0:
		return fmt.Sprintf("PT%dH", h)
	}
	return fmt.Sprintf("PT%dM", m)
}

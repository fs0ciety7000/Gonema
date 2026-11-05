package health

import (
	"fmt"
	"strconv"
	"time"
)

// Wording helpers: messages are read by people, not parsed.

// plural returns "1 film", "3 films".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// duration renders d roughly: "less than a minute", "40 minutes", "3 hours",
// "2 days".
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute", "minutes")
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour", "hours")
	}
	return plural(int(d/(24*time.Hour)), "day", "days")
}

// clock renders t for someone reading at now: "21:30" today, "tomorrow at
// 09:00", else "Mon 9 Nov at 09:00".
func clock(t, now time.Time, loc *time.Location) string {
	t, now = t.In(loc), now.In(loc)
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	switch {
	case ty == ny && tm == nm && td == nd:
		return t.Format("15:04")
	case t.After(now) && sameDay(t, now.AddDate(0, 0, 1)):
		return "tomorrow at " + t.Format("15:04")
	}
	return t.Format("Mon 2 Jan at 15:04")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// size renders a byte count with decimal units: "512 MB", "38 GB", "9.1 GB",
// "1.2 TB".
func size(n int64) string {
	const (
		mb = 1_000_000
		gb = 1000 * mb
		tb = 1000 * gb
	)
	switch {
	case n >= tb:
		return decimal(float64(n)/tb) + " TB"
	case n >= gb:
		return decimal(float64(n)/gb) + " GB"
	case n >= mb:
		return strconv.FormatInt(n/mb, 10) + " MB"
	}
	return strconv.FormatInt(n/1000, 10) + " kB"
}

// decimal keeps one decimal below 10: "9.1", "38".
func decimal(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f", v)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

package ical

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestEscape(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{`a\b`, `a\\b`},
		{"a;b,c", `a\;b\,c`},
		{"line1\nline2", `line1\nline2`},
		{"crlf\r\nend", `crlf\nend`},
		{"tab\there", "tab here"},
		{"Amélie: le fabuleux destin", "Amélie: le fabuleux destin"}, // colon needs no escape in TEXT
		{`\;,`, `\\\;\,`},
	} {
		if got := Escape(tc.in); got != tc.want {
			t.Errorf("Escape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFold(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"short", "SUMMARY:hello"},
		{"exactly 75", "X:" + strings.Repeat("a", 73)},
		{"76", "X:" + strings.Repeat("a", 74)},
		{"long ascii", "DESCRIPTION:" + strings.Repeat("abcdefghij", 30)},
		{"multibyte", "SUMMARY:" + strings.Repeat("é", 100)},
		{"emoji", "SUMMARY:" + strings.Repeat("🎬", 40)},
	} {
		got := Fold(tc.in)
		if !strings.HasSuffix(got, "\r\n") {
			t.Fatalf("%s: no CRLF at the end", tc.name)
		}
		lines := strings.Split(strings.TrimSuffix(got, "\r\n"), "\r\n")
		var unfolded strings.Builder
		for i, l := range lines {
			if len(l) > maxLine {
				t.Errorf("%s: line %d is %d octets", tc.name, i, len(l))
			}
			if !utf8.ValidString(l) {
				t.Errorf("%s: line %d splits a UTF-8 sequence", tc.name, i)
			}
			if i > 0 {
				if !strings.HasPrefix(l, " ") {
					t.Errorf("%s: continuation line %d does not start with a space", tc.name, i)
				}
				l = l[1:]
			}
			unfolded.WriteString(l)
		}
		if unfolded.String() != tc.in {
			t.Errorf("%s: unfolding gives %q", tc.name, unfolded.String())
		}
		if len(tc.in) <= maxLine && len(lines) != 1 {
			t.Errorf("%s: folded a line of %d octets", tc.name, len(tc.in))
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Hour: "PT1H", 30 * time.Minute: "PT30M", 90 * time.Minute: "PT1H30M", time.Second: "PT1M", 6 * time.Hour: "PT6H",
	} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %s, want %s", d, got, want)
		}
	}
}

func TestWrite(t *testing.T) {
	stamp := time.Date(2026, 11, 6, 10, 30, 0, 0, time.FixedZone("CET", 3600))
	c := Calendar{
		ProdID: "-//Gonema//Calendar 1.1//EN", Name: "Gonema, films", Refresh: time.Hour,
		Events: []Event{{
			UID: "movie-12-digital@gonema", Date: time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC),
			Summary: "Halcyon Drift (2026); digital", Description: "Will search on release day.\nMonitored.",
			URL: "https://media.example.org/films/12", Categories: []string{"Digital", "Gonema"}, Stamp: stamp,
		}},
	}
	var buf bytes.Buffer
	if err := c.Write(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Error("bare LF in the output")
	}
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Gonema//Calendar 1.1//EN\r\n",
		"X-WR-CALNAME:Gonema\\, films\r\n",
		"REFRESH-INTERVAL;VALUE=DURATION:PT1H\r\n",
		"UID:movie-12-digital@gonema\r\n",
		"DTSTAMP:20261106T093000Z\r\n",
		"DTSTART;VALUE=DATE:20261231\r\nDTEND;VALUE=DATE:20270101\r\n",
		"SUMMARY:Halcyon Drift (2026)\\; digital\r\n",
		"DESCRIPTION:Will search on release day.\\nMonitored.\r\n",
		"URL;VALUE=URI:https://media.example.org/films/12\r\n",
		"CATEGORIES:Digital,Gonema\r\n",
		"TRANSP:TRANSPARENT\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "BEGIN:VEVENT") != 1 {
		t.Errorf("events: %d", strings.Count(out, "BEGIN:VEVENT"))
	}
}

func FuzzFold(f *testing.F) {
	f.Add("SUMMARY:" + strings.Repeat("é🎬a", 50))
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			return
		}
		got := Fold(s)
		for l := range strings.SplitSeq(strings.TrimSuffix(got, "\r\n"), "\r\n") {
			if len(l) > maxLine || !utf8.ValidString(l) {
				t.Fatalf("bad line %q", l)
			}
		}
	})
}

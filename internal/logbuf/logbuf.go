// Package logbuf keeps the recent log entries of Gonema in memory, for the
// Logs screen, and redacts secrets from every log output.
//
// Handler is a slog.Handler that tees each record to the regular handler
// (stderr, optional rotating file) and to a Buffer, a ring of the last N
// entries that can be queried and followed live. Messages and attribute
// values are redacted on the way (see Redact): API keys, tokens, passwords,
// Authorization headers and URL credentials never reach an output.
//
// The level is a *slog.LevelVar shared by both outputs, so it can change at
// runtime.
//
// The top-level attribute ComponentKey (set by logger.With("component",
// name) where a service is wired) names the part of Gonema that logged: it is
// kept in Entry.Component, not in the attributes. A later With of the key
// replaces the earlier one, so a service can hand a narrower component to a
// part of itself without the outputs repeating the key.
package logbuf

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultCapacity is the number of entries a Buffer keeps by default.
const DefaultCapacity = 2000

// ComponentKey is the attribute naming the component that logs an entry.
const ComponentKey = "component"

// maxComponentLen bounds the component name kept in an entry.
const maxComponentLen = 64

// maxValueLen bounds an attribute value kept in the buffer.
const maxValueLen = 2048

// Entry is one log record, redacted.
type Entry struct {
	ID        uint64            `json:"id"`
	Time      time.Time         `json:"time"`
	Level     string            `json:"level" enum:"debug,info,warn,error"`
	Component string            `json:"component" doc:"Part of Gonema that logged (indexer, download, import...); empty when unknown"`
	Message   string            `json:"message"`
	Attrs     map[string]string `json:"attrs,omitempty" doc:"Attributes, redacted; groups are joined with dots"`
}

// LevelName returns the lower-case name of a level: debug, info, warn, error.
func LevelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	}
	return "error"
}

// ParseLevel parses debug, info, warn or error (case-insensitive).
func ParseLevel(s string) (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return 0, fmt.Errorf("logbuf: level %q: want debug, info, warn or error", s)
	}
	return l, nil
}

// Buffer is a ring of the last entries. It is safe for concurrent use.
type Buffer struct {
	mu      sync.Mutex
	ring    []Entry
	pos     int // next write position once full
	nextID  uint64
	subs    map[*Subscription]struct{}
	dropped uint64
}

// NewBuffer returns a buffer keeping capacity entries (DefaultCapacity if <= 0).
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Buffer{ring: make([]Entry, 0, capacity), subs: map[*Subscription]struct{}{}}
}

// Capacity returns the number of entries kept.
func (b *Buffer) Capacity() int { return cap(b.ring) }

// add stores e (its ID is assigned) and offers it to the subscribers without
// blocking.
func (b *Buffer) add(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	e.ID = b.nextID
	if len(b.ring) < cap(b.ring) {
		b.ring = append(b.ring, e)
	} else {
		b.ring[b.pos] = e
		b.pos = (b.pos + 1) % len(b.ring)
	}
	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
			s.dropped++
			b.dropped++
		}
	}
}

// entries returns the kept entries, oldest first. Callers hold b.mu.
func (b *Buffer) entries() []Entry {
	out := make([]Entry, 0, len(b.ring))
	out = append(out, b.ring[b.pos:]...)
	return append(out, b.ring[:b.pos]...)
}

// Query selects entries.
type Query struct {
	MinLevel   string    // debug, info, warn or error: entries at this level or above; "" = all
	Since      time.Time // zero: no lower bound
	AfterID    uint64    // entries with a greater ID
	Text       string    // case-insensitive substring of the message or an attribute
	Components []string  // entries of one of these components (exact, case-insensitive); empty: all
	Limit      int       // the newest Limit matches; <= 0: all
}

// Match reports whether e has the level and component q asks for (the
// filters a live follower applies; Since, AfterID and Limit are not checked).
func (q *Query) Match(e *Entry) bool {
	if q.MinLevel != "" && levelOf(e.Level) < levelOf(q.MinLevel) {
		return false
	}
	if len(q.Components) > 0 && !slices.ContainsFunc(q.Components, func(c string) bool { return strings.EqualFold(c, e.Component) }) {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(q.Text))
	return text == "" || e.contains(text)
}

// Entries returns the matching entries, oldest first.
func (b *Buffer) Entries(q Query) []Entry {
	b.mu.Lock()
	all := b.entries()
	b.mu.Unlock()
	out := make([]Entry, 0, min(len(all), max(q.Limit, 0)+1))
	for i := len(all) - 1; i >= 0; i-- {
		e := &all[i]
		if e.ID <= q.AfterID {
			break
		}
		if !q.Since.IsZero() && e.Time.Before(q.Since) {
			break // entries are in time order
		}
		if !q.Match(e) {
			continue
		}
		out = append(out, *e)
		if q.Limit > 0 && len(out) == q.Limit {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func levelOf(name string) slog.Level {
	switch name {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

func (e *Entry) contains(lower string) bool {
	if strings.Contains(strings.ToLower(e.Message), lower) {
		return true
	}
	for k, v := range e.Attrs {
		if strings.Contains(strings.ToLower(v), lower) || strings.Contains(strings.ToLower(k), lower) {
			return true
		}
	}
	return false
}

// WriteText writes the kept entries as text lines, oldest first.
func (b *Buffer) WriteText(w io.Writer) error {
	b.mu.Lock()
	all := b.entries()
	b.mu.Unlock()
	var sb strings.Builder
	for i := range all {
		sb.Reset()
		all[i].format(&sb)
		if _, err := io.WriteString(w, sb.String()); err != nil {
			return err
		}
	}
	return nil
}

func (e *Entry) format(sb *strings.Builder) {
	sb.WriteString(e.Time.Format(time.RFC3339Nano))
	sb.WriteByte(' ')
	sb.WriteString(strings.ToUpper(e.Level))
	sb.WriteByte(' ')
	if e.Component != "" {
		sb.WriteString(ComponentKey + "=" + e.Component + " ")
	}
	sb.WriteString(e.Message)
	keys := make([]string, 0, len(e.Attrs))
	for k := range e.Attrs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		sb.WriteByte(' ')
		sb.WriteString(k)
		sb.WriteByte('=')
		v := e.Attrs[k]
		if strings.ContainsAny(v, " \t\"=") {
			fmt.Fprintf(sb, "%q", v)
		} else {
			sb.WriteString(v)
		}
	}
	sb.WriteByte('\n')
}

// Subscription receives new entries. A subscriber that does not keep up loses
// entries (counted by Dropped), never slows logging down.
type Subscription struct {
	b       *Buffer
	ch      chan Entry
	dropped uint64 // guarded by b.mu
	closed  bool   // guarded by b.mu
}

// Subscribe follows new entries with a buffer of the given size.
func (b *Buffer) Subscribe(buffer int) *Subscription {
	s := &Subscription{b: b, ch: make(chan Entry, max(buffer, 1))}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// C delivers the entries; it is closed by Close.
func (s *Subscription) C() <-chan Entry { return s.ch }

// Dropped returns the number of entries this subscriber missed.
func (s *Subscription) Dropped() uint64 {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	return s.dropped
}

// Close ends the subscription. Safe to call more than once.
func (s *Subscription) Close() {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	delete(s.b.subs, s)
	close(s.ch)
}

// ----------------------------------------------------------------- handler

// Handler tees records to another handler and to a Buffer, redacting both.
type Handler struct {
	next   slog.Handler
	buf    *Buffer
	level  slog.Leveler
	prefix string      // group prefix of the attributes added by WithGroup
	attrs  []slog.Attr // attributes added by WithAttrs, redacted, with the prefix applied (buffer only)
	// component is the last top-level ComponentKey given to WithAttrs. While
	// pending, next has not received it: Handle adds it to the record, so a
	// replaced component never reaches next. WithGroup hands it over first.
	component string
	pending   bool
}

// NewHandler returns a handler writing to next and buf at level (next should
// use the same level so both outputs agree).
func NewHandler(next slog.Handler, buf *Buffer, level slog.Leveler) *Handler {
	return &Handler{next: next, buf: buf, level: level}
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level.Level() && h.next.Enabled(ctx, l)
}

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	red := slog.NewRecord(r.Time, r.Level, Redact(r.Message), r.PC)
	e := Entry{Time: r.Time.UTC(), Level: LevelName(r.Level), Component: h.component, Message: red.Message}
	if n := len(h.attrs) + r.NumAttrs(); n > 0 {
		e.Attrs = make(map[string]string, n)
	}
	for _, a := range h.attrs {
		flatten(e.Attrs, "", a)
	}
	own := false // the record names its component itself
	if h.prefix == "" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == ComponentKey {
				own = true
			}
			return !own
		})
	}
	if h.pending && !own {
		red.AddAttrs(slog.String(ComponentKey, h.component))
	}
	r.Attrs(func(a slog.Attr) bool {
		a = redactAttr(a)
		red.AddAttrs(a)
		if c, ok := componentOf(h.prefix, a); ok {
			e.Component = c
		} else {
			flatten(e.Attrs, h.prefix, a)
		}
		return true
	})
	if len(e.Attrs) == 0 {
		e.Attrs = nil
	}
	if h.buf != nil {
		h.buf.add(e)
	}
	return h.next.Handle(ctx, red)
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	red := make([]slog.Attr, 0, len(as))
	for _, a := range as {
		a = redactAttr(a)
		if comp, ok := componentOf(h.prefix, a); ok {
			c.component, c.pending = comp, true
			continue
		}
		red = append(red, a)
	}
	if len(red) > 0 {
		c.next = h.next.WithAttrs(red)
		c.attrs = append(append([]slog.Attr(nil), h.attrs...), prefixed(h.prefix, red)...)
	}
	return &c
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	if c.pending { // attributes added after the group cannot replace it any more
		c.next = c.next.WithAttrs([]slog.Attr{slog.String(ComponentKey, c.component)})
		c.pending = false
	}
	c.next = c.next.WithGroup(name)
	c.prefix = h.prefix + name + "."
	return &c
}

func prefixed(prefix string, as []slog.Attr) []slog.Attr {
	if prefix == "" {
		return as
	}
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = slog.Attr{Key: prefix + a.Key, Value: a.Value}
	}
	return out
}

// componentOf returns the component named by a (redacted) when it is the
// top-level ComponentKey attribute.
func componentOf(prefix string, a slog.Attr) (string, bool) {
	if prefix != "" || a.Key != ComponentKey || a.Value.Kind() == slog.KindGroup {
		return "", false
	}
	v := strings.TrimSpace(a.Value.String())
	if len(v) > maxComponentLen {
		v = v[:maxComponentLen]
	}
	return v, true
}

// redactAttr resolves a and redacts its value (recursively for groups).
func redactAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if SensitiveKey(a.Key) && a.Value.Kind() != slog.KindGroup {
		return slog.String(a.Key, Redacted)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		if s := a.Value.String(); mayHoldSecret(s) {
			return slog.String(a.Key, Redact(s))
		}
	case slog.KindGroup:
		gs := a.Value.Group()
		out := make([]slog.Attr, len(gs))
		for i, g := range gs {
			out[i] = redactAttr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case error:
			return slog.String(a.Key, Redact(v.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, Redact(v.String()))
		case []byte:
			return slog.String(a.Key, Redact(string(v)))
		}
	}
	return a
}

// flatten stores a (already redacted) into m with dotted group keys.
func flatten(m map[string]string, prefix string, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, g := range a.Value.Group() {
			flatten(m, p, g)
		}
		return
	}
	v := a.Value.String()
	if a.Value.Kind() == slog.KindTime {
		v = a.Value.Time().UTC().Format(time.RFC3339Nano)
	}
	if len(v) > maxValueLen {
		v = v[:maxValueLen] + "…"
	}
	m[prefix+a.Key] = v
}

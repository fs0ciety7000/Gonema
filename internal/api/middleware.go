package api

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// RequestIDHeader carries the request id, accepted from a reverse proxy when
// well formed, generated otherwise, and echoed in the response.
const RequestIDHeader = "X-Request-Id"

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID returns the request id stored in ctx ("" if none).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		ok := c == '-' || c == '_' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !ok {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b[:])
}

// withRequestID assigns the request id.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// statusWriter records the status and size of a response.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach Flush and deadlines.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// withLogging logs one line per request: no query string, no body, no header
// (they may hold search terms or credentials).
func withLogging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		level := slog.LevelInfo
		switch {
		case sw.status >= 500:
			level = slog.LevelError
		case r.URL.Path == "/health" || strings.HasSuffix(r.URL.Path, "/health"):
			level = slog.LevelDebug
		}
		log.LogAttrs(r.Context(), level, "http request",
			slog.String("request_id", RequestID(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Int64("bytes", sw.bytes),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

// withRecover turns a panic into a 500 problem response and an error log with
// the stack. http.ErrAbortHandler is passed through (it aborts on purpose).
func withRecover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if p == http.ErrAbortHandler { //nolint:errorlint // sentinel compared as net/http does
				panic(p)
			}
			log.Error("panic while serving a request", slog.String("request_id", RequestID(r.Context())),
				slog.String("path", r.URL.Path), slog.Any("panic", p), slog.String("stack", string(debug.Stack())))
			if sw.status == 0 {
				writeProblem(w, r, http.StatusInternalServerError,
					"Something went wrong on the server; the logs have the details (request id "+RequestID(r.Context())+").")
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// withWriteDeadline bounds the time to write a response. Streaming handlers
// (SSE) clear it and manage their own deadlines.
func withWriteDeadline(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d)) // unsupported in tests
		next.ServeHTTP(w, r)
	})
}

// gzipPool holds BestSpeed writers: on the 2.2 MB columns of a 10,000-movie
// library, level 1 takes 12 ms less than the default level for a body 20 %
// larger (425 KB instead of 345 KB), and small responses barely differ.
var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}}

// gzipWriter compresses JSON and text responses when the client accepts gzip.
// The decision is taken when the headers are written, from the Content-Type:
// event streams and binary responses pass through untouched.
type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

func compressible(ct string) bool {
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "text/event-stream") {
		return false
	}
	return strings.Contains(ct, "json") || strings.Contains(ct, "yaml") ||
		strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript")
}

func (w *gzipWriter) decide(status int) {
	if w.decided {
		return
	}
	w.decided = true
	h := w.Header()
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified ||
		h.Get("Content-Encoding") != "" || !compressible(h.Get("Content-Type")) {
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	gz, _ := gzipPool.Get().(*gzip.Writer)
	gz.Reset(w.ResponseWriter)
	w.gz = gz
}

func (w *gzipWriter) WriteHeader(code int) {
	w.decide(code)
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.decided {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush flushes the compressor then the connection.
func (w *gzipWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach deadlines.
func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipWriter) close() {
	if w.gz == nil {
		return
	}
	_ = w.gz.Close()
	w.gz.Reset(io.Discard)
	gzipPool.Put(w.gz)
	w.gz = nil
}

func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

func acceptsGzip(h string) bool {
	for part := range strings.SplitSeq(h, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return !strings.Contains(strings.ReplaceAll(params, " ", ""), "q=0") ||
				strings.Contains(params, "q=0.")
		}
	}
	return false
}

// writeProblem writes an RFC 9457 problem+json response outside Huma.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	body, err := json.Marshal(problemBody{
		Title: http.StatusText(status), Status: status, Detail: detail,
		Instance: "urn:gonema:request:" + RequestID(r.Context()),
	})
	if err != nil { // cannot happen: plain strings
		body = []byte(`{"status":500}`)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

type problemBody struct {
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance,omitempty"`
}

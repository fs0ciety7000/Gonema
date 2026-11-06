// Package api is the HTTP API of Gonema: REST endpoints described with Huma v2
// (OpenAPI 3.1 at /api/v1/openapi.json), a Server-Sent Events stream, and the
// /health liveness probe.
//
// Errors are RFC 9457 problem+json documents whose "detail" is a plain-language
// sentence. No response ever contains a secret (the TMDB key is write-only).
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/auth"
	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/health"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// Prefix is the path of the versioned API.
const Prefix = "/api/v1"

// Deps are the services the API serves.
type Deps struct {
	Library   *library.Service
	Acquire   *acquire.Service // optional: without it, the acquisition endpoints are absent
	Jobs      *jobs.Runner
	Bus       *events.Bus
	Store     *store.Store
	Logger    *slog.Logger
	Version   string
	StartedAt time.Time
	DataDir   string
	BaseURL   string // "" or "/path"
	Bind      string // listening address, shown in Settings > General
	LogLevel  string // shown in Settings > General

	// Queue gives the live download progress of the lists; default Acquire.
	Queue QueueReader
	// Images serves /images; optional (404 without it).
	Images ImageStore
	// UI serves the web interface on every path outside /api and /health;
	// optional.
	UI http.Handler

	// SSEHeartbeat is the period of SSE keep-alive comments; default 15 s.
	SSEHeartbeat time.Duration
	// MaxStreams and MaxStreamsPerCaller cap the open SSE connections
	// (/events and /logs/stream); defaults 64 and 8.
	MaxStreams, MaxStreamsPerCaller int

	// Auth authenticates the callers. Without it every protected route
	// answers 401 (fail closed). See auth_middleware.go.
	Auth *auth.Service
	// Security holds the authentication options (mode, cookie, origins).
	Security config.Security
}

// Server is the HTTP API.
type Server struct {
	d       Deps
	files   *importer.Service // file operations; set by RegisterImportRoutes
	health  *health.Scheduler // health issues of the dashboard; set by RegisterHealthRoutes
	log     *slog.Logger
	handler http.Handler
	api     huma.API

	// streams is canceled by Shutdown so that SSE handlers return and
	// http.Server.Shutdown can complete.
	streams      context.Context
	closeStreams context.CancelFunc
	// streamSlots caps the open SSE connections (sse_limit.go).
	streamSlots *streamLimiter

	// Scheme warnings of GONEMA_COOKIE_SECURE=auto (auth_middleware.go).
	httpsOrigins bool // a trusted origin is https
	sawHTTPS     atomic.Bool
	warnedProto  atomic.Bool
	warnedHTTPS  atomic.Bool
	// setupTokenLogged: the setup token was logged again after a refusal.
	setupTokenLogged atomic.Bool
}

// New builds the API.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.SSEHeartbeat <= 0 {
		d.SSEHeartbeat = 15 * time.Second
	}
	if d.MaxStreams <= 0 {
		d.MaxStreams = maxStreams
	}
	if d.MaxStreamsPerCaller <= 0 {
		d.MaxStreamsPerCaller = maxStreamsPerCaller
	}
	s := &Server{d: d, log: d.Logger, streamSlots: newStreamLimiter(d.MaxStreams, d.MaxStreamsPerCaller)}
	s.streams, s.closeStreams = context.WithCancel(context.Background())
	for _, o := range d.Security.TrustedOrigins {
		if strings.HasPrefix(o, "https://") {
			s.httpsOrigins = true
		}
	}

	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Gonema API", d.Version)
	cfg.Info.Description = "Self-hosted movie collection manager. Errors are RFC 9457 problem+json documents."
	cfg.Info.License = &huma.License{Name: "GPL-3.0", URL: "https://www.gnu.org/licenses/gpl-3.0.html"}
	cfg.Servers = []*huma.Server{{URL: d.BaseURL + Prefix}}
	cfg.DocsPath = ""    // the docs UI loads scripts from a CDN: not for a self-hosted app
	cfg.SchemasPath = "" // no $schema links in responses
	cfg.CreateHooks = nil
	s.api = humago.NewWithPrefix(mux, Prefix, cfg)

	s.routes()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, "There is no API endpoint at "+r.URL.Path+"; see "+d.BaseURL+Prefix+"/openapi.json.")
	})
	if d.UI != nil {
		mux.Handle("/", d.UI)
	}

	h := s.withAuth(mux) // every /api route needs a caller, see auth_middleware.go
	if d.BaseURL != "" {
		outer := http.NewServeMux()
		outer.Handle(d.BaseURL+"/", http.StripPrefix(d.BaseURL, h)) // h, not mux: auth and CSRF apply under the base URL too
		outer.Handle("GET "+d.BaseURL, http.RedirectHandler(d.BaseURL+"/", http.StatusMovedPermanently))
		outer.HandleFunc("GET /health", handleHealth)
		h = outer
	}
	h = withSecurityHeaders(h)
	h = withGzip(h)
	h = withWriteDeadline(60*time.Second, h)
	h = withRecover(d.Logger, h)
	h = withLogging(d.Logger, h)
	h = withRequestID(h)
	s.handler = h
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// OpenAPI returns the OpenAPI document (for tests and code generation).
func (s *Server) OpenAPI() *huma.OpenAPI { return s.api.OpenAPI() }

// Shutdown ends the event streams; call it before http.Server.Shutdown (or
// register it with RegisterOnShutdown).
func (s *Server) Shutdown() { s.closeStreams() }

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
}

// userMessage is implemented by errors whose text is meant for the user.
type userMessage interface{ UserMessage() string }

// problem converts a service error into a problem+json error with a
// plain-language detail. Unexpected errors are logged with the request id and
// reported without internals. upstream marks endpoints that call TMDB.
func (s *Server) problem(ctx context.Context, err error, upstream bool) error {
	var um userMessage
	hasMsg := errors.As(err, &um)
	var apiErr *metadata.APIError
	var rl *metadata.RateLimitError
	switch {
	case errors.Is(err, acquire.ErrRefused) && hasMsg:
		return huma.Error422UnprocessableEntity(um.UserMessage())
	case errors.Is(err, acquire.ErrInvalid) && errors.Is(err, store.ErrConflict) && hasMsg:
		return huma.Error409Conflict(um.UserMessage())
	case (errors.Is(err, acquire.ErrInvalid) || errors.Is(err, acquire.ErrUnavailable)) && hasMsg:
		return huma.Error422UnprocessableEntity(um.UserMessage())
	case errors.Is(err, acquire.ErrUpstream) && hasMsg:
		return huma.Error502BadGateway(um.UserMessage())
	case errors.Is(err, library.ErrInvalid) && errors.Is(err, store.ErrConflict) && hasMsg:
		return huma.Error409Conflict(um.UserMessage())
	case errors.Is(err, library.ErrInvalid) && hasMsg:
		return huma.Error422UnprocessableEntity(um.UserMessage())
	case errors.Is(err, store.ErrNotFound) && hasMsg:
		return huma.Error404NotFound(um.UserMessage())
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("Not found.")
	case errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict("This conflicts with existing data (duplicate, or still in use).")
	case errors.Is(err, library.ErrNoProvider):
		return huma.Error503ServiceUnavailable("No TMDB key is configured yet: add one in Settings > Metadata to search and add movies.")
	case errors.As(err, &rl):
		e := huma.Error503ServiceUnavailable("TMDB is rate limiting requests; try again in a moment.")
		if rl.RetryAfter > 0 {
			return huma.ErrorWithHeaders(e, http.Header{"Retry-After": {strconv.Itoa(int(rl.RetryAfter.Seconds()) + 1)}})
		}
		return e
	case errors.Is(err, metadata.ErrUnauthorized):
		return huma.Error502BadGateway("TMDB rejected the configured key; update it in Settings > Metadata.")
	case errors.As(err, &apiErr):
		return huma.Error502BadGateway("TMDB answered with an error (HTTP " + strconv.Itoa(apiErr.HTTPStatus) + "); try again later.")
	case errors.Is(err, context.DeadlineExceeded) && upstream:
		return huma.Error504GatewayTimeout("TMDB did not answer in time; try again later.")
	case errors.Is(err, context.Canceled):
		return huma.Error503ServiceUnavailable("The request was canceled.")
	}
	rid := RequestID(ctx)
	if upstream {
		s.log.Warn("upstream request failed", slog.String("request_id", rid), slog.Any("error", err))
		return huma.Error502BadGateway("TMDB could not be reached; check the server's internet access and try again (request id " + rid + ").")
	}
	s.log.Error("request failed", slog.String("request_id", rid), slog.Any("error", err))
	return huma.Error500InternalServerError("Something went wrong on the server; the logs have the details (request id " + rid + ").")
}

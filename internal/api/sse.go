package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/fs0ciety7000/gonema/internal/events"
)

// SSE stream parameters.
const (
	sseBuffer       = 256              // events buffered per client
	sseWriteTimeout = 15 * time.Second // a client that does not read is dropped
	sseRetryMS      = 3000             // reconnection delay advised to clients
)

// ResyncEvent is sent when the client may have missed events (its buffer
// overflowed, or Last-Event-ID is too old): it should refetch its data.
const ResyncEvent = "resync"

type eventsInput struct {
	LastEventID string `header:"Last-Event-ID" maxLength:"20" doc:"Id of the last event received; missed events still in memory are replayed"`
}

func (s *Server) registerEvents() {
	huma.Register(s.api, huma.Operation{
		OperationID: "stream-events", Method: http.MethodGet, Path: "/events", Tags: []string{"Events"},
		Summary: "Event stream (Server-Sent Events)",
		Description: "Each message has an id, an event name (movie.added, movie.updated, movie.deleted, job.started, " +
			"job.completed, job.failed, health.changed, download.progress, download.grabbed, download.updated, " +
			"download.imported, download.failed, download.removed, search.batch, search.indexer_done, " +
			"search.indexer_failed, search.finished, ...) and JSON data {id, type, time, movieId, data}. " +
			"A comment is sent every 15 s as a heartbeat. An event named \"resync\" means events were missed: refetch.",
		Responses: map[string]*huma.Response{
			"200": {Description: "An endless text/event-stream", Content: map[string]*huma.MediaType{
				"text/event-stream": {Schema: &huma.Schema{Type: huma.TypeString}},
			}},
		},
	}, func(_ context.Context, in *eventsInput) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			r, w := humago.Unwrap(hctx)
			s.stream(r, w, in.LastEventID)
		}}, nil
	})
}

// stream serves one SSE client until it disconnects or the server shuts down.
func (s *Server) stream(r *http.Request, w http.ResponseWriter, lastEventID string) {
	ctx := r.Context()
	log := s.log.With(slog.String("request_id", RequestID(ctx)))
	rc := http.NewResponseController(w)
	// The connection lives long: drop the server-wide deadlines, then bound
	// each write instead.
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	sub := s.d.Bus.Subscribe(sseBuffer, nil)
	defer sub.Close()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
	w.WriteHeader(http.StatusOK)

	var buf bytes.Buffer
	write := func() bool {
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
		_, err := w.Write(buf.Bytes())
		buf.Reset()
		if err == nil {
			err = rc.Flush()
		}
		if err != nil {
			log.Debug("event stream closed", slog.Any("error", err))
			return false
		}
		return true
	}
	var last uint64
	send := func(e *events.Event) bool {
		data, err := json.Marshal(e)
		if err != nil {
			log.Error("encode event", slog.String("type", string(e.Type)), slog.Any("error", err))
			return true
		}
		buf.WriteString("id: ")
		buf.WriteString(strconv.FormatUint(e.ID, 10))
		buf.WriteString("\nevent: ")
		buf.WriteString(string(e.Type))
		buf.WriteString("\ndata: ")
		buf.Write(data)
		buf.WriteString("\n\n")
		last = e.ID
		return write()
	}
	resync := func(reason string) bool {
		buf.WriteString("event: " + ResyncEvent + "\ndata: ")
		b, _ := json.Marshal(map[string]string{"reason": reason})
		buf.Write(b)
		buf.WriteString("\n\n")
		return write()
	}

	buf.WriteString("retry: " + strconv.Itoa(sseRetryMS) + "\n: connected\n\n")
	if !write() {
		return
	}
	if lastEventID != "" {
		if id, err := strconv.ParseUint(lastEventID, 10, 64); err == nil {
			missed, complete := s.d.Bus.Since(id)
			if !complete && !resync("some events were missed while disconnected") {
				return
			}
			for i := range missed {
				if !send(&missed[i]) {
					return
				}
			}
		}
	}

	heartbeat := time.NewTicker(s.d.SSEHeartbeat)
	defer heartbeat.Stop()
	var dropped uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.streams.Done():
			return // server shutdown: the client reconnects to the next instance
		case <-heartbeat.C:
			buf.WriteString(": ping\n\n")
			if !write() {
				return
			}
		case e, ok := <-sub.C():
			if !ok {
				return
			}
			if d := sub.Dropped(); d > dropped {
				dropped = d
				if !resync("the connection was too slow and events were dropped") {
					return
				}
			}
			if e.ID <= last { // already replayed
				continue
			}
			if !send(&e) {
				return
			}
		}
	}
}

package deluge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/download/internal/httpc"
	"github.com/fs0ciety7000/gonema/internal/flight"
)

// DefaultPort is the Deluge Web UI default port.
const DefaultPort = 8112

// JSON-RPC error codes of deluge/ui/web/json_api.py.
const (
	codeNotAuthenticated = 1
	codeUnknownMethod    = 2
)

// Client is a Deluge Web UI client. It is safe for concurrent use.
type Client struct {
	r        *httpc.Requester
	pass     string
	category string
	id       atomic.Uint64

	mu      sync.Mutex
	session *session
	open    flight.Group[*session]
}

type session struct {
	cookie string // "_session_id=..." or "" when the Web UI sets none
}

var _ download.Client = (*Client)(nil)

// New returns a client for cfg (Password of the Web UI; Username is unused).
func New(cfg *download.Config) (*Client, error) {
	r, err := httpc.New(download.KindDeluge, cfg, DefaultPort)
	if err != nil {
		return nil, err
	}
	return &Client{r: r, pass: cfg.Password, category: strings.ToLower(cfg.Category)}, nil
}

// Kind implements download.Client.
func (*Client) Kind() download.Kind { return download.KindDeluge }

type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// raw sends one JSON-RPC request with the session cookie.
func (c *Client) raw(ctx context.Context, op string, s *session, method string, params []any) (*rpcResponse, string, error) {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{"method": method, "params": params, "id": c.id.Add(1)})
	if err != nil {
		return nil, "", c.r.Err(op, download.ErrInvalidRequest, "", err)
	}
	h := http.Header{"Accept": {"application/json"}}
	if s != nil && s.cookie != "" {
		h.Set("Cookie", s.cookie)
	}
	resp, err := c.r.Do(ctx, &httpc.Request{Op: op, Method: http.MethodPost, Path: "json", Body: body, ContentType: "application/json", Header: h})
	if err != nil {
		return nil, "", err
	}
	if err := c.r.StatusError(op, resp); err != nil {
		return nil, "", err
	}
	var out rpcResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, "", c.r.Err(op, download.ErrUnexpected, "the answer is not Deluge JSON-RPC: check the URL base", nil)
	}
	cookie := ""
	for _, ck := range (&http.Response{Header: resp.Header}).Cookies() {
		if ck.Name == "_session_id" && ck.Value != "" {
			c.r.AddSecret(ck.Value)
			cookie = ck.Name + "=" + ck.Value
		}
	}
	return &out, cookie, nil
}

func (c *Client) rpcErr(op, method string, e *rpcError) error {
	msg := e.Message
	switch {
	case e.Code == codeNotAuthenticated:
		return c.r.Err(op, download.ErrAuth, "Deluge says not authenticated", nil)
	case strings.Contains(msg, "InvalidTorrentError"):
		return c.r.Err(op, download.ErrNotFound, msg, nil)
	case e.Code == codeUnknownMethod && strings.HasPrefix(method, "label."):
		return c.r.Err(op, download.ErrUnsupported, "enable the Label plugin in Deluge to use categories", nil)
	}
	return c.r.Err(op, download.ErrRejected, fmt.Sprintf("Deluge error %d calling %s: %s", e.Code, method, msg), nil)
}

// connect logs in and connects the Web UI to its first daemon.
func (c *Client) connect(ctx context.Context, op string) (*session, error) {
	out, cookie, err := c.raw(ctx, op, nil, "auth.login", []any{c.pass})
	if err != nil {
		return nil, err
	}
	var ok bool
	if out.Error != nil || json.Unmarshal(out.Result, &ok) != nil || !ok {
		return nil, c.r.Err(op, download.ErrAuth, "wrong Deluge Web UI password", nil)
	}
	s := &session{cookie: cookie}
	var connected bool
	if err := c.callIn(ctx, op, s, "web.connected", nil, &connected); err != nil {
		return nil, err
	}
	if connected {
		return s, nil
	}
	var hosts [][]any
	if err := c.callIn(ctx, op, s, "web.get_hosts", nil, &hosts); err != nil {
		return nil, err
	}
	if len(hosts) == 0 || len(hosts[0]) == 0 {
		return nil, c.r.Err(op, download.ErrAPIDisabled, "the Deluge Web UI knows no daemon: add one in its Connection Manager", nil)
	}
	hostID, _ := hosts[0][0].(string)
	if hostID == "" {
		return nil, c.r.Err(op, download.ErrUnexpected, "unreadable Deluge host list", nil)
	}
	if err := c.callIn(ctx, op, s, "web.connect", []any{hostID}, nil); err != nil {
		return nil, err
	}
	if err := c.callIn(ctx, op, s, "web.connected", nil, &connected); err != nil {
		return nil, err
	}
	if !connected {
		return nil, c.r.Err(op, download.ErrUnreachable, "the Deluge Web UI cannot connect to its daemon (is deluged running?)", nil)
	}
	return s, nil
}

// callIn calls method within s without retry.
func (c *Client) callIn(ctx context.Context, op string, s *session, method string, params []any, dst any) error {
	out, _, err := c.raw(ctx, op, s, method, params)
	if err != nil {
		return err
	}
	if out.Error != nil {
		return c.rpcErr(op, method, out.Error)
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(out.Result, dst); err != nil {
		return c.r.Err(op, download.ErrUnexpected, "cannot decode the Deluge answer to "+method, err)
	}
	return nil
}

// reopen replaces stale with a new connected session; concurrent callers
// share one login.
func (c *Client) reopen(ctx context.Context, op string, stale *session) (*session, error) {
	s, _, err := c.open.Do(ctx, "open", func(fctx context.Context) (*session, error) {
		c.mu.Lock()
		cur := c.session
		c.mu.Unlock()
		if cur != nil && cur != stale {
			return cur, nil
		}
		ns, err := c.connect(fctx, op)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.session = ns
		c.mu.Unlock()
		return ns, nil
	})
	return s, err
}

// call runs method, opening a session first and once more when Deluge
// answers "not authenticated" or "unknown method" (daemon disconnected).
func (c *Client) call(ctx context.Context, op, method string, params []any, dst any) error {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	var err error
	if s == nil {
		if s, err = c.reopen(ctx, op, nil); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		out, _, err := c.raw(ctx, op, s, method, params)
		if err != nil {
			return err
		}
		if out.Error == nil {
			if dst == nil {
				return nil
			}
			if err := json.Unmarshal(out.Result, dst); err != nil {
				return c.r.Err(op, download.ErrUnexpected, "cannot decode the Deluge answer to "+method, err)
			}
			return nil
		}
		retry := out.Error.Code == codeNotAuthenticated ||
			(out.Error.Code == codeUnknownMethod && !strings.HasPrefix(method, "label."))
		if !retry || attempt > 0 {
			return c.rpcErr(op, method, out.Error)
		}
		if s, err = c.reopen(ctx, op, s); err != nil {
			return err
		}
	}
}

func (c *Client) labelPlugin(ctx context.Context, op string) (bool, error) {
	var plugins []string
	if err := c.call(ctx, op, "core.get_enabled_plugins", nil, &plugins); err != nil {
		return false, err
	}
	return slices.Contains(plugins, "Label"), nil
}

// Test implements download.Client: login, daemon connection and, with a
// category, the Label plugin.
func (c *Client) Test(ctx context.Context) error {
	c.mu.Lock()
	c.session = nil
	c.mu.Unlock()
	ok, err := c.labelPlugin(ctx, "test")
	if err != nil {
		return err
	}
	if c.category != "" && !ok {
		return c.r.Err("test", download.ErrUnsupported, "a category is set: enable the Label plugin in Deluge (Preferences > Plugins)", nil)
	}
	return nil
}

// Add implements download.Client.
func (c *Client) Add(ctx context.Context, req download.AddRequest) (string, error) {
	if err := req.Validate(decision.Torrent); err != nil {
		return "", c.r.Err("add", download.ErrInvalidRequest, "", err)
	}
	opts := map[string]any{"add_paused": req.Paused}
	if req.SeedRatio > 0 {
		opts["stop_at_ratio"] = true
		opts["stop_ratio"] = req.SeedRatio
	}
	var (
		id     *string
		method string
		params []any
	)
	switch {
	case download.IsMagnet(req.URL):
		method, params = "core.add_torrent_magnet", []any{req.URL, opts}
	case req.URL != "":
		method, params = "core.add_torrent_url", []any{req.URL, opts, map[string]any{}}
	default:
		method, params = "core.add_torrent_file", []any{req.Filename, base64.StdEncoding.EncodeToString(req.Data), opts}
	}
	if err := c.call(ctx, "add", method, params, &id); err != nil {
		return "", err
	}
	if id == nil || *id == "" {
		return "", c.r.Err("add", download.ErrRejected, "Deluge did not add the torrent (already present or invalid?)", nil)
	}
	ref := strings.ToLower(*id)
	if req.Priority == download.PriorityHigh || req.Priority == download.PriorityForce {
		if err := c.call(ctx, "add", "core.queue_top", []any{[]string{ref}}, nil); err != nil {
			return ref, err
		}
	}
	if cat := strings.ToLower(req.CategoryOr(c.category)); cat != "" {
		if err := c.setLabel(ctx, ref, cat); err != nil {
			return ref, err
		}
	}
	return ref, nil
}

func (c *Client) setLabel(ctx context.Context, ref, label string) error {
	var labels []string
	if err := c.call(ctx, "add", "label.get_labels", nil, &labels); err != nil {
		return err
	}
	if !slices.Contains(labels, label) {
		if err := c.call(ctx, "add", "label.add", []any{label}, nil); err != nil {
			return err
		}
	}
	return c.call(ctx, "add", "label.set_torrent", []any{ref, label}, nil)
}

var statusKeys = []string{
	"hash", "name", "state", "progress", "total_wanted", "total_done", "download_payload_rate",
	"eta", "save_path", "download_location", "ratio", "message", "label", "is_finished",
}

type torrentStatus struct {
	Hash             string    `json:"hash"`
	Name             string    `json:"name"`
	State            string    `json:"state"`
	Progress         httpc.Num `json:"progress"` // 0..100
	TotalWanted      httpc.Num `json:"total_wanted"`
	TotalDone        httpc.Num `json:"total_done"`
	DownloadRate     httpc.Num `json:"download_payload_rate"`
	ETA              httpc.Num `json:"eta"`
	SavePath         string    `json:"save_path"`
	DownloadLocation string    `json:"download_location"`
	Ratio            httpc.Num `json:"ratio"`
	Message          string    `json:"message"`
	Label            string    `json:"label"`
	IsFinished       bool      `json:"is_finished"`
}

// Items implements download.Client.
func (c *Client) Items(ctx context.Context) ([]download.Item, error) {
	filter := map[string]any{}
	if c.category != "" {
		filter["label"] = c.category
	}
	var m map[string]torrentStatus
	if err := c.call(ctx, "items", "core.get_torrents_status", []any{filter, statusKeys}, &m); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	items := make([]download.Item, 0, len(m))
	for _, id := range ids {
		t := m[id]
		if t.Hash == "" {
			t.Hash = id
		}
		items = append(items, item(&t))
	}
	return items, nil
}

func item(t *torrentStatus) download.Item {
	st, msg := mapState(t.State, t.IsFinished, t.Message)
	loc := t.DownloadLocation
	if loc == "" {
		loc = t.SavePath
	}
	it := download.Item{
		Ref: strings.ToLower(t.Hash), Name: t.Name, Category: t.Label,
		Status: st, RawState: t.State, Message: msg,
		Progress: download.Clamp01(float64(t.Progress) / 100),
		Size:     t.TotalWanted.Int64(), Downloaded: t.TotalDone.Int64(),
		Speed: t.DownloadRate.Int64(), Ratio: float64(t.Ratio),
		OutputPath: joinPath(loc, t.Name),
	}
	if eta := t.ETA.Int64(); eta > 0 && st == download.StatusDownloading {
		it.ETA = time.Duration(eta) * time.Second
	}
	return it
}

// joinPath joins dir and name with the separator dir already uses.
func joinPath(dir, name string) string {
	if dir == "" || name == "" {
		return dir
	}
	sep := "/"
	if strings.Contains(dir, `\`) && !strings.Contains(dir, "/") {
		sep = `\`
	}
	return strings.TrimRight(dir, `/\`) + sep + name
}

// mapState maps a Deluge torrent state (deluge/common.py TORRENT_STATE).
func mapState(raw string, finished bool, message string) (st download.Status, msg string) {
	switch raw {
	case "Downloading", "Checking", "Moving":
		return download.StatusDownloading, ""
	case "Allocating":
		return download.StatusQueued, ""
	case "Queued":
		if finished {
			return download.StatusCompleted, ""
		}
		return download.StatusQueued, ""
	case "Paused":
		if finished {
			return download.StatusCompleted, ""
		}
		return download.StatusPaused, ""
	case "Seeding":
		return download.StatusCompleted, ""
	case "Error":
		if message == "" || message == "OK" {
			message = "Deluge reported an error"
		}
		return download.StatusFailed, message
	}
	return download.StatusWarning, fmt.Sprintf("unknown Deluge state %q", raw)
}

func validHash(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, r := range ref {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func (c *Client) exists(ctx context.Context, op, ref string) error {
	if !validHash(ref) {
		return c.r.Err(op, download.ErrInvalidRequest, "invalid info hash", nil)
	}
	var st map[string]any
	if err := c.call(ctx, op, "core.get_torrent_status", []any{ref, []string{"hash"}}, &st); err != nil {
		return err
	}
	if len(st) == 0 {
		return c.r.Err(op, download.ErrNotFound, "", nil)
	}
	return nil
}

// Remove implements download.Client.
func (c *Client) Remove(ctx context.Context, ref string, deleteData bool) error {
	if err := c.exists(ctx, "remove", ref); err != nil {
		return err
	}
	var ok bool
	if err := c.call(ctx, "remove", "core.remove_torrent", []any{ref, deleteData}, &ok); err != nil {
		return err
	}
	if !ok {
		return c.r.Err("remove", download.ErrRejected, "Deluge did not remove the torrent", nil)
	}
	return nil
}

// Pause implements download.Client.
func (c *Client) Pause(ctx context.Context, ref string) error {
	return c.action(ctx, "pause", "core.pause_torrents", ref)
}

// Resume implements download.Client.
func (c *Client) Resume(ctx context.Context, ref string) error {
	return c.action(ctx, "resume", "core.resume_torrents", ref)
}

func (c *Client) action(ctx context.Context, op, method, ref string) error {
	if err := c.exists(ctx, op, ref); err != nil {
		return err
	}
	return c.call(ctx, op, method, []any{[]string{ref}}, nil)
}

// Categories implements download.Client: the labels of the Label plugin.
func (c *Client) Categories(ctx context.Context) ([]string, error) {
	ok, err := c.labelPlugin(ctx, "categories")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, c.r.Err("categories", download.ErrUnsupported, "enable the Label plugin in Deluge to use categories", nil)
	}
	var labels []string
	if err := c.call(ctx, "categories", "label.get_labels", nil, &labels); err != nil {
		return nil, err
	}
	return labels, nil
}

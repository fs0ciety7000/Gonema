package qbittorrent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/download/internal/httpc"
	"github.com/fs0ciety7000/gonema/internal/flight"
)

// DefaultPort is qBittorrent's default WebUI port.
const DefaultPort = 8080

// infiniteETA is the ETA qBittorrent reports when it cannot estimate one.
const infiniteETA = 8640000

// Client is a qBittorrent client. It is safe for concurrent use.
type Client struct {
	r        *httpc.Requester
	user     string
	pass     string
	category string
	origin   string

	mu      sync.Mutex
	session *session
	login   flight.Group[*session]
}

// session is one authenticated WebUI session.
type session struct {
	cookie *http.Cookie
	api    [3]int // WebAPI version
}

var _ download.Client = (*Client)(nil)

// New returns a client for cfg (Username and Password of the WebUI).
func New(cfg *download.Config) (*Client, error) {
	r, err := httpc.New(download.KindQBittorrent, cfg, DefaultPort)
	if err != nil {
		return nil, err
	}
	origin := url.URL{Scheme: r.Base.Scheme, Host: r.Base.Host}
	return &Client{r: r, user: cfg.Username, pass: cfg.Password, category: cfg.Category, origin: origin.String()}, nil
}

// Kind implements download.Client.
func (*Client) Kind() download.Kind { return download.KindQBittorrent }

func (c *Client) csrfHeaders() http.Header {
	return http.Header{"Referer": {c.origin}, "Origin": {c.origin}}
}

// doLogin opens a session and reads the WebAPI version.
func (c *Client) doLogin(ctx context.Context, op string) (*session, error) {
	form := url.Values{"username": {c.user}, "password": {c.pass}}
	resp, err := c.r.Do(ctx, &httpc.Request{
		Op: op, Method: http.MethodPost, Path: "api/v2/auth/login",
		Body: []byte(form.Encode()), ContentType: "application/x-www-form-urlencoded", Header: c.csrfHeaders(),
	})
	if err != nil {
		return nil, err
	}
	body := strings.TrimSpace(string(resp.Body))
	switch {
	case resp.Status == http.StatusForbidden:
		return nil, c.r.Err(op, download.ErrAuth, "qBittorrent banned this address after too many failed logins: wait or restart qBittorrent", nil)
	case resp.Status == http.StatusUnauthorized, resp.Status == http.StatusOK && body == "Fails.":
		return nil, c.r.Err(op, download.ErrAuth, "wrong qBittorrent WebUI user name or password", nil)
	}
	if err := c.r.StatusError(op, resp); err != nil {
		return nil, err
	}
	if body != "Ok." {
		return nil, c.r.Err(op, download.ErrUnexpected, "the login answer is not qBittorrent's: check the URL base", nil)
	}
	s := &session{}
	for _, ck := range (&http.Response{Header: resp.Header}).Cookies() {
		if strings.Contains(strings.ToUpper(ck.Name), "SID") && ck.Value != "" {
			s.cookie = &http.Cookie{Name: ck.Name, Value: ck.Value}
			c.r.AddSecret(ck.Value)
			break
		}
	}
	// No cookie: authentication is bypassed for this address (WebUI option);
	// calls work without one.
	ver, err := c.get(ctx, op, s, "api/v2/app/webapiVersion", nil)
	if err != nil {
		return nil, err
	}
	if err := c.r.StatusError(op, ver); err != nil {
		return nil, err
	}
	api, ok := parseVersion(string(ver.Body))
	if !ok {
		return nil, c.r.Err(op, download.ErrUnexpected, "unreadable qBittorrent WebAPI version", nil)
	}
	s.api = api
	return s, nil
}

// parseVersion parses "2.11.2" (or "v2.11"); missing parts are 0.
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func atLeast(v [3]int, major, minor int) bool {
	return v[0] > major || (v[0] == major && v[1] >= minor)
}

// current returns the open session, logging in when there is none.
func (c *Client) current(ctx context.Context, op string) (*session, error) {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s != nil {
		return s, nil
	}
	return c.relogin(ctx, op, nil)
}

// relogin replaces stale (nil: no session yet) with a new session; concurrent
// callers share one login.
func (c *Client) relogin(ctx context.Context, op string, stale *session) (*session, error) {
	s, _, err := c.login.Do(ctx, "login", func(fctx context.Context) (*session, error) {
		c.mu.Lock()
		cur := c.session
		c.mu.Unlock()
		if cur != nil && cur != stale {
			return cur, nil // another caller already logged in again
		}
		ns, err := c.doLogin(fctx, op)
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

func (c *Client) send(ctx context.Context, op string, s *session, req *httpc.Request) (*httpc.Response, error) {
	req.Op = op
	if req.Header == nil {
		req.Header = c.csrfHeaders()
	}
	if s.cookie != nil {
		req.Header.Set("Cookie", s.cookie.String())
	}
	return c.r.Do(ctx, req)
}

func (c *Client) get(ctx context.Context, op string, s *session, path string, q url.Values) (*httpc.Response, error) {
	return c.send(ctx, op, s, &httpc.Request{Path: path, Query: q})
}

// call runs an authenticated request, logging in again once on 401/403.
// build is called for each attempt (bodies are single-use readers).
func (c *Client) call(ctx context.Context, op string, build func(s *session) *httpc.Request) (*httpc.Response, error) {
	s, err := c.current(ctx, op)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.send(ctx, op, s, build(s))
		if err != nil {
			return nil, err
		}
		if resp.Status != http.StatusForbidden && resp.Status != http.StatusUnauthorized {
			return resp, nil
		}
		if attempt > 0 {
			return nil, c.r.Err(op, download.ErrAuth, "qBittorrent refused the session after a new login", nil)
		}
		if s, err = c.relogin(ctx, op, s); err != nil {
			return nil, err
		}
	}
}

func form(path string, v url.Values) func(*session) *httpc.Request {
	return func(*session) *httpc.Request {
		return &httpc.Request{Method: http.MethodPost, Path: path, Body: []byte(v.Encode()), ContentType: "application/x-www-form-urlencoded"}
	}
}

// Test implements download.Client: login and WebAPI version.
func (c *Client) Test(ctx context.Context) error {
	c.mu.Lock()
	c.session = nil // always prove the credentials
	c.mu.Unlock()
	_, err := c.current(ctx, "test")
	return err
}

// Add implements download.Client.
func (c *Client) Add(ctx context.Context, req download.AddRequest) (string, error) {
	if err := req.Validate(decision.Torrent); err != nil {
		return "", c.r.Err("add", download.ErrInvalidRequest, "", err)
	}
	ref, err := localHash(&req)
	if err != nil {
		return "", c.r.Err("add", download.ErrInvalidRequest, "", err)
	}
	paused := strconv.FormatBool(req.Paused)
	fields := []httpc.Field{{Name: "paused", Value: paused}, {Name: "stopped", Value: paused}}
	if cat := req.CategoryOr(c.category); cat != "" {
		fields = append(fields, httpc.Field{Name: "category", Value: cat})
	}
	if req.SeedRatio > 0 {
		fields = append(fields, httpc.Field{Name: "ratioLimit", Value: strconv.FormatFloat(req.SeedRatio, 'f', 2, 64)})
	}
	if req.SeedTime > 0 {
		fields = append(fields, httpc.Field{Name: "seedingTimeLimit", Value: strconv.FormatInt(int64(max(1, req.SeedTime/time.Minute)), 10)})
	}
	var files []httpc.File
	if req.URL != "" {
		fields = append(fields, httpc.Field{Name: "urls", Value: req.URL})
	} else {
		files = append(files, httpc.File{Field: "torrents", Filename: req.Filename, ContentType: "application/x-bittorrent", Data: req.Data})
	}
	body, ct, err := httpc.Multipart(fields, files...)
	if err != nil {
		return "", c.r.Err("add", download.ErrInvalidRequest, "", err)
	}
	resp, err := c.call(ctx, "add", func(*session) *httpc.Request {
		return &httpc.Request{Method: http.MethodPost, Path: "api/v2/torrents/add", Body: body, ContentType: ct}
	})
	if err != nil {
		return "", err
	}
	switch resp.Status {
	case http.StatusUnsupportedMediaType:
		return "", c.r.Err("add", download.ErrRejected, "qBittorrent says the torrent file is not valid", nil)
	case http.StatusConflict:
		return "", c.r.Err("add", download.ErrRejected, "qBittorrent could not add the torrent (already present?)", nil)
	}
	if err := c.r.StatusError("add", resp); err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(resp.Body))
	if text == "Fails." {
		return "", c.r.Err("add", download.ErrRejected, "qBittorrent could not add the torrent (already present or invalid link?)", nil)
	}
	if ref == "" && strings.HasPrefix(text, "{") {
		var out struct {
			AddedTorrentIDs []string `json:"added_torrent_ids"`
		}
		if err := json.Unmarshal(resp.Body, &out); err == nil && len(out.AddedTorrentIDs) > 0 {
			ref = strings.ToLower(out.AddedTorrentIDs[0])
		}
	}
	if ref == "" {
		return "", c.r.Err("add", download.ErrUnknownRef, "add the .torrent file content instead of its link", nil)
	}
	if req.Priority == download.PriorityForce && !req.Paused {
		resp, err := c.call(ctx, "add", form("api/v2/torrents/setForceStart", url.Values{"hashes": {ref}, "value": {"true"}}))
		if err != nil {
			return ref, err
		}
		if err := c.r.StatusError("add", resp); err != nil {
			return ref, err
		}
	}
	return ref, nil
}

// localHash returns the info hash known before adding ("" for http links).
func localHash(req *download.AddRequest) (string, error) {
	switch {
	case len(req.Data) > 0:
		return download.InfoHash(req.Data)
	case download.IsMagnet(req.URL):
		return download.MagnetInfoHash(req.URL)
	}
	return "", nil
}

type torrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	State       string  `json:"state"`
	Progress    float64 `json:"progress"`
	Size        int64   `json:"size"`
	Downloaded  int64   `json:"downloaded"`
	AmountLeft  int64   `json:"amount_left"`
	DLSpeed     int64   `json:"dlspeed"`
	ETA         int64   `json:"eta"`
	SavePath    string  `json:"save_path"`
	ContentPath string  `json:"content_path"`
	Ratio       float64 `json:"ratio"`
}

func (c *Client) torrents(ctx context.Context, op string, q url.Values) ([]torrent, error) {
	resp, err := c.call(ctx, op, func(*session) *httpc.Request {
		return &httpc.Request{Path: "api/v2/torrents/info", Query: q}
	})
	if err != nil {
		return nil, err
	}
	if err := c.r.StatusError(op, resp); err != nil {
		return nil, err
	}
	var ts []torrent
	if err := json.Unmarshal(resp.Body, &ts); err != nil {
		return nil, c.r.Err(op, download.ErrUnexpected, "cannot decode the torrent list", err)
	}
	return ts, nil
}

// Items implements download.Client.
func (c *Client) Items(ctx context.Context) ([]download.Item, error) {
	q := url.Values{}
	if c.category != "" {
		q.Set("category", c.category)
	}
	ts, err := c.torrents(ctx, "items", q)
	if err != nil {
		return nil, err
	}
	items := make([]download.Item, 0, len(ts))
	for i := range ts {
		items = append(items, item(&ts[i]))
	}
	return items, nil
}

func item(t *torrent) download.Item {
	st, msg := mapState(t.State)
	it := download.Item{
		Ref: strings.ToLower(t.Hash), Name: t.Name, Category: t.Category,
		Status: st, RawState: t.State, Message: msg,
		Progress: download.Clamp01(t.Progress), Size: t.Size,
		Downloaded: max(0, t.Size-t.AmountLeft), Speed: t.DLSpeed,
		OutputPath: t.ContentPath, Ratio: t.Ratio,
	}
	if it.OutputPath == "" {
		it.OutputPath = t.SavePath
	}
	if t.ETA > 0 && t.ETA < infiniteETA {
		it.ETA = time.Duration(t.ETA) * time.Second
	}
	return it
}

// mapState maps a torrent state (4.x and 5.x names).
func mapState(raw string) (st download.Status, msg string) {
	switch raw {
	case "error":
		return download.StatusFailed, "qBittorrent reported an error (see its log)"
	case "missingFiles":
		return download.StatusFailed, "the data files are missing"
	case "uploading", "stalledUP", "queuedUP", "forcedUP", "checkingUP", "pausedUP", "stoppedUP":
		return download.StatusCompleted, ""
	case "downloading", "forcedDL", "metaDL", "forcedMetaDL", "allocating", "checkingDL", "moving":
		return download.StatusDownloading, ""
	case "queuedDL", "checkingResumeData":
		return download.StatusQueued, ""
	case "pausedDL", "stoppedDL":
		return download.StatusPaused, ""
	case "stalledDL":
		return download.StatusWarning, "stalled: no connection to peers"
	}
	return download.StatusWarning, fmt.Sprintf("unknown qBittorrent state %q", raw)
}

// validHash accepts a 40 hex (v1) or 64 hex (v2) info hash.
func validHash(ref string) bool {
	if len(ref) != 40 && len(ref) != 64 {
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
	ts, err := c.torrents(ctx, op, url.Values{"hashes": {ref}})
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(ts, func(t torrent) bool { return strings.EqualFold(t.Hash, ref) }) {
		return c.r.Err(op, download.ErrNotFound, "", nil)
	}
	return nil
}

func (c *Client) post(ctx context.Context, op, path string, v url.Values) error {
	resp, err := c.call(ctx, op, form(path, v))
	if err != nil {
		return err
	}
	return c.r.StatusError(op, resp)
}

// Remove implements download.Client.
func (c *Client) Remove(ctx context.Context, ref string, deleteData bool) error {
	if err := c.exists(ctx, "remove", ref); err != nil {
		return err
	}
	return c.post(ctx, "remove", "api/v2/torrents/delete", url.Values{"hashes": {ref}, "deleteFiles": {strconv.FormatBool(deleteData)}})
}

// Pause implements download.Client (torrents/stop since WebAPI 2.11, else pause).
func (c *Client) Pause(ctx context.Context, ref string) error {
	return c.toggle(ctx, "pause", ref, "stop", "pause")
}

// Resume implements download.Client (torrents/start since WebAPI 2.11, else resume).
func (c *Client) Resume(ctx context.Context, ref string) error {
	return c.toggle(ctx, "resume", ref, "start", "resume")
}

func (c *Client) toggle(ctx context.Context, op, ref, v5, v4 string) error {
	if err := c.exists(ctx, op, ref); err != nil {
		return err
	}
	resp, err := c.call(ctx, op, func(s *session) *httpc.Request {
		name := v4
		if atLeast(s.api, 2, 11) {
			name = v5
		}
		return form("api/v2/torrents/"+name, url.Values{"hashes": {ref}})(s)
	})
	if err != nil {
		return err
	}
	return c.r.StatusError(op, resp)
}

// Categories implements download.Client.
func (c *Client) Categories(ctx context.Context) ([]string, error) {
	resp, err := c.call(ctx, "categories", func(*session) *httpc.Request {
		return &httpc.Request{Path: "api/v2/torrents/categories"}
	})
	if err != nil {
		return nil, err
	}
	if err := c.r.StatusError("categories", resp); err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &m); err != nil {
		return nil, c.r.Err("categories", download.ErrUnexpected, "cannot decode the categories", err)
	}
	cats := make([]string, 0, len(m))
	for k := range m {
		cats = append(cats, k)
	}
	slices.Sort(cats)
	return cats, nil
}

package transmission

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
)

// DefaultPort is Transmission's default RPC port.
const DefaultPort = 9091

const sessionHeader = "X-Transmission-Session-Id"

// Client is a Transmission client. It is safe for concurrent use.
type Client struct {
	r        *httpc.Requester
	path     string // RPC path relative to the host
	auth     string
	category string
	dir      string
	tag      atomic.Uint64

	mu        sync.Mutex
	sessionID string
}

var _ download.Client = (*Client)(nil)

// New returns a client for cfg. Without URLBase the RPC path is
// /transmission/rpc; with one it is <URLBase>/rpc.
func New(cfg *download.Config) (*Client, error) {
	c2 := *cfg
	path := "rpc"
	if strings.Trim(c2.URLBase, "/ ") == "" {
		c2.URLBase = "/transmission"
	}
	r, err := httpc.New(download.KindTransmission, &c2, DefaultPort)
	if err != nil {
		return nil, err
	}
	c := &Client{r: r, path: path, category: cfg.Category, dir: strings.TrimSpace(cfg.Directory)}
	if cfg.Username != "" || cfg.Password != "" {
		c.auth = httpc.BasicAuth(cfg.Username, cfg.Password)
		r.AddSecret(strings.TrimPrefix(c.auth, "Basic "))
	}
	return c, nil
}

// Kind implements download.Client.
func (*Client) Kind() download.Kind { return download.KindTransmission }

type rpcResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
}

// rpc calls method and decodes its arguments into dst (may be nil). It
// performs the session-id handshake when Transmission answers 409.
func (c *Client) rpc(ctx context.Context, op, method string, args, dst any) error {
	body, err := json.Marshal(map[string]any{"method": method, "arguments": args, "tag": c.tag.Add(1)})
	if err != nil {
		return c.r.Err(op, download.ErrInvalidRequest, "", err)
	}
	var resp *httpc.Response
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		sid := c.sessionID
		c.mu.Unlock()
		h := http.Header{}
		if sid != "" {
			h.Set(sessionHeader, sid)
		}
		if c.auth != "" {
			h.Set("Authorization", c.auth)
		}
		resp, err = c.r.Do(ctx, &httpc.Request{Op: op, Method: http.MethodPost, Path: c.path, Body: body, ContentType: "application/json", Header: h})
		if err != nil {
			return err
		}
		if resp.Status != http.StatusConflict {
			break
		}
		nsid := resp.Header.Get(sessionHeader)
		if nsid == "" || attempt >= 2 {
			return c.r.Err(op, download.ErrUnexpected, "Transmission keeps refusing the session id (409)", nil)
		}
		c.r.AddSecret(nsid)
		c.mu.Lock()
		c.sessionID = nsid
		c.mu.Unlock()
	}
	switch resp.Status {
	case http.StatusUnauthorized:
		return c.r.Err(op, download.ErrAuth, "wrong Transmission user name or password", nil)
	case http.StatusForbidden:
		return c.r.Err(op, download.ErrAPIDisabled, "Transmission refuses this address: add it to rpc-whitelist in settings.json", nil)
	}
	if err := c.r.StatusError(op, resp); err != nil {
		return err
	}
	var out rpcResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil || out.Result == "" {
		return c.r.Err(op, download.ErrUnexpected, "the answer is not Transmission RPC: check the URL base", nil)
	}
	if out.Result != "success" {
		return c.r.Err(op, download.ErrRejected, "Transmission: "+out.Result, nil)
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(out.Arguments, dst); err != nil {
		return c.r.Err(op, download.ErrUnexpected, "cannot decode the Transmission answer to "+method, err)
	}
	return nil
}

// Test implements download.Client.
func (c *Client) Test(ctx context.Context) error {
	var s struct {
		Version    string `json:"version"`
		RPCVersion int    `json:"rpc-version"`
	}
	if err := c.rpc(ctx, "test", "session-get", map[string]any{"fields": []string{"version", "rpc-version"}}, &s); err != nil {
		return err
	}
	if s.RPCVersion == 0 {
		return c.r.Err("test", download.ErrUnexpected, "no RPC version in the answer: is this Transmission?", nil)
	}
	return nil
}

func bandwidth(p download.Priority) int {
	switch p {
	case download.PriorityLow:
		return -1
	case download.PriorityHigh, download.PriorityForce:
		return 1
	}
	return 0
}

// categoryDir returns Directory/category, with the separator of Directory.
func (c *Client) categoryDir(cat string) string {
	if c.dir == "" {
		return ""
	}
	if cat == "" {
		return c.dir
	}
	sep := "/"
	if strings.Contains(c.dir, `\`) && !strings.Contains(c.dir, "/") {
		sep = `\`
	}
	return strings.TrimRight(c.dir, `/\`) + sep + cat
}

type added struct {
	Hash string `json:"hashString"`
}

// Add implements download.Client.
func (c *Client) Add(ctx context.Context, req download.AddRequest) (string, error) {
	if err := req.Validate(decision.Torrent); err != nil {
		return "", c.r.Err("add", download.ErrInvalidRequest, "", err)
	}
	cat := req.CategoryOr(c.category)
	args := map[string]any{"paused": req.Paused, "bandwidthPriority": bandwidth(req.Priority)}
	if req.URL != "" {
		args["filename"] = req.URL
	} else {
		args["metainfo"] = base64.StdEncoding.EncodeToString(req.Data)
	}
	if cat != "" {
		args["labels"] = []string{cat}
	}
	if d := c.categoryDir(cat); d != "" {
		args["download-dir"] = d
	}
	var out struct {
		Added     *added `json:"torrent-added"`
		Duplicate *added `json:"torrent-duplicate"`
	}
	if err := c.rpc(ctx, "add", "torrent-add", args, &out); err != nil {
		return "", err
	}
	a := out.Added
	if a == nil {
		a = out.Duplicate
	}
	if a == nil || a.Hash == "" {
		return "", c.r.Err("add", download.ErrUnknownRef, "Transmission returned no hash", nil)
	}
	ref := strings.ToLower(a.Hash)
	set := map[string]any{}
	if req.SeedRatio > 0 {
		set["seedRatioLimit"] = req.SeedRatio
		set["seedRatioMode"] = 1
	}
	if req.SeedTime > 0 {
		set["seedIdleLimit"] = max(1, int64(req.SeedTime/time.Minute))
		set["seedIdleMode"] = 1
	}
	if len(set) > 0 {
		set["ids"] = []string{ref}
		if err := c.rpc(ctx, "add", "torrent-set", set, nil); err != nil {
			return ref, err
		}
	}
	if req.Priority == download.PriorityForce && !req.Paused {
		if err := c.rpc(ctx, "add", "torrent-start-now", map[string]any{"ids": []string{ref}}, nil); err != nil {
			return ref, err
		}
	}
	return ref, nil
}

var fields = []string{
	"hashString", "name", "status", "percentDone", "sizeWhenDone", "leftUntilDone", "rateDownload",
	"eta", "downloadDir", "uploadRatio", "error", "errorString", "labels", "isFinished",
}

type torrent struct {
	Hash          string    `json:"hashString"`
	Name          string    `json:"name"`
	Status        int       `json:"status"`
	PercentDone   float64   `json:"percentDone"`
	SizeWhenDone  int64     `json:"sizeWhenDone"`
	LeftUntilDone int64     `json:"leftUntilDone"`
	RateDownload  int64     `json:"rateDownload"`
	ETA           int64     `json:"eta"`
	DownloadDir   string    `json:"downloadDir"`
	UploadRatio   httpc.Num `json:"uploadRatio"`
	Error         int       `json:"error"`
	ErrorString   string    `json:"errorString"`
	Labels        []string  `json:"labels"`
	IsFinished    bool      `json:"isFinished"`
}

func (c *Client) torrents(ctx context.Context, op string, ids []string) ([]torrent, error) {
	args := map[string]any{"fields": fields}
	if ids != nil {
		args["ids"] = ids
	}
	var out struct {
		Torrents []torrent `json:"torrents"`
	}
	if err := c.rpc(ctx, op, "torrent-get", args, &out); err != nil {
		return nil, err
	}
	return out.Torrents, nil
}

// Items implements download.Client.
func (c *Client) Items(ctx context.Context) ([]download.Item, error) {
	ts, err := c.torrents(ctx, "items", nil)
	if err != nil {
		return nil, err
	}
	catDir := strings.TrimRight(c.categoryDir(c.category), `/\`)
	items := make([]download.Item, 0, len(ts))
	for i := range ts {
		t := &ts[i]
		cat := ""
		switch {
		case c.category != "" && slices.ContainsFunc(t.Labels, func(l string) bool { return strings.EqualFold(l, c.category) }):
			cat = c.category
		case c.category != "" && c.dir != "" && strings.TrimRight(t.DownloadDir, `/\`) == catDir:
			cat = c.category
		case c.category != "":
			continue
		case len(t.Labels) > 0:
			cat = t.Labels[0]
		}
		it := item(t)
		it.Category = cat
		items = append(items, it)
	}
	return items, nil
}

func item(t *torrent) download.Item {
	st, msg := mapStatus(t.Status, t.Error, t.ErrorString, t.LeftUntilDone == 0 && t.PercentDone >= 1)
	it := download.Item{
		Ref: strings.ToLower(t.Hash), Name: t.Name,
		Status: st, RawState: statusNames[t.Status], Message: msg,
		Progress: download.Clamp01(t.PercentDone), Size: t.SizeWhenDone,
		Downloaded: max(0, t.SizeWhenDone-t.LeftUntilDone), Speed: t.RateDownload,
		Ratio: max(0, float64(t.UploadRatio)), OutputPath: joinPath(t.DownloadDir, t.Name),
	}
	if it.RawState == "" {
		it.RawState = fmt.Sprintf("status %d", t.Status)
	}
	if t.ETA > 0 && st == download.StatusDownloading {
		it.ETA = time.Duration(t.ETA) * time.Second
	}
	return it
}

var statusNames = map[int]string{
	0: "stopped", 1: "check pending", 2: "checking", 3: "download pending", 4: "downloading", 5: "seed pending", 6: "seeding",
}

// mapStatus maps tr_torrent_activity and tr_stat_errtype (done: nothing left).
func mapStatus(status, errType int, errString string, done bool) (st download.Status, msg string) {
	if errType == 3 {
		if errString == "" {
			errString = "Transmission reported a local error"
		}
		return download.StatusFailed, errString
	}
	switch status {
	case 0:
		st = download.StatusPaused
		if done {
			st = download.StatusCompleted
		}
	case 1, 3:
		st = download.StatusQueued
	case 2, 4:
		st = download.StatusDownloading
	case 5, 6:
		st = download.StatusCompleted
	default:
		return download.StatusWarning, fmt.Sprintf("unknown Transmission status %d", status)
	}
	if (errType == 1 || errType == 2) && st != download.StatusCompleted {
		if errString == "" {
			errString = "tracker problem"
		}
		return download.StatusWarning, errString
	}
	return st, ""
}

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
	ts, err := c.torrents(ctx, op, []string{ref})
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(ts, func(t torrent) bool { return strings.EqualFold(t.Hash, ref) }) {
		return c.r.Err(op, download.ErrNotFound, "", nil)
	}
	return nil
}

// Remove implements download.Client.
func (c *Client) Remove(ctx context.Context, ref string, deleteData bool) error {
	if err := c.exists(ctx, "remove", ref); err != nil {
		return err
	}
	return c.rpc(ctx, "remove", "torrent-remove", map[string]any{"ids": []string{ref}, "delete-local-data": deleteData}, nil)
}

// Pause implements download.Client.
func (c *Client) Pause(ctx context.Context, ref string) error {
	return c.action(ctx, "pause", "torrent-stop", ref)
}

// Resume implements download.Client.
func (c *Client) Resume(ctx context.Context, ref string) error {
	return c.action(ctx, "resume", "torrent-start", ref)
}

func (c *Client) action(ctx context.Context, op, method, ref string) error {
	if err := c.exists(ctx, op, ref); err != nil {
		return err
	}
	return c.rpc(ctx, op, method, map[string]any{"ids": []string{ref}}, nil)
}

// Categories implements download.Client: Transmission has no category list.
func (c *Client) Categories(context.Context) ([]string, error) {
	return nil, c.r.Err("categories", download.ErrUnsupported, "Transmission has no categories; Gonema uses labels", nil)
}

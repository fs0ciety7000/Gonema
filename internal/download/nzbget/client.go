package nzbget

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/download/internal/httpc"
)

// DefaultPort is NZBGet's default web port.
const DefaultPort = 6789

// MinMajorVersion is the oldest supported NZBGet (editqueue v18 signature).
const MinMajorVersion = 18

// Client is an NZBGet client. It is safe for concurrent use.
type Client struct {
	r        *httpc.Requester
	auth     string // Authorization header value
	category string
	id       atomic.Uint64
}

var _ download.Client = (*Client)(nil)

// New returns a client for cfg (Username and Password of the control user).
func New(cfg *download.Config) (*Client, error) {
	r, err := httpc.New(download.KindNZBGet, cfg, DefaultPort)
	if err != nil {
		return nil, err
	}
	c := &Client{r: r, category: cfg.Category}
	if cfg.Username != "" || cfg.Password != "" {
		c.auth = httpc.BasicAuth(cfg.Username, cfg.Password)
		r.AddSecret(strings.TrimPrefix(c.auth, "Basic "))
	}
	return c, nil
}

// Kind implements download.Client.
func (*Client) Kind() download.Kind { return download.KindNZBGet }

type rpcRequest struct {
	Version string `json:"version"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      uint64 `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// rpc calls method with positional params and decodes the result into dst.
func (c *Client) rpc(ctx context.Context, op, method string, params []any, dst any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(rpcRequest{Version: "1.1", Method: method, Params: params, ID: c.id.Add(1)})
	if err != nil {
		return c.r.Err(op, download.ErrInvalidRequest, "", err)
	}
	h := http.Header{}
	if c.auth != "" {
		h.Set("Authorization", c.auth)
	}
	resp, err := c.r.Do(ctx, &httpc.Request{Op: op, Method: http.MethodPost, Path: "jsonrpc", Body: body, ContentType: "application/json", Header: h})
	if err != nil {
		return err
	}
	if resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden {
		return c.r.Err(op, download.ErrAuth, "check the NZBGet ControlUsername and ControlPassword (Settings > Security)", nil)
	}
	if err := c.r.StatusError(op, resp); err != nil {
		return err
	}
	var out rpcResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return c.r.Err(op, download.ErrUnexpected, "the answer is not NZBGet JSON-RPC: check the URL base", nil)
	}
	if out.Error != nil {
		kind := download.ErrRejected
		detail := fmt.Sprintf("NZBGet error %d: %s", out.Error.Code, out.Error.Message)
		if out.Error.Code == 401 {
			kind = download.ErrAuth
			detail = "this NZBGet user may not do that: use the control user (ControlUsername)"
		}
		return c.r.Err(op, kind, detail, nil)
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(out.Result, dst); err != nil {
		return c.r.Err(op, download.ErrUnexpected, "cannot decode the NZBGet answer to "+method, err)
	}
	return nil
}

// Test implements download.Client: version, a queue read (control user) and
// the configured category.
func (c *Client) Test(ctx context.Context) error {
	var v string
	if err := c.rpc(ctx, "test", "version", nil, &v); err != nil {
		return err
	}
	major, err := strconv.Atoi(strings.SplitN(strings.TrimSpace(v), ".", 2)[0])
	if err != nil {
		return c.r.Err("test", download.ErrUnexpected, "unreadable NZBGet version", nil)
	}
	if major < MinMajorVersion {
		return c.r.Err("test", download.ErrUnsupported, fmt.Sprintf("NZBGet %s is too old: version %d or later is required", v, MinMajorVersion), nil)
	}
	if err := c.rpc(ctx, "test", "listgroups", []any{0}, nil); err != nil {
		return err
	}
	if c.category == "" {
		return nil
	}
	cats, err := c.categories(ctx, "test")
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(cats, func(s string) bool { return strings.EqualFold(s, c.category) }) {
		return c.r.Err("test", download.ErrRejected, fmt.Sprintf("category %q does not exist in NZBGet: create it in Settings > Categories", c.category), nil)
	}
	return nil
}

func priority(p download.Priority) int {
	switch p {
	case download.PriorityLow:
		return -50
	case download.PriorityHigh:
		return 50
	case download.PriorityForce:
		return 900
	}
	return 0
}

// Add implements download.Client.
func (c *Client) Add(ctx context.Context, req download.AddRequest) (string, error) {
	if err := req.Validate(decision.Usenet); err != nil {
		return "", c.r.Err("add", download.ErrInvalidRequest, "", err)
	}
	filename, content := "", req.URL
	if req.URL == "" {
		filename = req.Filename
		content = base64.StdEncoding.EncodeToString(req.Data)
	}
	if req.Name != "" {
		filename = req.Name
		if !strings.HasSuffix(strings.ToLower(filename), ".nzb") {
			filename += ".nzb"
		}
	}
	params := []any{
		filename, content, req.CategoryOr(c.category), priority(req.Priority),
		false, req.Paused, "", 0, "SCORE", []any{},
	}
	var id int64
	if err := c.rpc(ctx, "add", "append", params, &id); err != nil {
		return "", err
	}
	if id <= 0 {
		return "", c.r.Err("add", download.ErrRejected, "NZBGet refused the NZB (invalid file or unreachable link?)", nil)
	}
	return strconv.FormatInt(id, 10), nil
}

type group struct {
	NZBID           int64  `json:"NZBID"`
	NZBName         string `json:"NZBName"`
	Category        string `json:"Category"`
	Status          string `json:"Status"`
	DestDir         string `json:"DestDir"`
	FinalDir        string `json:"FinalDir"`
	FileSizeLo      uint32 `json:"FileSizeLo"`
	FileSizeHi      uint32 `json:"FileSizeHi"`
	RemainingSizeLo uint32 `json:"RemainingSizeLo"`
	RemainingSizeHi uint32 `json:"RemainingSizeHi"`
	PausedSizeLo    uint32 `json:"PausedSizeLo"`
	PausedSizeHi    uint32 `json:"PausedSizeHi"`
	ActiveDownloads int    `json:"ActiveDownloads"`
	PostInfoText    string `json:"PostInfoText"`
}

type historyEntry struct {
	NZBID              int64  `json:"NZBID"`
	Kind               string `json:"Kind"`
	Name               string `json:"Name"`
	Category           string `json:"Category"`
	Status             string `json:"Status"`
	DestDir            string `json:"DestDir"`
	FinalDir           string `json:"FinalDir"`
	FileSizeLo         uint32 `json:"FileSizeLo"`
	FileSizeHi         uint32 `json:"FileSizeHi"`
	DownloadedSizeLo   uint32 `json:"DownloadedSizeLo"`
	DownloadedSizeHi   uint32 `json:"DownloadedSizeHi"`
	RemainingFileCount int    `json:"RemainingFileCount"`
}

type status struct {
	DownloadRate int64 `json:"DownloadRate"`
}

func size64(hi, lo uint32) int64 { return int64(hi)<<32 | int64(lo) }

func (c *Client) listGroups(ctx context.Context, op string) ([]group, error) {
	var gs []group
	err := c.rpc(ctx, op, "listgroups", []any{0}, &gs)
	return gs, err
}

func (c *Client) history(ctx context.Context, op string) ([]historyEntry, error) {
	var hs []historyEntry
	err := c.rpc(ctx, op, "history", []any{false}, &hs)
	return hs, err
}

// Items implements download.Client.
func (c *Client) Items(ctx context.Context) ([]download.Item, error) {
	gs, err := c.listGroups(ctx, "items")
	if err != nil {
		return nil, err
	}
	hs, err := c.history(ctx, "items")
	if err != nil {
		return nil, err
	}
	var st status
	if err := c.rpc(ctx, "items", "status", nil, &st); err != nil {
		return nil, err
	}
	keep := func(cat string) bool { return c.category == "" || strings.EqualFold(cat, c.category) }
	items := make([]download.Item, 0, len(gs)+len(hs))
	speedGiven := false
	for i := range gs {
		if !keep(gs[i].Category) {
			continue
		}
		it := groupItem(&gs[i])
		if gs[i].ActiveDownloads > 0 && !speedGiven && st.DownloadRate > 0 {
			speedGiven = true
			it.Speed = st.DownloadRate
			if left := it.Size - it.Downloaded; left > 0 {
				it.ETA = time.Duration(left/st.DownloadRate) * time.Second
			}
		}
		items = append(items, it)
	}
	for i := range hs {
		if hs[i].Kind == "DUP" || !keep(hs[i].Category) {
			continue
		}
		items = append(items, historyItem(&hs[i]))
	}
	return items, nil
}

func outputDir(final, dest string) string {
	if final != "" {
		return final
	}
	return dest
}

func groupItem(g *group) download.Item {
	st, msg := mapGroupStatus(g.Status)
	if msg == "" {
		msg = g.PostInfoText
	}
	size := size64(g.FileSizeHi, g.FileSizeLo)
	left := max(0, size64(g.RemainingSizeHi, g.RemainingSizeLo)-size64(g.PausedSizeHi, g.PausedSizeLo))
	it := download.Item{
		Ref: strconv.FormatInt(g.NZBID, 10), Name: g.NZBName, Category: g.Category,
		Status: st, RawState: g.Status, Message: msg,
		Size: size, Downloaded: max(0, size-left),
		OutputPath: outputDir(g.FinalDir, g.DestDir),
	}
	if size > 0 {
		it.Progress = download.Clamp01(float64(it.Downloaded) / float64(size))
	}
	return it
}

func historyItem(h *historyEntry) download.Item {
	st, msg := mapHistoryStatus(h.Status)
	it := download.Item{
		Ref: strconv.FormatInt(h.NZBID, 10), Name: h.Name, Category: h.Category,
		Status: st, RawState: h.Status, Message: msg,
		Size: size64(h.FileSizeHi, h.FileSizeLo), Downloaded: size64(h.DownloadedSizeHi, h.DownloadedSizeLo),
		OutputPath: outputDir(h.FinalDir, h.DestDir),
	}
	if st != download.StatusFailed {
		it.Progress = 1
	}
	return it
}

// mapGroupStatus maps a listgroups Status.
func mapGroupStatus(raw string) (st download.Status, msg string) {
	switch raw {
	case "QUEUED":
		return download.StatusQueued, ""
	case "FETCHING":
		return download.StatusQueued, "fetching the NZB file"
	case "PAUSED":
		return download.StatusPaused, ""
	case "DOWNLOADING":
		return download.StatusDownloading, ""
	case "PP_QUEUED", "LOADING_PARS", "VERIFYING_SOURCES", "REPAIRING", "VERIFYING_REPAIRED", "RENAMING",
		"UNPACKING", "MOVING", "POST_UNPACK_RENAMING", "POST_DOWNLOAD_RENAMING", "EXECUTING_SCRIPT", "PP_FINISHED":
		return download.StatusDownloading, ""
	}
	return download.StatusWarning, fmt.Sprintf("unknown NZBGet state %q", raw)
}

// historyMessages explains history statuses (from the history method page).
var historyMessages = map[string]string{
	"WARNING/SCRIPT":     "a post-processing script failed",
	"WARNING/SPACE":      "unpack failed: not enough disk space",
	"WARNING/PASSWORD":   "unpack failed: password missing or wrong",
	"WARNING/DAMAGED":    "damaged: par-check is set to manual",
	"WARNING/REPAIRABLE": "damaged but repairable: par-repair is disabled",
	"WARNING/HEALTH":     "health below 100% and no par files",
	"WARNING/SKIPPED":    "the URL did not return an NZB file",
	"DELETED/MANUAL":     "deleted manually in NZBGet",
	"DELETED/DUPE":       "deleted by the duplicate check",
	"DELETED/COPY":       "deleted: the same NZB is already queued or in history",
	"DELETED/GOOD":       "deleted: a duplicate was already marked good",
	"FAILURE/PAR":        "par-check failed",
	"FAILURE/UNPACK":     "unpack failed",
	"FAILURE/MOVE":       "moving the files to the destination failed",
	"FAILURE/SCAN":       "malformed NZB file",
	"FAILURE/BAD":        "marked as bad in NZBGet",
	"FAILURE/HEALTH":     "health below critical: articles missing (incomplete or out of retention)",
	"FAILURE/FETCH":      "fetching the NZB from the URL failed",
}

// mapHistoryStatus maps a history Status ("SUCCESS/ALL", "FAILURE/UNPACK"...).
func mapHistoryStatus(raw string) (st download.Status, msg string) {
	msg = historyMessages[raw]
	prefix, _, _ := strings.Cut(raw, "/")
	switch prefix {
	case "SUCCESS":
		return download.StatusCompleted, ""
	case "WARNING":
		if msg == "" {
			msg = raw
		}
		return download.StatusWarning, msg
	case "FAILURE", "DELETED":
		if msg == "" {
			msg = raw
		}
		return download.StatusFailed, msg
	}
	return download.StatusWarning, fmt.Sprintf("unknown NZBGet state %q", raw)
}

func parseRef(ref string) (int64, bool) {
	id, err := strconv.ParseInt(ref, 10, 64)
	return id, err == nil && id > 0
}

// locate reports whether id is in the queue or in the history.
func (c *Client) locate(ctx context.Context, op string, id int64) (inQueue, inHistory bool, err error) {
	gs, err := c.listGroups(ctx, op)
	if err != nil {
		return false, false, err
	}
	if slices.ContainsFunc(gs, func(g group) bool { return g.NZBID == id }) {
		return true, false, nil
	}
	hs, err := c.history(ctx, op)
	if err != nil {
		return false, false, err
	}
	return false, slices.ContainsFunc(hs, func(h historyEntry) bool { return h.NZBID == id }), nil
}

func (c *Client) edit(ctx context.Context, op, command string, id int64) error {
	var ok bool
	if err := c.rpc(ctx, op, "editqueue", []any{command, "", []int64{id}}, &ok); err != nil {
		return err
	}
	if !ok {
		return c.r.Err(op, download.ErrRejected, "NZBGet refused "+command, nil)
	}
	return nil
}

// Remove implements download.Client. deleteData has no effect, see the
// package documentation.
func (c *Client) Remove(ctx context.Context, ref string, _ bool) error {
	id, ok := parseRef(ref)
	if !ok {
		return c.r.Err("remove", download.ErrInvalidRequest, "invalid NZBID", nil)
	}
	inQ, inH, err := c.locate(ctx, "remove", id)
	switch {
	case err != nil:
		return err
	case inQ:
		return c.edit(ctx, "remove", "GroupFinalDelete", id)
	case inH:
		return c.edit(ctx, "remove", "HistoryFinalDelete", id)
	}
	return c.r.Err("remove", download.ErrNotFound, "", nil)
}

// Pause implements download.Client.
func (c *Client) Pause(ctx context.Context, ref string) error {
	return c.groupAction(ctx, "pause", "GroupPause", ref)
}

// Resume implements download.Client.
func (c *Client) Resume(ctx context.Context, ref string) error {
	return c.groupAction(ctx, "resume", "GroupResume", ref)
}

func (c *Client) groupAction(ctx context.Context, op, command, ref string) error {
	id, ok := parseRef(ref)
	if !ok {
		return c.r.Err(op, download.ErrInvalidRequest, "invalid NZBID", nil)
	}
	gs, err := c.listGroups(ctx, op)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(gs, func(g group) bool { return g.NZBID == id }) {
		return c.r.Err(op, download.ErrNotFound, "not in the NZBGet queue", nil)
	}
	return c.edit(ctx, op, command, id)
}

var categoryName = regexp.MustCompile(`^(?i)Category\d+\.Name$`)

// Categories implements download.Client (Category<N>.Name options).
func (c *Client) Categories(ctx context.Context) ([]string, error) {
	return c.categories(ctx, "categories")
}

func (c *Client) categories(ctx context.Context, op string) ([]string, error) {
	var opts []struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	}
	if err := c.rpc(ctx, op, "config", nil, &opts); err != nil {
		return nil, err
	}
	var cats []string
	for _, o := range opts {
		if categoryName.MatchString(o.Name) && o.Value != "" {
			cats = append(cats, o.Value)
		}
	}
	return cats, nil
}

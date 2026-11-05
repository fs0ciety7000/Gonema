package sabnzbd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/download/internal/httpc"
)

// DefaultPort is SABnzbd's default web port.
const DefaultPort = 8080

// HistoryLimit is the number of history entries read by Items.
const HistoryLimit = 200

// Client is a SABnzbd client. It is safe for concurrent use.
type Client struct {
	r        *httpc.Requester
	apiKey   string
	category string
}

var _ download.Client = (*Client)(nil)

// New returns a client for cfg; cfg.APIKey is required.
func New(cfg *download.Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("sabnzbd: %w: API key missing (SABnzbd Config > General > Security)", download.ErrInvalidRequest)
	}
	r, err := httpc.New(download.KindSABnzbd, cfg, DefaultPort)
	if err != nil {
		return nil, err
	}
	return &Client{r: r, apiKey: strings.TrimSpace(cfg.APIKey), category: cfg.Category}, nil
}

// Kind implements download.Client.
func (*Client) Kind() download.Kind { return download.KindSABnzbd }

// answer holds the fields every SABnzbd answer may carry.
type answer struct {
	Status *bool  `json:"status"`
	Error  string `json:"error"`
}

// call runs a GET api call and decodes its JSON answer into dst (may be nil).
func (c *Client) call(ctx context.Context, op string, q url.Values, dst any) error {
	q.Set("output", "json")
	q.Set("apikey", c.apiKey)
	resp, err := c.r.Do(ctx, &httpc.Request{Op: op, Path: "api", Query: q})
	if err != nil {
		return err
	}
	return c.decode(op, resp, dst)
}

func (c *Client) decode(op string, resp *httpc.Response, dst any) error {
	if e := apiError(strings.TrimSpace(string(resp.Body))); e != nil {
		return c.r.Err(op, e.kind, e.detail, nil)
	}
	if err := c.r.StatusError(op, resp); err != nil {
		return err
	}
	var a answer
	if err := json.Unmarshal(resp.Body, &a); err != nil {
		return c.r.Err(op, download.ErrUnexpected, "the answer is not SABnzbd JSON: check the URL base", nil)
	}
	if a.Error != "" {
		e := apiError("error: " + a.Error)
		return c.r.Err(op, e.kind, e.detail, nil)
	}
	if a.Status != nil && !*a.Status {
		return c.r.Err(op, download.ErrRejected, "SABnzbd answered status false", nil)
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Body, dst); err != nil {
		return c.r.Err(op, download.ErrUnexpected, "cannot decode the SABnzbd answer", err)
	}
	return nil
}

type sabErr struct {
	kind   error
	detail string
}

// apiError recognises SABnzbd error answers ("error: ..." as plain text or in
// the JSON "error" field).
func apiError(body string) *sabErr {
	msg, ok := strings.CutPrefix(body, "error:")
	if !ok {
		return nil
	}
	msg = strings.TrimSpace(msg)
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "api key"):
		return &sabErr{download.ErrAuth, "wrong API key: copy the API key (not the NZB key) from SABnzbd Config > General"}
	case strings.Contains(low, "denied"), strings.Contains(low, "not allowed"):
		return &sabErr{download.ErrAPIDisabled, "SABnzbd refused access from this address (Config > General > External internet access, or host whitelist): " + msg}
	case strings.Contains(low, "not implemented"):
		return &sabErr{download.ErrUnsupported, msg}
	case strings.Contains(low, "does not exist"):
		return &sabErr{download.ErrNotFound, msg}
	}
	return &sabErr{download.ErrRejected, msg}
}

// Test implements download.Client: version (no key needed), then a queue read
// (checks the key) and, when a category is configured, its existence.
func (c *Client) Test(ctx context.Context) error {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.call(ctx, "test", url.Values{"mode": {"version"}}, &v); err != nil {
		return err
	}
	if v.Version == "" {
		return c.r.Err("test", download.ErrUnexpected, "no version in the answer: is this SABnzbd?", nil)
	}
	if err := c.call(ctx, "test", url.Values{"mode": {"queue"}, "limit": {"1"}}, nil); err != nil {
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
		return c.r.Err("test", download.ErrRejected, fmt.Sprintf("category %q does not exist in SABnzbd: create it in Config > Categories", c.category), nil)
	}
	return nil
}

func priority(req *download.AddRequest) string {
	if req.Paused {
		return "-2"
	}
	switch req.Priority {
	case download.PriorityLow:
		return "-1"
	case download.PriorityHigh:
		return "1"
	case download.PriorityForce:
		return "2"
	}
	return "0"
}

// Add implements download.Client.
func (c *Client) Add(ctx context.Context, req download.AddRequest) (string, error) {
	if err := req.Validate(decision.Usenet); err != nil {
		return "", c.r.Err("add", download.ErrInvalidRequest, "", err)
	}
	params := url.Values{
		"cat":      {orStar(req.CategoryOr(c.category))},
		"priority": {priority(&req)},
	}
	if req.Name != "" {
		params.Set("nzbname", req.Name)
	}
	var out struct {
		NzoIDs []string `json:"nzo_ids"`
	}
	if req.URL != "" {
		params.Set("mode", "addurl")
		params.Set("name", req.URL)
		if err := c.call(ctx, "add", params, &out); err != nil {
			return "", err
		}
	} else {
		// The API key goes in the form body, not in the URL.
		params.Set("mode", "addfile")
		params.Set("output", "json")
		params.Set("apikey", c.apiKey)
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		fields := make([]httpc.Field, 0, len(keys))
		for _, k := range keys {
			fields = append(fields, httpc.Field{Name: k, Value: params.Get(k)})
		}
		body, ct, err := httpc.Multipart(fields, httpc.File{Field: "name", Filename: req.Filename, ContentType: "application/x-nzb", Data: req.Data})
		if err != nil {
			return "", c.r.Err("add", download.ErrInvalidRequest, "", err)
		}
		resp, err := c.r.Do(ctx, &httpc.Request{Op: "add", Method: http.MethodPost, Path: "api", Body: body, ContentType: ct})
		if err != nil {
			return "", err
		}
		if err := c.decode("add", resp, &out); err != nil {
			return "", err
		}
	}
	if len(out.NzoIDs) == 0 || out.NzoIDs[0] == "" {
		return "", c.r.Err("add", download.ErrRejected, "SABnzbd returned no job id (invalid NZB or unreachable link?)", nil)
	}
	return out.NzoIDs[0], nil
}

func orStar(cat string) string {
	if cat == "" {
		return "*" // SABnzbd's default category
	}
	return cat
}

type queueSlot struct {
	NzoID      string    `json:"nzo_id"`
	Filename   string    `json:"filename"`
	Cat        string    `json:"cat"`
	Status     string    `json:"status"`
	MB         httpc.Num `json:"mb"`
	MBLeft     httpc.Num `json:"mbleft"`
	Percentage httpc.Num `json:"percentage"`
	TimeLeft   string    `json:"timeleft"`
	Labels     []string  `json:"labels"`
}

type queueResp struct {
	Queue struct {
		KBPerSec httpc.Num   `json:"kbpersec"`
		Slots    []queueSlot `json:"slots"`
	} `json:"queue"`
}

type historySlot struct {
	NzoID       string    `json:"nzo_id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Status      string    `json:"status"`
	FailMessage string    `json:"fail_message"`
	Storage     string    `json:"storage"`
	Bytes       httpc.Num `json:"bytes"`
	Downloaded  httpc.Num `json:"downloaded"`
}

type historyResp struct {
	History struct {
		Slots []historySlot `json:"slots"`
	} `json:"history"`
}

const mib = 1 << 20

// Items implements download.Client.
func (c *Client) Items(ctx context.Context) ([]download.Item, error) {
	qq := url.Values{"mode": {"queue"}}
	hq := url.Values{"mode": {"history"}, "limit": {strconv.Itoa(HistoryLimit)}}
	if c.category != "" {
		qq.Set("cat", c.category)
		hq.Set("cat", c.category)
	}
	var q queueResp
	if err := c.call(ctx, "items", qq, &q); err != nil {
		return nil, err
	}
	var h historyResp
	if err := c.call(ctx, "items", hq, &h); err != nil {
		return nil, err
	}
	items := make([]download.Item, 0, len(q.Queue.Slots)+len(h.History.Slots))
	speedGiven := false
	for i := range q.Queue.Slots {
		it := queueItem(&q.Queue.Slots[i])
		if it.Status == download.StatusDownloading && !speedGiven {
			it.Speed = int64(float64(q.Queue.KBPerSec) * 1024)
			speedGiven = true
		}
		items = append(items, it)
	}
	for i := range h.History.Slots {
		items = append(items, historyItem(&h.History.Slots[i]))
	}
	return items, nil
}

func queueItem(s *queueSlot) download.Item {
	st, msg := mapQueueStatus(s.Status, s.Labels)
	size := int64(float64(s.MB) * mib)
	left := int64(float64(s.MBLeft) * mib)
	it := download.Item{
		Ref: s.NzoID, Name: s.Filename, Category: s.Cat,
		Status: st, RawState: s.Status, Message: msg,
		Size: size, Downloaded: max(0, size-left),
		Progress: download.Clamp01(float64(s.Percentage) / 100),
	}
	if size > 0 { // finer than the integer percentage
		it.Progress = download.Clamp01(float64(it.Downloaded) / float64(size))
	}
	if st == download.StatusDownloading {
		it.ETA = parseTimeLeft(s.TimeLeft)
	}
	return it
}

func historyItem(s *historySlot) download.Item {
	st, msg := mapHistoryStatus(s.Status, s.FailMessage)
	it := download.Item{
		Ref: s.NzoID, Name: s.Name, Category: s.Category,
		Status: st, RawState: s.Status, Message: msg,
		Size: s.Bytes.Int64(), Downloaded: s.Downloaded.Int64(),
		OutputPath: s.Storage,
	}
	if it.Downloaded == 0 {
		it.Downloaded = it.Size
	}
	if st != download.StatusFailed {
		it.Progress = 1
	}
	return it
}

// mapQueueStatus maps a queue slot status (and its labels).
func mapQueueStatus(raw string, labels []string) (download.Status, string) {
	for _, l := range labels {
		if strings.Contains(strings.ToUpper(l), "ENCRYPTED") {
			return download.StatusWarning, "encrypted archive: SABnzbd needs the password"
		}
	}
	msg := strings.Join(labels, ", ")
	switch raw {
	case "Downloading", "Fetching":
		return download.StatusDownloading, msg
	case "Queued", "Grabbing", "Checking":
		return download.StatusQueued, msg
	case "Propagating":
		if msg == "" {
			msg = "waiting for Usenet propagation"
		}
		return download.StatusQueued, msg
	case "Paused":
		return download.StatusPaused, msg
	}
	return download.StatusWarning, unknownState(raw)
}

// mapHistoryStatus maps a history slot status and its fail_message.
func mapHistoryStatus(raw, failMessage string) (download.Status, string) {
	switch raw {
	case "Completed":
		return download.StatusCompleted, failMessage
	case "Failed":
		if failMessage == "" {
			failMessage = "failed in SABnzbd"
		}
		return download.StatusFailed, failMessage
	case "Queued", "QuickCheck", "Verifying", "Repairing", "Fetching", "Extracting", "Moving", "Running":
		return download.StatusDownloading, "post-processing: " + raw
	case "Deleted":
		return download.StatusFailed, "deleted in SABnzbd"
	}
	return download.StatusWarning, unknownState(raw)
}

func unknownState(raw string) string {
	return fmt.Sprintf("unknown SABnzbd state %q", raw)
}

// parseTimeLeft parses "H:MM:SS" or "D:HH:MM:SS"; 0 when malformed.
func parseTimeLeft(s string) time.Duration {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 4 {
		return 0
	}
	units := []time.Duration{24 * time.Hour, time.Hour, time.Minute, time.Second}[4-len(parts):]
	var d time.Duration
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 1_000_000 {
			return 0
		}
		d += time.Duration(n) * units[i]
	}
	return d
}

func (c *Client) inQueue(ctx context.Context, op, ref string) (bool, error) {
	var q queueResp
	if err := c.call(ctx, op, url.Values{"mode": {"queue"}, "nzo_ids": {ref}}, &q); err != nil {
		return false, err
	}
	return slices.ContainsFunc(q.Queue.Slots, func(s queueSlot) bool { return s.NzoID == ref }), nil
}

func (c *Client) inHistory(ctx context.Context, op, ref string) (bool, error) {
	var h historyResp
	if err := c.call(ctx, op, url.Values{"mode": {"history"}, "nzo_ids": {ref}}, &h); err != nil {
		return false, err
	}
	return slices.ContainsFunc(h.History.Slots, func(s historySlot) bool { return s.NzoID == ref }), nil
}

// validRef rejects empty ids, lists and SABnzbd's special values ("all",
// "failed", "completed") that would act on many jobs.
func validRef(ref string) bool {
	if ref == "" || len(ref) > 256 || strings.ContainsAny(ref, ", ") {
		return false
	}
	switch strings.ToLower(ref) {
	case "all", "failed", "completed":
		return false
	}
	return true
}

// Remove implements download.Client.
func (c *Client) Remove(ctx context.Context, ref string, deleteData bool) error {
	if !validRef(ref) {
		return c.r.Err("remove", download.ErrInvalidRequest, "invalid job id", nil)
	}
	del := "0"
	if deleteData {
		del = "1"
	}
	inQ, err := c.inQueue(ctx, "remove", ref)
	if err != nil {
		return err
	}
	if inQ {
		return c.call(ctx, "remove", url.Values{"mode": {"queue"}, "name": {"delete"}, "value": {ref}, "del_files": {del}}, nil)
	}
	inH, err := c.inHistory(ctx, "remove", ref)
	if err != nil {
		return err
	}
	if !inH {
		return c.r.Err("remove", download.ErrNotFound, "", nil)
	}
	return c.call(ctx, "remove", url.Values{"mode": {"history"}, "name": {"delete"}, "value": {ref}, "archive": {"0"}, "del_files": {del}}, nil)
}

// Pause implements download.Client.
func (c *Client) Pause(ctx context.Context, ref string) error { return c.queueAction(ctx, "pause", ref) }

// Resume implements download.Client.
func (c *Client) Resume(ctx context.Context, ref string) error {
	return c.queueAction(ctx, "resume", ref)
}

// queueAction pauses or resumes one job; SABnzbd lists the affected nzo_ids.
func (c *Client) queueAction(ctx context.Context, op, ref string) error {
	if !validRef(ref) {
		return c.r.Err(op, download.ErrInvalidRequest, "invalid job id", nil)
	}
	var out struct {
		NzoIDs []string `json:"nzo_ids"`
	}
	if err := c.call(ctx, op, url.Values{"mode": {"queue"}, "name": {op}, "value": {ref}}, &out); err != nil {
		return err
	}
	if !slices.Contains(out.NzoIDs, ref) {
		return c.r.Err(op, download.ErrNotFound, "not in the SABnzbd queue", nil)
	}
	return nil
}

// Categories implements download.Client. SABnzbd's default category "*" is
// left out.
func (c *Client) Categories(ctx context.Context) ([]string, error) {
	return c.categories(ctx, "categories")
}

func (c *Client) categories(ctx context.Context, op string) ([]string, error) {
	var out struct {
		Categories []string `json:"categories"`
	}
	if err := c.call(ctx, op, url.Values{"mode": {"get_cats"}}, &out); err != nil {
		return nil, err
	}
	cats := make([]string, 0, len(out.Categories))
	for _, s := range out.Categories {
		if s != "*" && s != "" {
			cats = append(cats, s)
		}
	}
	return cats, nil
}

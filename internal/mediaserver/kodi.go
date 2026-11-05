package mediaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// kodi talks to Kodi's JSON-RPC API over HTTP (see doc.go for the reference).
type kodi struct {
	c        *outbound.Client
	endpoint string // base + "/jsonrpc"
	user     string
	pass     string
	clean    bool
	id       atomic.Int64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int64  `json:"id"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call sends one JSON-RPC request and returns its result.
func (k *kodi) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: k.id.Add(1)})
	if err != nil {
		return nil, k.c.Fail(outbound.ErrResponse, 0, "cannot encode the request")
	}
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}}
	if k.user != "" || k.pass != "" {
		req := http.Request{Header: http.Header{}}
		req.SetBasicAuth(k.user, k.pass)
		h.Set("Authorization", req.Header.Get("Authorization"))
	}
	resp, err := k.c.Do(ctx, http.MethodPost, k.endpoint, h, body)
	if err != nil {
		return nil, err
	}
	if err := k.c.Check(resp, ""); err != nil {
		return nil, err
	}
	var out rpcResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, k.c.Fail(outbound.ErrResponse, resp.Status, "the answer is not JSON-RPC (is the web server enabled in Kodi's settings?)")
	}
	if out.Error != nil {
		return nil, k.c.Fail(outbound.ErrStatus, 0, fmt.Sprintf("%s failed: %s (code %d)", method, out.Error.Message, out.Error.Code))
	}
	return out.Result, nil
}

func (k *kodi) Test(ctx context.Context) error {
	res, err := k.call(ctx, "JSONRPC.Ping", nil)
	if err != nil {
		return err
	}
	var pong string
	if json.Unmarshal(res, &pong) != nil || pong != "pong" {
		return k.c.Fail(outbound.ErrResponse, 0, "JSONRPC.Ping did not answer pong")
	}
	return nil
}

// Refresh scans the movie folder (VideoLibrary.Scan with a directory, which
// Kodi wants with a trailing separator). After a deletion, when enabled, the
// library is cleaned for that folder instead (VideoLibrary.Clean), which
// removes the entries whose files are gone.
func (k *kodi) Refresh(ctx context.Context, req RefreshRequest) error {
	dir := withTrailingSep(req.Path)
	if req.Reason == ReasonDelete {
		if !k.clean {
			return nil // a scan does not remove entries: nothing useful to do
		}
		_, err := k.call(ctx, "VideoLibrary.Clean", map[string]any{"showdialogs": false, "content": "movies", "directory": dir})
		return err
	}
	_, err := k.call(ctx, "VideoLibrary.Scan", map[string]any{"directory": dir, "showdialogs": false})
	return err
}

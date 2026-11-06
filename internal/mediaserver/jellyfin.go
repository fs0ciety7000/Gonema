package mediaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// jellyfin talks to Jellyfin (see doc.go for the reference).
type jellyfin struct {
	c       *outbound.Client
	base    string
	token   string
	version string
}

// header carries the API key in the Authorization header ("MediaBrowser"
// scheme). The legacy X-Emby-Token header is not sent: recent servers only
// read it when legacy authorization is enabled. Values are URL-decoded by the
// server, so they are URL-encoded here.
func (j *jellyfin) header() http.Header {
	v := j.version
	if v == "" {
		v = "dev"
	}
	auth := `MediaBrowser Client="Gonema", Device="Gonema", DeviceId="gonema", Version="` + url.QueryEscape(v) +
		`", Token="` + url.QueryEscape(j.token) + `"`
	return http.Header{"Authorization": {auth}, "Accept": {"application/json"}}
}

func (j *jellyfin) Test(ctx context.Context) error {
	if j.token == "" {
		return j.c.Fail(outbound.ErrAuth, 0, "a Jellyfin API key is required")
	}
	resp, err := j.c.Do(ctx, http.MethodGet, j.base+"/System/Info", j.header(), nil)
	if err != nil {
		return err
	}
	if err := j.c.Check(resp, ""); err != nil {
		return err
	}
	var info struct {
		ID string `json:"Id"`
	}
	if json.Unmarshal(resp.Body, &info) != nil || info.ID == "" {
		return j.c.Fail(outbound.ErrResponse, resp.Status, "the server information is missing (is this a Jellyfin URL?)")
	}
	return nil
}

// updateType maps a reason to MediaUpdateInfoPathDto.UpdateType.
func updateType(r Reason) string {
	switch r {
	case ReasonAdd:
		return "Created"
	case ReasonDelete:
		return "Deleted"
	}
	return "Modified"
}

// Refresh reports the folder change (POST /Library/Media/Updated): Jellyfin
// scans that folder only. A server without the endpoint gets a full library
// scan (POST /Library/Refresh), which needs an administrator key.
func (j *jellyfin) Refresh(ctx context.Context, req RefreshRequest) error {
	body, err := json.Marshal(map[string]any{
		"Updates": []map[string]string{{"Path": req.Path, "UpdateType": updateType(req.Reason)}},
	})
	if err != nil {
		return j.c.Fail(outbound.ErrResponse, 0, "cannot encode the request")
	}
	h := j.header()
	h.Set("Content-Type", "application/json")
	resp, err := j.c.Do(ctx, http.MethodPost, j.base+"/Library/Media/Updated", h, body)
	if err != nil {
		return err
	}
	if resp.Status == http.StatusNotFound {
		if resp, err = j.c.Do(ctx, http.MethodPost, j.base+"/Library/Refresh", j.header(), nil); err != nil {
			return err
		}
	}
	return j.c.Check(resp, strings.TrimSpace(resp.Header.Get("Message")))
}

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/mediaserver"
	"github.com/fs0ciety7000/gonema/internal/notify"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
)

func newIntegrationEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t, "")
	key, _ := secretbox.NewKey()
	box, _ := secretbox.New(key)
	n, err := notify.NewService(notify.ServiceOptions{Store: e.srv.d.Store, Box: box, Bus: e.bus})
	if err != nil {
		t.Fatal(err)
	}
	m, err := mediaserver.NewService(mediaserver.ServiceOptions{Store: e.srv.d.Store, Box: box, Bus: e.bus})
	if err != nil {
		t.Fatal(err)
	}
	e.srv.RegisterIntegrationRoutes(n, m)
	return e
}

func TestNotificationRoutes(t *testing.T) {
	e := newIntegrationEnv(t)
	const p = "/api/v1"
	var hits atomic.Int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Gonema-Signature") == "" || r.Header.Get("X-Api-Key") != "header-secret-value" || !strings.Contains(string(b), `"event":"test"`) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()

	body := `{"name":"Hook","kind":"webhook","settings":{"url":"` + hook.URL + `/in"},
		"secrets":{"signingSecret":"hmac-secret-value","headers":{"X-Api-Key":"header-secret-value"}},
		"events":["grab","failure"],"tags":["Kids"]}`
	r := e.do(t, "POST", p+"/notifications", body)
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	if strings.Contains(r.body, "hmac-secret-value") || strings.Contains(r.body, "header-secret-value") {
		t.Fatalf("a secret is returned: %s", r.body)
	}
	n := decode[notify.Notification](t, r)
	if !n.SecretsSet.SigningSecret || len(n.SecretsSet.HeaderNames) != 1 || len(n.EventMatrix) != 7 || !n.EventMatrix[0].Enabled ||
		n.EventMatrix[1].Enabled || n.Tags[0] != "kids" {
		t.Errorf("created = %+v", n)
	}
	id := strconv.FormatInt(n.ID, 10)

	r = e.do(t, "POST", p+"/notifications/"+id+"/test", "")
	if r.status != 200 || !strings.Contains(r.body, `"ok":true`) || hits.Load() != 1 {
		t.Fatalf("test saved: %d %s (hits %d)", r.status, r.body, hits.Load())
	}
	// Unsaved settings with ?id=: the stored secrets fill the omitted ones.
	r = e.do(t, "POST", p+"/notifications/test?id="+id, `{"name":"Hook","kind":"webhook","settings":{"url":"`+hook.URL+`/other"}}`)
	if r.status != 200 || !strings.Contains(r.body, `"ok":true`) || hits.Load() != 2 {
		t.Fatalf("test config: %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p+"/notifications/test", `{"name":"Hook","kind":"webhook","settings":{"url":"`+hook.URL+`/x"}}`)
	if r.status != 200 || !strings.Contains(r.body, `"ok":false`) || !strings.Contains(r.body, "HTTP 400") {
		t.Errorf("test without secrets: %d %s", r.status, r.body)
	}

	r = e.do(t, "PUT", p+"/notifications/"+id, `{"name":"Hook 2","kind":"webhook","settings":{"url":"`+hook.URL+`/in"}}`)
	if r.status != 200 || !strings.Contains(r.body, `"signingSecret":true`) || !strings.Contains(r.body, `"name":"Hook 2"`) {
		t.Errorf("update: %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", p+"/notifications", ""); r.status != 200 || !strings.Contains(r.body, `"Hook 2"`) {
		t.Errorf("list: %d %s", r.status, r.body)
	}

	detail := problemOK(t, e.do(t, "POST", p+"/notifications", `{"name":"Bad","kind":"webhook","settings":{"url":"file:///etc/passwd"}}`), 422)
	if !strings.Contains(detail, "http://") {
		t.Errorf("invalid url detail = %q", detail)
	}
	problemOK(t, e.do(t, "POST", p+"/notifications", `{"name":"Hook 2","kind":"slack","secrets":{"webhookUrl":"https://hooks.slack.com/services/a"}}`), 409)
	problemOK(t, e.do(t, "GET", p+"/notifications/999", ""), 404)
	problemOK(t, e.do(t, "POST", p+"/notifications/999/test", ""), 404)

	r = e.do(t, "PUT", p+"/notifications/settings", `{"appUrl":"https://gonema.example.lan/"}`)
	if r.status != 200 || !strings.Contains(r.body, `"appUrl":"https://gonema.example.lan"`) {
		t.Errorf("put settings: %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", p+"/notifications/settings", ""); !strings.Contains(r.body, "gonema.example.lan") {
		t.Errorf("get settings: %s", r.body)
	}
	problemOK(t, e.do(t, "PUT", p+"/notifications/settings", `{"appUrl":"javascript:alert(1)"}`), 422)

	if r := e.do(t, "DELETE", p+"/notifications/"+id, ""); r.status != 204 {
		t.Errorf("delete: %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "DELETE", p+"/notifications/"+id, ""), 404)
}

func TestMediaServerRoutes(t *testing.T) {
	e := newIntegrationEnv(t)
	const p = "/api/v1"
	const token = "0123456789abcdef0123456789abcdef"
	jf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), `Token="`+token+`"`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/System/Info" {
			_, _ = w.Write([]byte(`{"Id":"abc","ServerName":"x"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer jf.Close()

	r := e.do(t, "POST", p+"/media-servers/test", `{"name":"JF","kind":"jellyfin","baseUrl":"`+jf.URL+`","token":"`+token+`"}`)
	if r.status != 200 || !strings.Contains(r.body, `"ok":true`) {
		t.Fatalf("test unsaved: %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p+"/media-servers", `{"name":"JF","kind":"jellyfin","baseUrl":"`+jf.URL+`","token":"`+token+`",
		"pathMappings":[{"from":"/movies","to":"/media/movies"}],"refreshOn":["add","upgrade"]}`)
	if r.status != 201 || strings.Contains(r.body, token) || !strings.Contains(r.body, `"tokenSet":true`) {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	ms := decode[mediaserver.MediaServer](t, r)
	if len(ms.RefreshMatrix) != 4 || !ms.RefreshMatrix[1].Enabled || ms.RefreshMatrix[3].Enabled || len(ms.PathMappings) != 1 {
		t.Errorf("created = %+v", ms)
	}
	id := strconv.FormatInt(ms.ID, 10)
	if r := e.do(t, "POST", p+"/media-servers/"+id+"/test", ""); r.status != 200 || !strings.Contains(r.body, `"ok":true`) {
		t.Errorf("test saved: %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p+"/media-servers/test?id="+id, `{"name":"JF","kind":"jellyfin","baseUrl":"`+jf.URL+`"}`)
	if r.status != 200 || !strings.Contains(r.body, `"ok":true`) {
		t.Errorf("test with stored token: %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p+"/media-servers/test", `{"name":"JF","kind":"jellyfin","baseUrl":"`+jf.URL+`","token":"wrong-token-0000"}`)
	if r.status != 200 || !strings.Contains(r.body, `"ok":false`) || strings.Contains(r.body, "wrong-token-0000") {
		t.Errorf("test wrong token: %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", p+"/media-servers/"+id, ""); r.status != 200 || strings.Contains(r.body, token) {
		t.Errorf("get: %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "POST", p+"/media-servers", `{"name":"X","kind":"plex","baseUrl":"http://u:p@plex:32400"}`), 422)
	problemOK(t, e.do(t, "POST", p+"/media-servers", `{"name":"JF","kind":"plex","baseUrl":"http://plex:32400"}`), 409)
	problemOK(t, e.do(t, "PUT", p+"/media-servers/999", `{"name":"X","kind":"plex","baseUrl":"http://plex:32400"}`), 404)
	if r := e.do(t, "GET", p+"/media-servers", ""); r.status != 200 || !strings.Contains(r.body, `"JF"`) {
		t.Errorf("list: %d %s", r.status, r.body)
	}
	if r := e.do(t, "DELETE", p+"/media-servers/"+id, ""); r.status != 204 {
		t.Errorf("delete: %d", r.status)
	}
}

package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/health"
)

func TestHealthRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	sched := health.New(health.Options{Bus: e.bus})
	sched.Register(health.Check{ID: "demo", Run: func(context.Context) ([]health.Issue, error) {
		return []health.Issue{{ID: "demo.down:1", Severity: health.Warning, Title: "Demo is not answering",
			Message: "Demo hasn't answered for 40 minutes.", Fix: []string{"Use Test."}, Link: "/settings/demo/1"}}, nil
	}})
	e.srv.RegisterHealthRoutes(sched, health.SettingsStore{St: e.st})
	raised := e.bus.Subscribe(4, func(ev *events.Event) bool { return ev.Type == events.HealthIssueRaised })
	defer raised.Close()

	if r := e.do(t, "GET", "/api/v1/health/issues", ""); r.status != 200 || strings.TrimSpace(r.body) != "[]" {
		t.Fatalf("issues before any run = %d %s", r.status, r.body)
	}
	r := e.do(t, "POST", "/api/v1/health/run", "")
	got := decode[[]health.Issue](t, r)
	if r.status != 200 || len(got) != 1 || got[0].ID != "demo.down:1" || got[0].Source != "demo" || got[0].Since.IsZero() {
		t.Fatalf("run = %d %s", r.status, r.body)
	}
	for _, field := range []string{`"severity":"warning"`, `"fix":["Use Test."]`, `"link":"/settings/demo/1"`, `"source":"demo"`} {
		if !strings.Contains(r.body, field) {
			t.Errorf("response lacks %s: %s", field, r.body)
		}
	}
	select {
	case ev := <-raised.C():
		if ev.Data.(events.HealthIssueEvent).Issue.ID != "demo.down:1" {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Error("no health.issue_raised event")
	}
	if got := decode[[]health.Issue](t, e.do(t, "GET", "/api/v1/health/issues", "")); len(got) != 1 {
		t.Errorf("issues = %+v", got)
	}
	if got := decode[[]health.CheckStatus](t, e.do(t, "GET", "/api/v1/health/checks", "")); len(got) != 1 || got[0].Issues != 1 {
		t.Errorf("checks = %+v", got)
	}

	if got := decode[health.Settings](t, e.do(t, "GET", "/api/v1/settings/health", "")); got != health.DefaultSettings() {
		t.Errorf("settings = %+v", got)
	}
	r = e.do(t, "PUT", "/api/v1/settings/health", `{"lowSpacePercent":10,"lowSpaceGb":0,"recycleBinMaxGb":20}`)
	if got := decode[health.Settings](t, r); r.status != 200 || got.LowSpacePercent != 10 || got.RecycleBinMaxGB != 20 {
		t.Errorf("put settings = %d %s", r.status, r.body)
	}
	detail := problemOK(t, e.do(t, "PUT", "/api/v1/settings/health", `{"lowSpacePercent":90,"lowSpaceGb":0,"recycleBinMaxGb":20}`), 422)
	if detail != "The low space share must be between 0 (off) and 50 percent." {
		t.Errorf("detail = %q", detail)
	}
}

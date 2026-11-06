package api

import (
	"fmt"
	"strings"
	"testing"
)

// TestQualityGroupEdits: create, rename, merge and split groups through the
// profile API ("equal, best score wins": members share one rank), with the
// validation of group names.
func TestQualityGroupEdits(t *testing.T) {
	e := newTestEnv(t, "")
	put := func(id int64, entries, cutoff string) response {
		return e.do(t, "PUT", fmt.Sprintf("%s/quality-profiles/%d", p1, id),
			fmt.Sprintf(`{"name":"Groups","entries":[%s],"cutoff":%q,"upgradeAllowed":true,"minScore":0,"cutoffScore":0,"minUpgradeStep":1}`, entries, cutoff))
	}
	// Create with a group.
	r := e.do(t, "POST", p1+"/quality-profiles", `{"name":"Groups","entries":[`+
		`{"group":"WEB 2160p","qualities":["webdl-2160p","webrip-2160p"],"allowed":true},`+
		`{"qualities":["bluray-1080p"],"allowed":true},{"qualities":["webdl-1080p"],"allowed":true}],`+
		`"cutoff":"WEB 2160p","upgradeAllowed":true,"minScore":0,"cutoffScore":0,"minUpgradeStep":1}`)
	p := decode[QualityProfileSaved](t, r).Profile
	if r.status != 201 || len(p.Entries) != 3 || p.Entries[0].Group != "WEB 2160p" || p.CutoffLabel != "WEB 2160p" ||
		p.Items[0].Position != p.Items[1].Position {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	// Rename: the cutoff follows when it names the new group, or a member.
	r = put(p.ID, `{"group":"UHD web","qualities":["webdl-2160p","webrip-2160p"],"allowed":true},{"qualities":["bluray-1080p"],"allowed":true}`, "webdl-2160p")
	if got := decode[QualityProfileSaved](t, r).Profile; r.status != 200 || got.Entries[0].Group != "UHD web" || got.CutoffLabel != "UHD web" {
		t.Fatalf("rename = %d %s", r.status, r.body)
	}
	detail := problemOK(t, put(p.ID, `{"group":"UHD web","qualities":["webdl-2160p"],"allowed":true}`, "WEB 2160p"), 422)
	if !strings.Contains(detail, `cutoff "WEB 2160p" is not a quality or group of the profile`) {
		t.Errorf("stale cutoff = %q", detail)
	}
	// Merge two entries into one group.
	r = put(p.ID, `{"group":"Any 1080p","qualities":["bluray-1080p","webdl-1080p","webrip-1080p"],"allowed":true}`, "Any 1080p")
	if got := decode[QualityProfileSaved](t, r).Profile; r.status != 200 || len(got.Entries) != 1 || len(got.Entries[0].Qualities) != 3 {
		t.Fatalf("merge = %d %s", r.status, r.body)
	}
	// Split it again, best first.
	r = put(p.ID, `{"qualities":["bluray-1080p"],"allowed":true},{"qualities":["webdl-1080p"],"allowed":true},{"qualities":["webrip-1080p"],"allowed":false}`, "bluray-1080p")
	got := decode[QualityProfileSaved](t, r).Profile
	if r.status != 200 || len(got.Entries) != 3 || got.Entries[0].Group != "" || got.Items[0].Position == got.Items[1].Position {
		t.Fatalf("split = %d %s", r.status, r.body)
	}
	// Validation of the groups.
	for _, tc := range []struct{ entries, want string }{
		{`{"group":"HD","qualities":["bluray-1080p"],"allowed":true},{"group":"hd","qualities":["webdl-1080p"],"allowed":true}`, "used twice"},
		{`{"group":"Bluray-1080p","qualities":["bluray-1080p","webdl-1080p"],"allowed":true}`, "also the name of a quality"},
		{`{"qualities":["bluray-1080p","webdl-1080p"],"allowed":true}`, "has no group name"},
		{`{"group":"A","qualities":["bluray-1080p"],"allowed":true},{"group":"B","qualities":["bluray-1080p"],"allowed":true}`, "appears twice"},
	} {
		if d := problemOK(t, put(p.ID, tc.entries, "bluray-1080p"), 422); !strings.Contains(d, tc.want) {
			t.Errorf("%s: detail = %q, want %q", tc.entries, d, tc.want)
		}
	}
}

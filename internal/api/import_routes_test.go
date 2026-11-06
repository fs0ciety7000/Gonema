package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/importer"
)

func newImportEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t, "")
	svc, err := importer.New(importer.Options{Store: e.srv.d.Store, Matcher: e.lib, Library: e.lib, Bus: e.bus})
	if err != nil {
		t.Fatal(err)
	}
	e.srv.RegisterImportRoutes(svc)
	return e
}

func TestImportRoutes(t *testing.T) {
	e := newImportEnv(t)
	const p = "/api/v1"

	r := e.do(t, "GET", p+"/settings/naming", "")
	if r.status != 200 {
		t.Fatalf("get naming: %d %s", r.status, r.body)
	}
	view := decode[NamingSettingsView](t, r)
	if view.FileTemplate == "" || len(view.Tokens) != 16 || view.Defaults.FolderTemplate == "" || !view.RenameFiles {
		t.Errorf("naming view = %+v", view)
	}

	detail := problemOK(t, e.do(t, "POST", p+"/settings/naming/preview", `{"folderTemplate":"{Title}","fileTemplate":"{Movie Title} {Bogus}","renameFiles":true}`), 422)
	if !strings.Contains(detail, "{Bogus}") {
		t.Errorf("preview detail = %q", detail)
	}
	r = e.do(t, "POST", p+"/settings/naming/preview", `{"folderTemplate":"{Title} ({Year})","fileTemplate":"{Movie Title} ({Release Year}) {[Quality Full]}","renameFiles":true}`)
	if r.status != 200 {
		t.Fatalf("preview: %d %s", r.status, r.body)
	}
	pv := decode[importer.NamingPreview](t, r)
	if !pv.Example || pv.File != "Dune - Part Two (2024) [Bluray-2160p].mkv" {
		t.Errorf("preview = %+v", pv)
	}
	r = e.do(t, "PUT", p+"/settings/naming", `{"folderTemplate":"{Title} ({Year})","fileTemplate":"{Movie Title} {[Quality Title]}","renameFiles":true}`)
	if r.status != 200 || !strings.Contains(r.body, "{Movie Title} {[Quality Title]}") {
		t.Errorf("put naming: %d %s", r.status, r.body)
	}

	dl := t.TempDir()
	body, _ := json.Marshal(map[string]any{
		"mode": "copy", "torrentMode": "hardlink", "allowedRoots": []string{dl}, "recycleBin": "",
		"recycleRetentionDays": 30, "importSubtitles": true,
	})
	r = e.do(t, "PUT", p+"/import/settings", string(body))
	if r.status != 200 || !strings.Contains(r.body, `"mode":"copy"`) {
		t.Fatalf("put settings: %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "PUT", p+"/import/settings", `{"mode":"move","torrentMode":"move","allowedRoots":[],"recycleBin":"","recycleRetentionDays":30,"importSubtitles":true}`), 422)

	outside, _ := json.Marshal(map[string]string{"path": t.TempDir()})
	if d := problemOK(t, e.do(t, "POST", p+"/import/scan", string(outside)), 422); !strings.Contains(d, "outside the folders") {
		t.Errorf("scan outside = %q", d)
	}
	inside, _ := json.Marshal(map[string]string{"path": dl})
	if r = e.do(t, "POST", p+"/import/scan", string(inside)); r.status != 200 || strings.TrimSpace(r.body) != "[]" {
		t.Errorf("scan empty: %d %s", r.status, r.body)
	}

	if r = e.do(t, "GET", p+"/recycle-bin", ""); r.status != 200 || strings.TrimSpace(r.body) != "[]" {
		t.Errorf("list bin: %d %s", r.status, r.body)
	}
	if d := problemOK(t, e.do(t, "POST", p+"/recycle-bin/purge", ""), 422); !strings.Contains(d, "confirm") {
		t.Errorf("purge without confirm = %q", d)
	}
	if r = e.do(t, "POST", p+"/recycle-bin/purge?confirm=true", ""); r.status != 200 || !strings.Contains(r.body, `"purged":[]`) {
		t.Errorf("purge: %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "POST", p+"/recycle-bin/restore", `{"name":"nothing.mkv"}`), 404)
	problemOK(t, e.do(t, "POST", p+"/import/manual", `{"selections":[]}`), 422)

	// The library scan of the seeded root folder (missing on disk) fails cleanly.
	r = e.do(t, "POST", p+"/library-import/scan", `{"rootFolderId":`+itoa(e.rootID)+`}`)
	if r.status < 400 || r.status == 500 && !strings.Contains(r.body, "root folder") {
		t.Errorf("library scan: %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "POST", p+"/library-import/scan", `{"rootFolderId":999}`), 404)

	doc, _ := json.Marshal(e.srv.OpenAPI())
	for _, path := range []string{"/import/settings", "/import/scan", "/import/manual", "/library-import/scan", "/library-import/confirm",
		"/recycle-bin", "/recycle-bin/restore", "/recycle-bin/purge", "/settings/naming", "/settings/naming/preview"} {
		if !strings.Contains(string(doc), `"`+path+`"`) {
			t.Errorf("OpenAPI lacks %s", path)
		}
	}
}

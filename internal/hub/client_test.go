package hub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchPackageSendsBearerAndStoresETag(t *testing.T) {
	var gotAuth, gotNone string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotNone = r.Header.Get("If-None-Match")
		if r.URL.Path != "/api/agent/v2/package" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"version":"abc","etag":"abc","entries":[{"position":1,"starts_at":"2026-10-20T10:00:00Z","duration_seconds":10,"screen_ids":[42],"media":{"id":19,"url":"/clip.mp3","mime_type":"audio/mpeg"}}]}`)
	}))
	defer srv.Close()

	pkg, notMod, err := New(srv.URL, "tok").FetchPackage(context.Background(), `"prev"`)
	if err != nil || notMod {
		t.Fatal(err, notMod)
	}
	if gotAuth != "Bearer tok" || gotNone != `"prev"` {
		t.Fatalf("auth %q none %q", gotAuth, gotNone)
	}
	if pkg.HeaderETag != `"abc"` || len(pkg.Entries) != 1 || pkg.Entries[0].Media.ID != 19 {
		t.Fatalf("%+v", pkg)
	}
}

func TestFetchPackageNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	_, notMod, err := New(srv.URL, "tok").FetchPackage(context.Background(), `"abc"`)
	if err != nil || !notMod {
		t.Fatal(err, notMod)
	}
}

func TestPostPlayEventBody(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/v1/play_events" || r.Method != http.MethodPost {
			t.Errorf("path %s method %s", r.URL.Path, r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"play_log_ids":[101]}`)
	}))
	defer srv.Close()
	started := time.Date(2026, 9, 2, 2, 0, 1, 0, time.UTC)
	status, resp, err := New(srv.URL, "tok").PostPlayEvent(context.Background(), 42, 19, started)
	if err != nil || status != 201 {
		t.Fatal(err, status, resp)
	}
	if !strings.Contains(body, `"screen_id":42`) || !strings.Contains(body, `"media_asset_id":19`) || !strings.Contains(body, `"started_at":"2026-09-02T02:00:01Z"`) {
		t.Fatalf("body %s", body)
	}
}

func TestUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").FetchConfig(context.Background())
	if err != ErrUnauthorized {
		t.Fatal(err)
	}
}

func TestRedirectStripsAuthorization(t *testing.T) {
	var hops []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops = append(hops, r.URL.Path+":"+r.Header.Get("Authorization"))
		if r.URL.Path == "/api/agent/v1/config" {
			http.Redirect(w, r, srv.URL+"/landed", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"station_id":3,"screens":[{"id":42}]}`)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "tok").FetchConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(hops) != 2 {
		t.Fatalf("hops %v", hops)
	}
	if hops[0] != "/api/agent/v1/config:Bearer tok" {
		t.Fatalf("first hop %q", hops[0])
	}
	if hops[1] != "/landed:" {
		t.Fatalf("redirect kept authorization: %q", hops[1])
	}
}

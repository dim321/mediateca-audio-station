package cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestResolveJoinsRelativeURL(t *testing.T) {
	got, err := Resolve("https://hub.example", "/rails/active_storage/blobs/redirect/x")
	if err != nil || got != "https://hub.example/rails/active_storage/blobs/redirect/x" {
		t.Fatal(got, err)
	}
}

func TestDownloadFollowsRedirectWithoutBearer(t *testing.T) {
	var sawAuth bool
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth = true
		}
		_, _ = w.Write([]byte("audio-bytes"))
	}))
	defer fileSrv.Close()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth = true
		}
		http.Redirect(w, r, fileSrv.URL+"/file", http.StatusFound)
	}))
	defer hub.Close()

	dir := t.TempDir()
	c := New(dir)
	err := c.Reconcile(context.Background(), hub.URL, []Item{{ID: 19, URL: "/clip.mp3", Mime: "audio/mpeg"}})
	if err != nil {
		t.Fatal(err)
	}
	if sawAuth {
		t.Fatal("bearer sent on media download")
	}
	if !c.Ready(19, "/clip.mp3") {
		t.Fatal("not ready")
	}
	body, err := os.ReadFile(c.Path(19))
	if err != nil || string(body) != "audio-bytes" {
		t.Fatalf("%q %v", body, err)
	}
}

func TestPartialIsNotReadyAndPruneKeepsUnknownOnSkippedReconcile(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Path(7)+".partial", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c.Ready(7, "/old") {
		t.Fatal("partial must not be ready")
	}
}

func TestReconcileDeletesUnreferenced(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Path(7), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Path(7)+".url", []byte("/old"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("new"))
	}))
	defer srv.Close()
	if err := c.Reconcile(context.Background(), srv.URL, []Item{{ID: 19, URL: "/new.mp3", Mime: "audio/mpeg"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Path(7)); !os.IsNotExist(err) {
		t.Fatal("old id kept", err)
	}
	if !c.Ready(19, "/new.mp3") {
		t.Fatal("new id missing")
	}
}

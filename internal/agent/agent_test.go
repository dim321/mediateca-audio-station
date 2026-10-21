package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"mediateca-audio-station/internal/config"
	"mediateca-audio-station/internal/hub"
)

func testCfg(dir string) config.File {
	return config.File{
		HubBaseURL:    "https://hub.example",
		AgentToken:    "super-secret-token",
		DataDir:       dir,
		ScreenID:      42,
		LateThreshold: 2 * time.Second,
		PollInterval:  time.Minute,
		MPVPath:       "mpv",
	}
}

func seedReady(t *testing.T, dir string, id int, rawURL string) {
	t.Helper()
	media := filepath.Join(dir, "media")
	if err := os.MkdirAll(media, 0o700); err != nil {
		t.Fatal(err)
	}
	name := strconv.Itoa(id)
	if err := os.WriteFile(filepath.Join(media, name), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(media, name+".url"), []byte(rawURL), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(media, name+".mime"), []byte("audio/mpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pkgAt(start time.Time, mediaID int, screens []int) hub.Package {
	raw := start.UTC().Format(time.RFC3339)
	return hub.Package{
		Entries: []hub.Entry{{
			Position:        1,
			StartsAt:        &raw,
			DurationSeconds: 30,
			ScreenIDs:       screens,
			Media:           hub.Media{ID: mediaID, URL: "/clip.mp3", MimeType: "audio/mpeg"},
		}},
	}
}

func ptrPkg(pkg hub.Package) *hub.Package { return &pkg }

type fakePlayer struct {
	starts int
	paths  []string
	err    error
}

func (p *fakePlayer) Start(_ context.Context, path string) error {
	p.starts++
	p.paths = append(p.paths, path)
	return p.err
}

func (p *fakePlayer) Stop(context.Context) error { return nil }

type fakeHub struct {
	cfgErr error
}

func (h *fakeHub) FetchConfig(context.Context) (hub.Config, error) {
	if h.cfgErr != nil {
		return hub.Config{}, h.cfgErr
	}
	return hub.Config{}, nil
}

func (h *fakeHub) FetchPackage(context.Context, string) (hub.Package, bool, error) {
	return hub.Package{}, false, nil
}

func (h *fakeHub) PostPlayEvent(context.Context, int, int, time.Time) (int, string, error) {
	return httpCreated, "", nil
}

const httpCreated = 201

func newTestAgent(t *testing.T, cfg config.File, h hubAPI, p audioPlayer) (*Agent, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	a, err := newAgent(cfg, h, p, log)
	if err != nil {
		t.Fatal(err)
	}
	return a, &buf
}

func TestTickStartsOnceAndHidesToken(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
	seedReady(t, dir, 19, "/clip.mp3")
	p := &fakePlayer{}
	a, buf := newTestAgent(t, testCfg(dir), &fakeHub{}, p)
	a.now = func() time.Time { return start }
	a.pkg = ptrPkg(pkgAt(start, 19, []int{42}))
	a.tick(context.Background())
	if p.starts != 1 || p.paths[0] != filepath.Join(dir, "media", "19") {
		t.Fatalf("starts %d paths %v", p.starts, p.paths)
	}
	if !a.queue.Played("42:19:2026-09-02T02:00:00Z") {
		t.Fatal("played key missing")
	}
	a.tick(context.Background())
	if p.starts != 1 {
		t.Fatalf("repeated start %d", p.starts)
	}
	if bytes.Contains(buf.Bytes(), []byte("super-secret-token")) {
		t.Fatal("token logged")
	}
}

func TestFailedStartRetriesInsideWindow(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
	seedReady(t, dir, 19, "/clip.mp3")
	p := &fakePlayer{err: errors.New("spawn")}
	a, _ := newTestAgent(t, testCfg(dir), &fakeHub{}, p)
	a.now = func() time.Time { return start }
	a.pkg = ptrPkg(pkgAt(start, 19, []int{42}))
	a.tick(context.Background())
	if p.starts != 1 || a.queue.Played("42:19:2026-09-02T02:00:00Z") {
		t.Fatalf("starts %d played %v", p.starts, a.queue.Played("42:19:2026-09-02T02:00:00Z"))
	}
	p.err = nil
	a.tick(context.Background())
	if p.starts != 2 {
		t.Fatalf("starts %d", p.starts)
	}
}

func TestRestartMidClipDoesNotResume(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
	seedReady(t, dir, 19, "/clip.mp3")
	p := &fakePlayer{}
	a, _ := newTestAgent(t, testCfg(dir), &fakeHub{}, p)
	a.now = func() time.Time { return start.Add(5 * time.Second) }
	a.pkg = ptrPkg(pkgAt(start, 19, []int{42}))
	a.tick(context.Background())
	if p.starts != 0 {
		t.Fatalf("resumed starts=%d", p.starts)
	}
}

func TestStoredConfigOmittingScreenDoesNotPlay(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
	seedReady(t, dir, 19, "/clip.mp3")
	raw := `{"station_id":3,"screens":[{"id":7,"name":"Other","orientation":"landscape"}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &fakePlayer{}
	a, _ := newTestAgent(t, testCfg(dir), &fakeHub{}, p)
	a.now = func() time.Time { return start }
	a.pkg = ptrPkg(pkgAt(start, 19, []int{42}))
	a.tick(context.Background())
	if p.starts != 0 {
		t.Fatalf("played against hub config starts=%d", p.starts)
	}
}

func TestPoll401DoesNotReplacePackage(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
	seedReady(t, dir, 19, "/clip.mp3")
	h := &fakeHub{cfgErr: hub.ErrUnauthorized}
	p := &fakePlayer{}
	a, _ := newTestAgent(t, testCfg(dir), h, p)
	a.pkg = ptrPkg(pkgAt(start, 19, []int{42}))
	a.poll(context.Background())
	if a.pkg.Entries[0].Media.ID != 19 {
		t.Fatal("package replaced")
	}
	if !a.paused {
		t.Fatal("reporter not paused")
	}
}

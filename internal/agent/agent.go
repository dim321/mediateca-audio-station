package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mediateca-audio-station/internal/cache"
	"mediateca-audio-station/internal/config"
	"mediateca-audio-station/internal/fsutil"
	"mediateca-audio-station/internal/hub"
	"mediateca-audio-station/internal/player"
	"mediateca-audio-station/internal/report"
	"mediateca-audio-station/internal/schedule"
)

type hubAPI interface {
	FetchConfig(ctx context.Context) (hub.Config, error)
	FetchPackage(ctx context.Context, ifNoneMatch string) (hub.Package, bool, error)
	PostPlayEvent(ctx context.Context, screenID, mediaID int, startedAt time.Time) (int, string, error)
}

type audioPlayer interface {
	Start(ctx context.Context, path string) error
	Stop(ctx context.Context) error
}

type Agent struct {
	cfg         config.File
	hub         hubAPI
	player      audioPlayer
	cache       *cache.Cache
	queue       *report.Queue
	log         *slog.Logger
	now         func() time.Time
	mu          sync.Mutex
	pkg         *hub.Package
	etag        string
	screens     map[int]struct{}
	hasCfg      bool
	on          *schedule.Key
	paused      bool
	tokenLogged bool
	logged      string
	wake        chan struct{}
}

func Run(ctx context.Context, cfg config.File) error {
	mpv := player.New(cfg.MPVPath, cfg.AudioDevice)
	a, err := newAgent(cfg, hub.New(cfg.HubBaseURL, cfg.AgentToken), mpv, slog.Default())
	if err != nil {
		return err
	}
	mpv.SetOnExit(func(error) {
		a.log.Error("mpv exit before the scheduled stop")
	})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); a.pollLoop(ctx) }()
	go func() { defer wg.Done(); a.scheduleLoop(ctx) }()
	go func() { defer wg.Done(); a.drainLoop(ctx) }()
	wg.Wait()
	return nil
}

func newAgent(cfg config.File, h hubAPI, p audioPlayer, log *slog.Logger) (*Agent, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	q, err := report.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		cfg: cfg, hub: h, player: p, log: log, queue: q,
		cache:   cache.New(filepath.Join(cfg.DataDir, "media")),
		now:     time.Now,
		screens: map[int]struct{}{},
		wake:    make(chan struct{}, 1),
	}
	a.load()
	return a, nil
}

func (a *Agent) load() {
	if raw, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "config.json")); err == nil {
		var cfg hub.Config
		if json.Unmarshal(raw, &cfg) == nil {
			a.setScreens(cfg)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "package.json")); err == nil {
		var pkg hub.Package
		if json.Unmarshal(raw, &pkg) == nil {
			a.pkg = &pkg
		}
	}
	if raw, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "package.etagheader")); err == nil {
		a.etag = string(raw)
	}
}

func (a *Agent) pollLoop(ctx context.Context) {
	a.poll(ctx)
	tick := time.NewTicker(a.cfg.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.poll(ctx)
		}
	}
}

func (a *Agent) poll(ctx context.Context) {
	cfg, err := a.hub.FetchConfig(ctx)
	if err != nil {
		if errors.Is(err, hub.ErrUnauthorized) {
			a.rejectToken()
		} else {
			a.log.Error("hub config", "err", err.Error())
		}
		return
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return
	}
	if err := fsutil.WriteAtomic(filepath.Join(a.cfg.DataDir, "config.json"), body); err != nil {
		a.log.Error("store config", "err", err.Error())
		return
	}
	a.mu.Lock()
	a.paused = false
	a.tokenLogged = false
	a.setScreens(cfg)
	etag := a.etag
	a.mu.Unlock()
	a.log.Info("hub config", "offline_cache_hours", cfg.OfflineCacheHours, "screens", len(cfg.Screens))

	pkg, notMod, err := a.hub.FetchPackage(ctx, etag)
	if err != nil {
		if errors.Is(err, hub.ErrUnauthorized) {
			a.rejectToken()
		} else {
			a.log.Error("hub package", "err", err.Error())
		}
		return
	}
	a.mu.Lock()
	a.paused = false
	a.tokenLogged = false
	a.mu.Unlock()
	if notMod {
		a.log.Info("hub package", "status", 304)
		return
	}
	raw, err := json.Marshal(pkg)
	if err != nil {
		return
	}
	if err := fsutil.WriteAtomic(filepath.Join(a.cfg.DataDir, "package.json"), raw); err != nil {
		a.log.Error("store package", "err", err.Error())
		return
	}
	if err := fsutil.WriteAtomic(filepath.Join(a.cfg.DataDir, "package.etagheader"), []byte(pkg.HeaderETag)); err != nil {
		a.log.Error("store etag", "err", err.Error())
		return
	}
	a.mu.Lock()
	a.pkg = &pkg
	a.etag = pkg.HeaderETag
	a.mu.Unlock()
	if err := a.cache.Reconcile(ctx, a.cfg.HubBaseURL, itemsOf(pkg)); err != nil {
		a.log.Error("cache", "err", err.Error())
	}
	a.log.Info("hub package", "status", 200, "entries", len(pkg.Entries))
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func itemsOf(pkg hub.Package) []cache.Item {
	seen := map[int]struct{}{}
	var items []cache.Item
	for _, entry := range pkg.Entries {
		if _, ok := seen[entry.Media.ID]; ok || entry.Media.ID == 0 {
			continue
		}
		seen[entry.Media.ID] = struct{}{}
		items = append(items, cache.Item{ID: entry.Media.ID, URL: entry.Media.URL, Mime: entry.Media.MimeType})
	}
	return items
}

func (a *Agent) rejectToken() {
	a.mu.Lock()
	a.paused = true
	first := !a.tokenLogged
	a.tokenLogged = true
	a.mu.Unlock()
	if first {
		a.log.Error("hub token rejected")
	}
}

func (a *Agent) setScreens(cfg hub.Config) {
	a.screens = map[int]struct{}{}
	ids := make([]int, 0, len(cfg.Screens))
	for _, screen := range cfg.Screens {
		a.screens[screen.ID] = struct{}{}
		ids = append(ids, screen.ID)
	}
	a.hasCfg = true
	key := fmt.Sprint(ids)
	if key == a.logged {
		return
	}
	a.logged = key
	if len(cfg.Screens) != 1 || (len(cfg.Screens) == 1 && cfg.Screens[0].ID != a.cfg.ScreenID) {
		a.log.Error("hub screen list does not match configured screen_id", "configured", a.cfg.ScreenID, "hub_ids", ids)
	}
}

func (a *Agent) allowed() bool {
	_, ok := a.screens[a.cfg.ScreenID]
	return ok
}

func (a *Agent) scheduleLoop(ctx context.Context) {
	for {
		a.tick(ctx)
		wait := a.cfg.PollInterval
		a.mu.Lock()
		if a.pkg != nil {
			wake := schedule.WakeAt(a.now(), a.cfg.LateThreshold, a.cfg.ScreenID, a.timeline(), a.queue.Played, a.cache.Ready)
			if d := wake.Sub(a.now()); d > 0 && d < wait {
				wait = d
			}
		}
		a.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = a.player.Stop(context.Background())
			return
		case <-a.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (a *Agent) tick(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pkg == nil {
		return
	}
	if a.hasCfg && !a.allowed() {
		_ = a.player.Stop(ctx)
		a.on = nil
		return
	}
	var on *schedule.Key
	if a.on != nil {
		key := *a.on
		on = &key
	}
	cmds := schedule.Commands(a.now(), a.cfg.LateThreshold, a.cfg.ScreenID, a.timeline(), a.queue.Played, a.cache.Ready, on)
	for _, cmd := range cmds {
		switch cmd.Kind {
		case schedule.KindStop:
			if err := a.player.Stop(ctx); err != nil {
				a.log.Error("mpv stop", "err", err.Error())
			}
			a.on = nil
			a.log.Info("clip stop")
		case schedule.KindSkip:
			a.log.Info("clip skip", "reason", cmd.Reason, "media_id", cmd.Entry.Media.ID)
		case schedule.KindStart:
			path := a.cache.Path(cmd.Entry.Media.ID)
			started := a.now().UTC()
			if err := a.player.Start(ctx, path); err != nil {
				a.log.Error("mpv spawn", "err", err.Error())
				_ = a.player.Stop(ctx)
				continue
			}
			key := schedule.EntryKey(a.cfg.ScreenID, cmd.Entry)
			if err := a.queue.Enqueue(report.Event{
				ScreenID: a.cfg.ScreenID, MediaAssetID: cmd.Entry.Media.ID, StartedAt: started,
			}, key.String()); err != nil {
				a.log.Error("enqueue play event", "err", err.Error())
				continue
			}
			a.on = &key
			a.log.Info("clip start", "media_id", cmd.Entry.Media.ID)
		}
	}
}

func (a *Agent) timeline() []schedule.Entry {
	if a.pkg == nil {
		return nil
	}
	var out []schedule.Entry
	for _, entry := range a.pkg.Entries {
		parsed, err := schedule.ParseEntry(entry.StartsAt, entry.DurationSeconds, entry.ScreenIDs, entry.Media.ID, entry.Media.URL, entry.Media.MimeType, entry.Position)
		if err != nil {
			a.log.Error("ignored_entry")
			continue
		}
		out = append(out, parsed)
	}
	return out
}

func (a *Agent) drainLoop(ctx context.Context) {
	a.drain(ctx)
	tick := time.NewTicker(a.cfg.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.drain(ctx)
		}
	}
}

func (a *Agent) drain(ctx context.Context) {
	a.mu.Lock()
	paused := a.paused
	a.mu.Unlock()
	if paused {
		return
	}
	ev, ok, err := a.queue.Peek()
	if err != nil || !ok {
		return
	}
	status, body, postErr := a.hub.PostPlayEvent(ctx, ev.ScreenID, ev.MediaAssetID, ev.StartedAt)
	switch report.OutcomeFor(status, postErr != nil) {
	case report.OutcomeRemove:
		_ = a.queue.RemoveFirst()
	case report.OutcomeDrop:
		a.log.Error("drop play event", "status", status, "body", body)
		_ = a.queue.RemoveFirst()
	case report.OutcomePause:
		a.rejectToken()
	default:
		a.log.Error("play event retry", "status", status)
	}
}

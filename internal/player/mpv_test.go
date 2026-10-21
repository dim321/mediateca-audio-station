package player

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

type fakeProc struct {
	sig  os.Signal
	kill chan struct{}
}

func newFakeProc() *fakeProc {
	return &fakeProc{kill: make(chan struct{})}
}

func (p *fakeProc) Signal(s os.Signal) error {
	p.sig = s
	select {
	case <-p.kill:
	default:
		close(p.kill)
	}
	return nil
}

func (p *fakeProc) Wait() error {
	<-p.kill
	return nil
}

type fakeSpawn struct {
	argv [][]string
	proc *fakeProc
	err  error
}

func (f *fakeSpawn) Spawn(argv ...string) (Proc, error) {
	cp := append([]string(nil), argv...)
	f.argv = append(f.argv, cp)
	if f.err != nil {
		return nil, f.err
	}
	f.proc = newFakeProc()
	return f.proc, nil
}

func TestStartArgvIncludesNoVideoAndDevice(t *testing.T) {
	sp := &fakeSpawn{}
	p := New("/usr/bin/mpv", "alsa/hdmi:CARD=PCH,DEV=0")
	p.SetSpawner(sp)
	if err := p.Start(context.Background(), "/var/lib/media/19"); err != nil {
		t.Fatal(err)
	}
	got := sp.argv[0]
	want := []string{"/usr/bin/mpv", "--no-video", "--no-terminal", "--really-quiet", "--audio-device=alsa/hdmi:CARD=PCH,DEV=0", "--", "/var/lib/media/19"}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv %q", got)
		}
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sp.proc.sig != syscall.SIGTERM {
		t.Fatalf("signal %v", sp.proc.sig)
	}
}

func TestStartOmitsEmptyDevice(t *testing.T) {
	sp := &fakeSpawn{}
	p := New("mpv", "")
	p.SetSpawner(sp)
	if err := p.Start(context.Background(), "/clip.mp3"); err != nil {
		t.Fatal(err)
	}
	for _, arg := range sp.argv[0] {
		if len(arg) >= 14 && arg[:14] == "--audio-device" {
			t.Fatalf("device leaked: %q", sp.argv[0])
		}
	}
	_ = p.Stop(context.Background())
}

func TestSpawnError(t *testing.T) {
	sp := &fakeSpawn{err: errors.New("missing")}
	p := New("mpv", "")
	p.SetSpawner(sp)
	if err := p.Start(context.Background(), "/clip.mp3"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStopWithoutStart(t *testing.T) {
	p := New("mpv", "")
	p.SetSpawner(&fakeSpawn{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

type exitProc struct{}

func (exitProc) Signal(os.Signal) error { return nil }

func (exitProc) Wait() error { return errors.New("gone") }

type exitSpawn struct{}

func (exitSpawn) Spawn(...string) (Proc, error) { return exitProc{}, nil }

func TestUnexpectedExitNotifies(t *testing.T) {
	p := New("mpv", "")
	p.SetSpawner(exitSpawn{})
	called := make(chan struct{})
	p.SetOnExit(func(err error) {
		if err == nil {
			t.Errorf("expected exit error")
		}
		close(called)
	})
	if err := p.Start(context.Background(), "/clip.mp3"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("unexpected exit was not reported")
	}
}

func TestStopDoesNotReportExit(t *testing.T) {
	sp := &fakeSpawn{}
	p := New("mpv", "")
	p.SetSpawner(sp)
	p.SetOnExit(func(error) { t.Error("scheduled stop reported as an early exit") })
	if err := p.Start(context.Background(), "/clip.mp3"); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

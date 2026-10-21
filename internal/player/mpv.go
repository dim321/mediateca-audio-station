package player

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Proc interface {
	Signal(os.Signal) error
	Wait() error
}

type Spawner interface {
	Spawn(argv ...string) (Proc, error)
}

type execProc struct{ cmd *exec.Cmd }

func (p *execProc) Signal(s os.Signal) error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(s)
}

func (p *execProc) Wait() error { return p.cmd.Wait() }

type execSpawner struct{}

func (execSpawner) Spawn(argv ...string) (Proc, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProc{cmd: cmd}, nil
}

type running struct {
	proc     Proc
	done     chan struct{}
	stopping atomic.Bool
}

type Player struct {
	MPVPath     string
	AudioDevice string
	spawn       Spawner
	onExit      func(error)
	mu          sync.Mutex
	run         *running
}

func New(mpvPath, audioDevice string) *Player {
	return &Player{MPVPath: mpvPath, AudioDevice: audioDevice, spawn: execSpawner{}}
}

func (p *Player) SetSpawner(s Spawner) { p.spawn = s }

func (p *Player) SetOnExit(fn func(error)) {
	p.mu.Lock()
	p.onExit = fn
	p.mu.Unlock()
}

func (p *Player) Start(ctx context.Context, path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.stopLocked(ctx); err != nil {
		return err
	}
	argv := []string{p.MPVPath, "--no-video", "--no-terminal", "--really-quiet"}
	if p.AudioDevice != "" {
		argv = append(argv, "--audio-device="+p.AudioDevice)
	}
	argv = append(argv, "--", path)
	proc, err := p.spawn.Spawn(argv...)
	if err != nil {
		return err
	}
	done := make(chan struct{})
	current := &running{proc: proc, done: done}
	go func() {
		err := proc.Wait()
		close(done)
		if current.stopping.Load() {
			return
		}
		p.mu.Lock()
		unexpected := p.run == current
		cb := p.onExit
		p.mu.Unlock()
		if unexpected && cb != nil {
			cb(err)
		}
	}()
	p.run = current
	return nil
}

func (p *Player) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopLocked(ctx)
}

func (p *Player) stopLocked(ctx context.Context) error {
	if p.run == nil {
		return nil
	}
	p.run.stopping.Store(true)
	_ = p.run.proc.Signal(syscall.SIGTERM)
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-p.run.done:
	case <-timer.C:
		_ = p.run.proc.Signal(syscall.SIGKILL)
		<-p.run.done
	case <-ctx.Done():
		_ = p.run.proc.Signal(syscall.SIGKILL)
		return fmt.Errorf("stop mpv: %w", ctx.Err())
	}
	p.run = nil
	return nil
}

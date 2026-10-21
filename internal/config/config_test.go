package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRejectsGroupReadableConfig(t *testing.T) {
	mpv := filepath.Join(t.TempDir(), "mpv")
	if err := os.WriteFile(mpv, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, `
hub_base_url: "https://hub.example"
agent_token: "secret-token-value"
data_dir: "/var/lib/mediateca-audio-station"
screen_id: 42
mpv_path: "`+mpv+`"
`, 0o640)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadsDefaults(t *testing.T) {
	mpv := filepath.Join(t.TempDir(), "mpv")
	if err := os.WriteFile(mpv, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, `
hub_base_url: "https://hub.example/"
agent_token: "secret-token-value"
data_dir: "/var/lib/mediateca-audio-station"
screen_id: 42
mpv_path: "`+mpv+`"
audio_device: "alsa/default"
`, 0o600)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HubBaseURL != "https://hub.example" {
		t.Fatalf("base %q", cfg.HubBaseURL)
	}
	if cfg.ScreenID != 42 || cfg.LateThreshold != 2*time.Second || cfg.PollInterval != time.Minute {
		t.Fatalf("%+v", cfg)
	}
	if cfg.MPVPath != mpv || cfg.AudioDevice != "alsa/default" {
		t.Fatalf("%+v", cfg)
	}
}

func TestRejectsMissingMPV(t *testing.T) {
	path := writeConfig(t, `
hub_base_url: "https://hub.example"
agent_token: "secret-token-value"
data_dir: "/var/lib/mediateca-audio-station"
screen_id: 42
mpv_path: "mpv-does-not-exist-audio-station"
`, 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected missing mpv error")
	}
}

func TestRejectsPathOnHubURL(t *testing.T) {
	mpv := filepath.Join(t.TempDir(), "mpv")
	if err := os.WriteFile(mpv, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, `
hub_base_url: "https://hub.example/api"
agent_token: "secret-token-value"
data_dir: "/var/lib/mediateca-audio-station"
screen_id: 42
mpv_path: "`+mpv+`"
`, 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected path error")
	}
}

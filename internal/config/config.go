package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type File struct {
	HubBaseURL    string
	AgentToken    string
	DataDir       string
	ScreenID      int
	LateThreshold time.Duration
	PollInterval  time.Duration
	MPVPath       string
	AudioDevice   string
}

type fileYAML struct {
	HubBaseURL    string `yaml:"hub_base_url"`
	AgentToken    string `yaml:"agent_token"`
	DataDir       string `yaml:"data_dir"`
	ScreenID      int    `yaml:"screen_id"`
	LateThreshold string `yaml:"late_threshold"`
	PollInterval  string `yaml:"poll_interval"`
	MPVPath       string `yaml:"mpv_path"`
	AudioDevice   string `yaml:"audio_device"`
}

func Load(path string) (File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return File{}, fmt.Errorf("config %s is group or world readable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var doc fileYAML
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return File{}, err
	}
	cfg := File{
		HubBaseURL:    strings.TrimRight(doc.HubBaseURL, "/"),
		AgentToken:    doc.AgentToken,
		DataDir:       doc.DataDir,
		ScreenID:      doc.ScreenID,
		LateThreshold: 2 * time.Second,
		PollInterval:  time.Minute,
		MPVPath:       doc.MPVPath,
		AudioDevice:   doc.AudioDevice,
	}
	if cfg.MPVPath == "" {
		cfg.MPVPath = "mpv"
	}
	if doc.LateThreshold != "" {
		parsed, err := time.ParseDuration(doc.LateThreshold)
		if err != nil {
			return File{}, fmt.Errorf("late_threshold: %w", err)
		}
		cfg.LateThreshold = parsed
	}
	if doc.PollInterval != "" {
		parsed, err := time.ParseDuration(doc.PollInterval)
		if err != nil {
			return File{}, fmt.Errorf("poll_interval: %w", err)
		}
		cfg.PollInterval = parsed
	}
	if err := cfg.validate(); err != nil {
		return File{}, err
	}
	return cfg, nil
}

func (f File) validate() error {
	if f.HubBaseURL == "" || !(strings.HasPrefix(f.HubBaseURL, "http://") || strings.HasPrefix(f.HubBaseURL, "https://")) {
		return fmt.Errorf("hub_base_url is invalid")
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(f.HubBaseURL, "https://"), "http://")
	if rest == "" || strings.Contains(rest, "/") {
		return fmt.Errorf("hub_base_url must not include a path")
	}
	if f.AgentToken == "" {
		return fmt.Errorf("agent_token is required")
	}
	if f.DataDir == "" {
		return fmt.Errorf("data_dir is required")
	}
	if f.ScreenID <= 0 {
		return fmt.Errorf("screen_id must be positive")
	}
	if f.PollInterval <= 0 {
		return fmt.Errorf("poll_interval must be positive")
	}
	if f.LateThreshold < 0 {
		return fmt.Errorf("late_threshold must be non-negative")
	}
	if strings.Contains(f.MPVPath, "/") {
		if _, err := os.Stat(f.MPVPath); err != nil {
			return fmt.Errorf("mpv_path: %w", err)
		}
		return nil
	}
	if _, err := exec.LookPath(f.MPVPath); err != nil {
		return fmt.Errorf("mpv_path: %w", err)
	}
	return nil
}

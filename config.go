package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type UserConfig struct {
	SocketPath         string  `json:"socket_path,omitempty"`
	EndpointURL        string  `json:"endpoint_url,omitempty"`
	RefreshSeconds     float64 `json:"refresh_seconds,omitempty"`
	Theme              string  `json:"theme,omitempty"`
	LogLimit           int     `json:"log_limit,omitempty"`
	ConfirmDestructive bool    `json:"confirm_destructive"`
}

func defaultUserConfig() UserConfig {
	return UserConfig{RefreshSeconds: refreshInterval.Seconds(), LogLimit: 200, ConfirmDestructive: true}
}

func (c UserConfig) normalized() UserConfig {
	defaults := defaultUserConfig()
	if c.RefreshSeconds < 0.1 || c.RefreshSeconds > 60 {
		c.RefreshSeconds = defaults.RefreshSeconds
	}
	if c.LogLimit < 20 || c.LogLimit > 10000 {
		c.LogLimit = defaults.LogLimit
	}
	return c
}

func (c UserConfig) refreshDuration() time.Duration {
	return time.Duration(c.RefreshSeconds * float64(time.Second))
}

func configPath() string {
	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		return filepath.Join(root, "lzpody", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "lzpody", "config.json")
}

func loadUserConfig() (UserConfig, error) {
	config := defaultUserConfig()
	path := configPath()
	if path == "" {
		return config, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("read lzpody config: %w", err)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return defaultUserConfig(), fmt.Errorf("decode lzpody config: %w", err)
	}
	return config.normalized(), nil
}

func saveUserConfig(config UserConfig) error {
	path := configPath()
	if path == "" {
		return fmt.Errorf("cannot determine lzpody config path")
	}
	config = config.normalized()
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode lzpody config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create lzpody config directory: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write lzpody config: %w", err)
	}
	return nil
}

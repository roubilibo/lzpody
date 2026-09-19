package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var defaultThemeColors = map[string]string{
	"background": "#12101c",
	"foreground": "#f0c4a8",
	"accent":     "#e15a48",
	"selection":  "#2c2438",
	"muted":      "#6d5a68",
	"green":      "#7e9a6a",
	"red":        "#d6453d",
	"cyan":       "#4a9bb0",
	"yellow":     "#f0b45a",
}

type UITheme struct {
	Name, Normal, Title, Selected, Muted, Running, Stopped, Error, Border, Key string
}

func ansiColor(hex string, background bool, bold bool) string {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return ""
	}
	red, redErr := strconv.ParseInt(hex[0:2], 16, 32)
	green, greenErr := strconv.ParseInt(hex[2:4], 16, 32)
	blue, blueErr := strconv.ParseInt(hex[4:6], 16, 32)
	if redErr != nil || greenErr != nil || blueErr != nil {
		return ""
	}
	base := 38
	if background {
		base = 48
	}
	if bold {
		return fmt.Sprintf("\x1b[1;%d;2;%d;%d;%dm", base, red, green, blue)
	}
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", base, red, green, blue)
}

func loadUITheme() UITheme {
	colors := make(map[string]string, len(defaultThemeColors))
	for key, value := range defaultThemeColors {
		colors[key] = value
	}
	themeID := ""
	statePath := filepath.Join(os.Getenv("HOME"), ".local/state/omarchy/current/theme.name")
	if data, err := os.ReadFile(statePath); err == nil {
		themeID = strings.TrimSpace(string(data))
	}
	if themeID != "" {
		candidates := []string{
			filepath.Join(os.Getenv("HOME"), ".config/omarchy/themes", themeID, "colors.toml"),
			filepath.Join(os.Getenv("OMARCHY_PATH"), "themes", themeID, "colors.toml"),
		}
		if os.Getenv("OMARCHY_PATH") == "" {
			candidates[1] = filepath.Join("/usr/share/omarchy", "themes", themeID, "colors.toml")
		}
		for _, candidate := range candidates {
			data, err := os.ReadFile(candidate)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				trimmedLine := strings.TrimSpace(line)
				if strings.HasPrefix(trimmedLine, "#") {
					continue
				}
				keyValue := strings.SplitN(trimmedLine, "=", 2)
				if len(keyValue) != 2 {
					continue
				}
				key := strings.TrimSpace(keyValue[0])
				value := strings.Trim(strings.TrimSpace(keyValue[1]), "\"")
				if _, known := colors[key]; known && len(value) == 7 && strings.HasPrefix(value, "#") {
					colors[key] = value
				}
			}
			break
		}
	}
	name := "Omarchy"
	if themeID != "" {
		name = strings.Title(strings.ReplaceAll(themeID, "-", " "))
	}
	return UITheme{
		Name:     name,
		Normal:   ansiColor(colors["foreground"], false, false),
		Title:    ansiColor(colors["accent"], false, true),
		Selected: ansiColor(colors["foreground"], false, false) + ansiColor(colors["selection"], true, false),
		Muted:    ansiColor(colors["muted"], false, false),
		Running:  ansiColor(colors["green"], false, true),
		Stopped:  ansiColor(colors["muted"], false, false),
		Error:    ansiColor(colors["red"], false, true),
		Border:   ansiColor(colors["muted"], false, false),
		Key:      ansiColor(colors["yellow"], false, true),
	}
}

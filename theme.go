package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	Name                                             string
	Normal, Title, Selected, Muted, Running, Stopped lipgloss.Style
	Error, Border, Key                               lipgloss.Style
}

func colorStyle(foreground, background string, bold bool) lipgloss.Style {
	style := lipgloss.NewStyle()
	if foreground != "" {
		style = style.Foreground(lipgloss.Color(foreground))
	}
	if background != "" {
		style = style.Background(lipgloss.Color(background))
	}
	if bold {
		style = style.Bold(true)
	}
	return style
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
		Normal:   colorStyle(colors["foreground"], "", false),
		Title:    colorStyle(colors["accent"], "", true),
		Selected: colorStyle(colors["foreground"], colors["selection"], false),
		Muted:    colorStyle(colors["muted"], "", false),
		Running:  colorStyle(colors["green"], "", true),
		Stopped:  colorStyle(colors["muted"], "", false),
		Error:    colorStyle(colors["red"], "", true),
		Border:   colorStyle(colors["muted"], "", false),
		Key:      colorStyle(colors["yellow"], "", true),
	}
}

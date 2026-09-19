package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	defaultAPIVersion = "v5.0.0"
	refreshInterval   = 2 * time.Second
)

var resourceModes = []string{"containers", "pods", "images", "volumes", "networks"}
var resourceLabels = map[string]string{
	"containers": "Containers", "pods": "Pods", "images": "Images",
	"volumes": "Volumes", "networks": "Networks",
}

type App struct {
	Client         *PodmanClient
	Mode           string
	Items          map[string][]Item
	Selected       map[string]int
	DetailMode     string
	DetailLines    []string
	StatsHistory   map[string][]map[string]any
	Filter         string
	HideStopped    bool
	Status         string
	LastRefresh    time.Time
	DetailScroll   int
	DetailViewRows int
	FocusMain      bool
	MenuOpen       bool
	MenuIndex      int
	Dirty          bool
	ConfirmAction  string
	FilterInput    bool
	FilterDraft    string
}

func NewApp(client *PodmanClient) *App {
	items, selected := map[string][]Item{}, map[string]int{}
	for _, mode := range resourceModes {
		items[mode] = []Item{}
		selected[mode] = 0
	}
	return &App{Client: client, Mode: "containers", Items: items, Selected: selected, DetailMode: "summary", StatsHistory: map[string][]map[string]any{}, Status: "Connecting to rootless Podman...", Dirty: true}
}

func (a *App) current() *Item {
	items := a.Items[a.Mode]
	index := a.Selected[a.Mode]
	if index < 0 || index >= len(items) {
		return nil
	}
	return &items[index]
}

func (a *App) refresh(keepID string) {
	loaders := map[string]func() ([]map[string]any, error){"containers": a.Client.containers, "pods": a.Client.pods, "images": a.Client.images, "volumes": a.Client.volumes, "networks": a.Client.networks}
	converters := map[string]func(map[string]any) Item{"containers": containerItem, "pods": podItem, "images": imageItem, "volumes": volumeItem, "networks": networkItem}
	needle := strings.ToLower(a.Filter)
	var firstError error
	for _, mode := range resourceModes {
		raw, err := loaders[mode]()
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			a.Items[mode] = nil
			a.Selected[mode] = 0
			continue
		}
		converted := make([]Item, 0, len(raw))
		for _, object := range raw {
			item := converters[mode](object)
			if mode == "containers" {
				if sample, statsErr := a.Client.stats(item.ID); statsErr == nil {
					if payload := statsPayload(sample); payload != nil {
						item.CPU = numberValue(payload["CPU"])
						if item.CPU == 0 {
							item.CPU = numberValue(payload["AvgCPU"])
						}
					}
				}
			}
			if a.HideStopped && mode == "containers" && !isRunning(item.State) {
				continue
			}
			if needle != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.Image+" "+item.State), needle) {
				continue
			}
			converted = append(converted, item)
		}
		a.Items[mode] = converted
		if mode == a.Mode && keepID != "" {
			a.Selected[mode] = 0
			for index, item := range converted {
				if item.ID == keepID {
					a.Selected[mode] = index
					break
				}
			}
		} else if a.Selected[mode] >= len(converted) {
			a.Selected[mode] = max(0, len(converted)-1)
		}
	}
	a.LastRefresh = time.Now()
	a.Dirty = true
	if firstError != nil {
		a.Status = firstError.Error()
	} else {
		a.Status = "Updated " + a.LastRefresh.Format("15:04:05")
	}
	if a.current() == nil {
		a.DetailLines = []string{"No item selected."}
		a.DetailMode = "summary"
	} else {
		switch a.DetailMode {
		case "summary":
			a.loadSummary()
		case "logs":
			a.loadLogs(true)
		case "stats":
			a.loadStats(true)
		case "top":
			a.loadTop(true)
		}
	}
}

func isRunning(state string) bool {
	return strings.ToLower(state) == "running" || strings.ToLower(state) == "paused"
}

func (a *App) loadSummary() {
	item := a.current()
	if item == nil {
		a.DetailLines = []string{"No item selected."}
		a.DetailScroll = 0
		return
	}
	a.DetailLines = []string{fmt.Sprintf("Name:    %s", item.Name), fmt.Sprintf("ID:      %s", item.ID), fmt.Sprintf("State:   %s", item.State), fmt.Sprintf("Status:  %s", item.Status)}
	if item.Kind == "container" {
		health := item.Health
		if inspected, err := a.Client.inspect("containers", item.ID); err == nil {
			if object, ok := inspected.(map[string]any); ok {
				health = healthStatus(object, "no healthcheck")
				item.Health = health
			}
		}
		a.DetailLines = append(a.DetailLines, fmt.Sprintf("Health:  %s", defaultText(health, "unknown")), fmt.Sprintf("CPU:     %.2f%%", item.CPU), "Ports / forwarding:")
		if len(item.Ports) == 0 {
			a.DetailLines = append(a.DetailLines, "  (none)")
		} else {
			for _, port := range item.Ports {
				a.DetailLines = append(a.DetailLines, "  "+port)
			}
		}
	}
	if item.Image != "" {
		a.DetailLines = append(a.DetailLines, "Image:   "+item.Image)
	}
	a.DetailLines = append(a.DetailLines, "", "Press x or ? to open available actions.")
	a.DetailScroll = 0
}

func (a *App) loadLogs(silent ...bool) {
	item := a.current()
	if item == nil || item.Kind != "container" {
		return
	}
	value, err := a.Client.logs(item.ID)
	if err != nil {
		a.Status = err.Error()
		return
	}
	a.DetailLines = splitLines(value, "(no logs)")
	a.DetailMode = "logs"
	if len(silent) == 0 || !silent[0] {
		a.DetailScroll = 0
		a.Status = "Logs: " + item.Name
	}
}

func (a *App) loadStats(silent ...bool) {
	item := a.current()
	if item == nil || (item.Kind != "container" && item.Kind != "pod") {
		return
	}
	var value any
	var err error
	if item.Kind == "container" {
		value, err = a.Client.stats(item.ID)
	} else {
		value, err = a.Client.podStats(item.ID)
	}
	if err != nil {
		a.Status = err.Error()
		return
	}
	if sample := statsPayload(value); sample != nil {
		history := append(a.StatsHistory[item.ID], sample)
		if len(history) > 60 {
			history = history[len(history)-60:]
		}
		a.StatsHistory[item.ID] = history
		a.DetailLines = statsLines(history)
	} else {
		a.DetailLines = []string{"No statistics available."}
	}
	a.DetailMode = "stats"
	if len(silent) == 0 || !silent[0] {
		a.DetailScroll = 0
		a.Status = "Stats: " + item.Name
	}
}

func (a *App) loadInspect(mode string) {
	item := a.current()
	if item == nil {
		return
	}
	value, err := a.Client.inspect(item.Kind+"s", item.ID)
	if item.Kind == "network" {
		value, err = a.Client.inspect("networks", item.ID)
	}
	if item.Kind == "image" {
		value, err = a.Client.inspect("images", item.ID)
	}
	if item.Kind == "volume" {
		value, err = a.Client.inspect("volumes", item.ID)
	}
	if err != nil {
		a.Status = err.Error()
		return
	}
	encoded, _ := json.MarshalIndent(value, "", "  ")
	a.DetailLines = strings.Split(string(encoded), "\n")
	a.DetailMode = mode
	a.DetailScroll = 0
	a.Status = strings.Title(mode) + ": " + item.Name
}

func (a *App) loadEnv() {
	item := a.current()
	if item == nil || item.Kind != "container" {
		return
	}
	value, err := a.Client.inspect("containers", item.ID)
	if err != nil {
		a.Status = err.Error()
		return
	}
	lines := []string{}
	if object, ok := value.(map[string]any); ok {
		if config, ok := object["Config"].(map[string]any); ok {
			if env, ok := config["Env"].([]any); ok {
				for _, entry := range env {
					lines = append(lines, scalarText(entry))
				}
			}
		}
	}
	if len(lines) == 0 {
		lines = []string{"(no environment variables)"}
	}
	a.DetailLines = lines
	a.DetailMode = "env"
	a.DetailScroll = 0
	a.Status = "Environment: " + item.Name
}

func (a *App) loadTop(silent ...bool) {
	item := a.current()
	if item == nil || item.Kind != "container" {
		return
	}
	value, err := a.Client.top(item.ID)
	if err != nil {
		a.Status = err.Error()
		return
	}
	lines := []string{}
	if object, ok := value.(map[string]any); ok {
		titles, _ := object["Titles"].([]any)
		processes, _ := object["Processes"].([]any)
		if len(titles) > 0 {
			values := []string{}
			for _, title := range titles {
				values = append(values, scalarText(title))
			}
			lines = append(lines, strings.Join(values, "  "))
		}
		for _, process := range processes {
			if row, ok := process.([]any); ok {
				values := []string{}
				for _, cell := range row {
					values = append(values, scalarText(cell))
				}
				lines = append(lines, strings.Join(values, "  "))
			}
		}
	} else {
		encoded, _ := json.MarshalIndent(value, "", "  ")
		lines = strings.Split(string(encoded), "\n")
	}
	if len(lines) == 0 {
		lines = []string{"(no processes)"}
	}
	a.DetailLines = lines
	a.DetailMode = "top"
	if len(silent) == 0 || !silent[0] {
		a.DetailScroll = 0
		a.Status = "Top: " + item.Name
	}
}

func splitLines(value, fallback string) []string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{fallback}
	}
	return lines
}

func (a *App) detailTabs() []string {
	item := a.current()
	if item != nil && item.Kind == "container" {
		return []string{"summary", "logs", "stats", "env", "config", "top"}
	}
	if item != nil && item.Kind == "pod" {
		return []string{"summary", "stats", "config"}
	}
	return []string{"summary", "config"}
}

func (a *App) cycleTab(delta int) {
	tabs := a.detailTabs()
	index := 0
	for i, tab := range tabs {
		if tab == a.DetailMode {
			index = i
		}
	}
	next := tabs[(index+delta+len(tabs))%len(tabs)]
	switch next {
	case "summary":
		a.DetailMode = "summary"
		a.loadSummary()
	case "logs":
		a.loadLogs()
	case "stats":
		a.loadStats()
	case "env":
		a.loadEnv()
	case "config":
		a.loadInspect("config")
	case "top":
		a.loadTop()
	}
}

func (a *App) move(delta int) {
	items := a.Items[a.Mode]
	if len(items) == 0 {
		return
	}
	a.Selected[a.Mode] = max(0, min(len(items)-1, a.Selected[a.Mode]+delta))
	a.DetailMode = "summary"
	a.loadSummary()
}
func (a *App) moveFocus(delta int) {
	index := 0
	for i, mode := range resourceModes {
		if mode == a.Mode {
			index = i
		}
	}
	a.Mode = resourceModes[(index+delta+len(resourceModes))%len(resourceModes)]
	a.DetailMode = "summary"
	a.loadSummary()
}
func (a *App) toggleMode(mode string) {
	if a.Mode == mode {
		a.FocusMain = false
		return
	}
	a.Mode = mode
	a.FocusMain = false
	a.DetailMode = "summary"
	a.loadSummary()
}

func (a *App) menuEntries() [][2]string {
	item := a.current()
	if item == nil {
		return [][2]string{{"Refresh", "refresh"}}
	}
	if item.Kind == "container" {
		pause, label := "pause", "Pause"
		if strings.ToLower(item.State) == "paused" {
			pause, label = "unpause", "Unpause"
		}
		return [][2]string{{"Start [S]", "start"}, {"Stop [s]", "stop"}, {"Restart [r]", "restart"}, {label + " [p]", pause}, {"Kill [K]", "kill"}, {"Logs [m]", "logs"}, {"Attach [a]", "attach"}, {"Stats [t]", "stats"}, {"Environment", "env"}, {"Config [i]", "config"}, {"Top", "top"}, {"Exec shell [E]", "shell"}, {"Hide stopped [e]", "hide_stopped"}, {"Remove [d]", "remove"}}
	}
	if item.Kind == "pod" {
		return [][2]string{{"Start [S]", "start"}, {"Stop [s]", "stop"}, {"Restart [r]", "restart"}, {"Pause [p]", "pause"}, {"Unpause [p]", "unpause"}, {"Kill [K]", "kill"}, {"Stats [t]", "stats"}, {"Config [i]", "config"}, {"Remove [d]", "remove"}}
	}
	return [][2]string{{"Config [i]", "config"}, {"Remove [d]", "remove"}}
}

func (a *App) openMenu() {
	a.MenuOpen = true
	a.MenuIndex = 0
}

func (a *App) closeMenu() {
	a.MenuOpen = false
}

func (a *App) moveMenu(delta int) {
	entries := a.menuEntries()
	if len(entries) == 0 {
		return
	}
	a.MenuIndex = max(0, min(len(entries)-1, a.MenuIndex+delta))
}

func (a *App) selectedMenuAction() string {
	entries := a.menuEntries()
	if a.MenuIndex < 0 || a.MenuIndex >= len(entries) {
		return ""
	}
	return entries[a.MenuIndex][1]
}

func (a *App) perform(action string) {
	item := a.current()
	if item == nil {
		a.Status = "No item selected."
		return
	}
	id := item.ID
	name := item.Name
	var err error
	switch {
	case item.Kind == "container":
		err = a.Client.resourceAction("containers", id, action)
	case item.Kind == "pod":
		err = a.Client.resourceAction("pods", id, action)
	case action == "remove":
		err = a.Client.remove(item.Kind+"s", id)
	default:
		a.Status = fmt.Sprintf("Action %q is not available for %s.", action, item.Kind)
		return
	}
	if err != nil {
		a.Status = err.Error()
		return
	}
	a.refresh(id)
	if strings.HasPrefix(a.Status, "Updated ") {
		a.Status = strings.Title(action) + " " + name + ": OK"
	}
}

func (a *App) toggleHideStopped() {
	current := a.current()
	keep := ""
	if current != nil {
		keep = current.ID
	}
	a.HideStopped = !a.HideStopped
	a.refresh(keep)
	if a.HideStopped {
		a.Status = "Stopped containers hidden."
	} else {
		a.Status = "Stopped containers shown."
	}
	a.Dirty = true
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *App) loadDetail() {
	if a.Mode == "containers" {
		a.loadLogs()
	} else {
		a.loadInspect("config")
	}
}
func (a *App) scroll(delta int) {
	viewRows := a.DetailViewRows
	if viewRows < 1 {
		viewRows = 1
	}
	maximum := max(0, len(a.DetailLines)-viewRows)
	current := a.DetailScroll
	current = max(0, min(maximum, current+delta))
	a.setDetailScroll(current)
}

func (a *App) pageScroll(direction int) {
	viewRows := a.DetailViewRows
	if viewRows < 2 {
		viewRows = 2
	}
	a.scroll(direction * (viewRows - 1))
}
func (a *App) setDetailScroll(value int) { a.DetailScroll = value }

// DetailScroll is kept separate from the visible rendering size so tests and
// future renderers can move the detail viewport without touching the API layer.
func (a *App) detailScrollValue() int { return a.DetailScroll }

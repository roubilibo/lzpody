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
	logsBottomSpacer  = 10
)

var resourceModes = []string{"containers", "pods", "images", "volumes", "networks", "secrets"}
var resourceLabels = map[string]string{
	"containers": "Containers", "pods": "Pods", "images": "Images",
	"volumes": "Volumes", "networks": "Networks", "secrets": "Secrets",
}

type App struct {
	Client          *PodmanClient
	Mode            string
	Items           map[string][]Item
	Selected        map[string]int
	DetailMode      string
	DetailLines     []string
	DetailRawLines  []string
	StatsHistory    map[string][]map[string]any
	Filter          string
	HideStopped     bool
	Status          string
	LastRefresh     time.Time
	DetailScroll    int
	DetailViewRows  int
	DetailViewWidth int
	DetailRequestID uint64
	LogsFollow      bool
	FocusMain       bool
	MenuOpen        bool
	MenuIndex       int
	Dirty           bool
	ConfirmAction   string
	FilterInput     bool
	FilterDraft     string
	Prompt          *promptState
	ContainerForm   *containerFormState
	CursorVisible   bool
	Shell           *shellSession
	Pull            *pullSession
	PullOverlay     bool
}

type resourceSnapshot struct {
	items       map[string][]Item
	status      string
	hasError    bool
	lastRefresh time.Time
}

type detailResult struct {
	itemID       string
	mode         string
	requestID    uint64
	lines        []string
	health       string
	status       string
	statsHistory []map[string]any
	err          error
}

type promptStep struct {
	label     string
	sensitive bool
}

type promptState struct {
	action string
	steps  []promptStep
	values []string
	value  string
}

type containerFormField struct {
	label, hint, value string
	tab                int
}

type containerFormState struct {
	action      string
	title       string
	tabs        []string
	fields      []containerFormField
	tab         int
	activeField int
}

func NewApp(client *PodmanClient) *App {
	items, selected := map[string][]Item{}, map[string]int{}
	for _, mode := range resourceModes {
		items[mode] = []Item{}
		selected[mode] = 0
	}
	return &App{Client: client, Mode: "containers", Items: items, Selected: selected, DetailMode: "summary", StatsHistory: map[string][]map[string]any{}, LogsFollow: true, Status: "Connecting to rootless Podman...", Dirty: true}
}

func (a *App) current() *Item {
	items := a.Items[a.Mode]
	index := a.Selected[a.Mode]
	if index < 0 || index >= len(items) {
		return nil
	}
	return &items[index]
}

func fetchResourceSnapshot(client *PodmanClient, filter string, hideStopped bool) resourceSnapshot {
	loaders := map[string]func() ([]map[string]any, error){"containers": client.containers, "pods": client.pods, "images": client.images, "volumes": client.volumes, "networks": client.networks, "secrets": client.secrets}
	converters := map[string]func(map[string]any) Item{"containers": containerItem, "pods": podItem, "images": imageItem, "volumes": volumeItem, "networks": networkItem, "secrets": secretItem}
	items := map[string][]Item{}
	needle := strings.ToLower(filter)
	var firstError error
	for _, mode := range resourceModes {
		raw, err := loaders[mode]()
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			items[mode] = nil
			continue
		}
		converted := make([]Item, 0, len(raw))
		for _, object := range raw {
			item := converters[mode](object)
			if mode == "containers" {
				if sample, statsErr := client.stats(item.ID); statsErr == nil {
					if payload := statsPayload(sample); payload != nil {
						item.CPU = numberValue(payload["CPU"])
						if item.CPU == 0 {
							item.CPU = numberValue(payload["AvgCPU"])
						}
					}
				}
			}
			if hideStopped && mode == "containers" && !isRunning(item.State) {
				continue
			}
			if needle != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.Image+" "+item.State), needle) {
				continue
			}
			converted = append(converted, item)
		}
		items[mode] = converted
	}
	now := time.Now()
	status := "Updated " + now.Format("15:04:05")
	if firstError != nil {
		status = firstError.Error()
	}
	return resourceSnapshot{items: items, status: status, hasError: firstError != nil, lastRefresh: now}
}

func (a *App) applyRefresh(result resourceSnapshot, keepID string) {
	for _, mode := range resourceModes {
		converted := result.items[mode]
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
	a.LastRefresh = result.lastRefresh
	if result.hasError || (a.DetailMode != "logs" && a.DetailMode != "stats" && a.DetailMode != "top" && a.DetailMode != "system" && a.DetailMode != "events" && a.DetailMode != "exec" && a.DetailMode != "shell" && a.DetailMode != "pull" && a.DetailMode != "relationships") {
		a.Status = result.status
	}
	a.Dirty = true
}

func fetchDetail(client *PodmanClient, item Item, mode string, history []map[string]any) detailResult {
	result := detailResult{itemID: item.ID, mode: mode}
	switch mode {
	case "system":
		value, err := client.info()
		if err != nil {
			result.err = err
			return result
		}
		encoded, _ := json.MarshalIndent(value, "", "  ")
		result.lines = strings.Split(string(encoded), "\n")
		result.status = "System information"
	case "events":
		value, err := client.events(time.Now().Add(-10 * time.Second))
		if err != nil {
			result.err = err
			return result
		}
		result.lines = splitLines(valueText(value), "(no recent events)")
		result.status = "Events"
	case "summary":
		result.lines = []string{fmt.Sprintf("Name:    %s", item.Name), fmt.Sprintf("ID:      %s", item.ID), fmt.Sprintf("State:   %s", item.State), fmt.Sprintf("Status:  %s", item.Status)}
		if item.Kind == "container" {
			health := item.Health
			if inspected, err := client.inspect("containers", item.ID); err == nil {
				if object, ok := inspected.(map[string]any); ok {
					health = healthStatus(object, "no healthcheck")
				}
			}
			result.health = health
			result.lines = append(result.lines, fmt.Sprintf("Health:  %s", defaultText(health, "unknown")), fmt.Sprintf("CPU:     %.2f%%", item.CPU), "Ports / forwarding:")
			if len(item.Ports) == 0 {
				result.lines = append(result.lines, "  (none)")
			} else {
				for _, port := range item.Ports {
					result.lines = append(result.lines, "  "+port)
				}
			}
		}
		if item.Image != "" {
			result.lines = append(result.lines, "Image:   "+item.Image)
		}
		result.lines = append(result.lines, "", "Press x or ? to open available actions.")
	case "logs":
		if item.Kind != "container" {
			return result
		}
		value, err := client.logs(item.ID)
		if err != nil {
			result.err = err
			return result
		}
		result.lines = markLogTimestamps(splitLines(value, "(no logs)"))
		result.status = "Logs: " + item.Name
	case "stats":
		if item.Kind != "container" && item.Kind != "pod" {
			return result
		}
		var value any
		var err error
		if item.Kind == "container" {
			value, err = client.stats(item.ID)
		} else {
			value, err = client.podStats(item.ID)
		}
		if err != nil {
			result.err = err
			return result
		}
		if sample := statsPayload(value); sample != nil {
			history = append(history, sample)
			if len(history) > 60 {
				history = history[len(history)-60:]
			}
			result.statsHistory = history
			result.lines = statsLines(history)
		} else {
			result.lines = []string{"No statistics available."}
		}
		result.status = "Stats: " + item.Name
	case "env":
		if item.Kind != "container" {
			return result
		}
		value, err := client.inspect("containers", item.ID)
		if err != nil {
			result.err = err
			return result
		}
		if object, ok := value.(map[string]any); ok {
			if config, ok := object["Config"].(map[string]any); ok {
				if env, ok := config["Env"].([]any); ok {
					for _, entry := range env {
						result.lines = append(result.lines, scalarText(entry))
					}
				}
			}
		}
		if len(result.lines) == 0 {
			result.lines = []string{"(no environment variables)"}
		}
		result.status = "Environment: " + item.Name
	case "config":
		value, err := inspectItem(client, item)
		if err != nil {
			result.err = err
			return result
		}
		encoded, _ := json.MarshalIndent(value, "", "  ")
		result.lines = strings.Split(string(encoded), "\n")
		result.status = strings.Title(mode) + ": " + item.Name
	case "history":
		if item.Kind != "image" {
			return result
		}
		value, err := client.imageHistory(item.ID)
		if err != nil {
			result.err = err
			return result
		}
		encoded, _ := json.MarshalIndent(value, "", "  ")
		result.lines = strings.Split(string(encoded), "\n")
		result.status = "History: " + item.Name
	case "relationships":
		value, err := inspectItem(client, item)
		if err != nil {
			result.err = err
			return result
		}
		result.lines = relationshipLines(item.Kind, value)
		result.status = "Relationships: " + item.Name
	case "top":
		if item.Kind != "container" {
			return result
		}
		value, err := client.top(item.ID)
		if err != nil {
			result.err = err
			return result
		}
		if object, ok := value.(map[string]any); ok {
			titles, _ := object["Titles"].([]any)
			processes, _ := object["Processes"].([]any)
			if len(titles) > 0 {
				values := []string{}
				for _, title := range titles {
					values = append(values, scalarText(title))
				}
				result.lines = append(result.lines, strings.Join(values, "  "))
			}
			for _, process := range processes {
				if row, ok := process.([]any); ok {
					values := []string{}
					for _, cell := range row {
						values = append(values, scalarText(cell))
					}
					result.lines = append(result.lines, strings.Join(values, "  "))
				}
			}
		} else {
			encoded, _ := json.MarshalIndent(value, "", "  ")
			result.lines = strings.Split(string(encoded), "\n")
		}
		if len(result.lines) == 0 {
			result.lines = []string{"(no processes)"}
		}
		result.status = "Top: " + item.Name
	}
	return result
}

func inspectItem(client *PodmanClient, item Item) (any, error) {
	return client.inspect(item.Kind+"s", item.ID)
}

func relationshipLines(kind string, value any) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return []string{"(no relationship data)"}
	}
	selected := map[string]any{}
	switch kind {
	case "container":
		for _, key := range []string{"Id", "Name", "Pod", "Mounts", "NetworkSettings"} {
			if value, found := object[key]; found {
				selected[key] = value
			}
		}
	case "pod":
		for _, key := range []string{"Id", "Name", "InfraContainerID", "Containers"} {
			if value, found := object[key]; found {
				selected[key] = value
			}
		}
	case "network":
		for _, key := range []string{"id", "name", "subnets", "containers", "ipv6_enabled", "internal"} {
			if value, found := object[key]; found {
				selected[key] = value
			}
		}
	case "volume":
		for _, key := range []string{"Name", "Mountpoint", "MountCount", "NeedsChown", "Options"} {
			if value, found := object[key]; found {
				selected[key] = value
			}
		}
	}
	if len(selected) == 0 {
		return []string{"(no relationship data)"}
	}
	encoded, _ := json.MarshalIndent(selected, "", "  ")
	return strings.Split(string(encoded), "\n")
}

func (a *App) applyDetail(result detailResult) {
	item := a.current()
	globalDetail := result.mode == "system" || result.mode == "events"
	if (!globalDetail && (item == nil || item.ID != result.itemID)) || a.DetailMode != result.mode || (result.requestID != 0 && result.requestID != a.DetailRequestID) {
		return
	}
	if result.err != nil {
		a.Status = result.err.Error()
		return
	}
	if result.health != "" {
		item.Health = result.health
	}
	if result.statsHistory != nil {
		a.StatsHistory[result.itemID] = result.statsHistory
	}
	a.DetailRawLines = append([]string(nil), result.lines...)
	a.DetailLines = append([]string(nil), result.lines...)
	a.DetailMode = result.mode
	a.reflowDetail(a.DetailViewWidth)
	if result.mode == "logs" {
		maximum := max(0, len(a.DetailLines)-max(1, a.DetailViewRows))
		if a.LogsFollow {
			a.DetailScroll = maximum
		} else {
			a.DetailScroll = min(a.DetailScroll, maximum)
		}
	} else {
		a.DetailScroll = 0
	}
	if result.status != "" {
		a.Status = result.status
	}
}

func (a *App) reflowDetail(width int) {
	if width > 0 {
		a.DetailViewWidth = width
	}
	if a.DetailRawLines == nil {
		return
	}
	lines := append([]string(nil), a.DetailRawLines...)
	if a.DetailMode == "logs" && a.DetailViewWidth > 0 {
		lines = wrapLines(lines, a.DetailViewWidth)
	}
	if a.detailNeedsBottomSpacer() {
		lines = append(lines, make([]string, logsBottomSpacer)...)
	}
	a.DetailLines = lines
	maximum := max(0, len(a.DetailLines)-max(1, a.DetailViewRows))
	if a.DetailMode == "logs" && a.LogsFollow {
		a.DetailScroll = maximum
	} else {
		a.DetailScroll = min(a.DetailScroll, maximum)
	}
}

func (a *App) detailNeedsBottomSpacer() bool {
	if a.DetailMode == "logs" || a.DetailMode == "system" {
		return true
	}
	if a.DetailMode == "config" {
		item := a.current()
		return item != nil && item.Kind == "container"
	}
	return false
}

func wrapLines(lines []string, width int) []string {
	if width <= 0 {
		return append([]string(nil), lines...)
	}
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) == 0 {
			wrapped = append(wrapped, "")
			continue
		}
		for len(runes) > width {
			wrapped = append(wrapped, string(runes[:width]))
			runes = runes[width:]
		}
		wrapped = append(wrapped, string(runes))
	}
	return wrapped
}

func isRunning(state string) bool {
	return strings.ToLower(state) == "running" || strings.ToLower(state) == "paused"
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

func markLogTimestamps(lines []string) []string {
	marked := make([]string, 0, len(lines))
	for _, line := range lines {
		if isLogTimestamp(line) {
			marked = append(marked, "> "+line)
		} else {
			marked = append(marked, line)
		}
	}
	return marked
}

func isLogTimestamp(line string) bool {
	if len(line) < len("2006-01-02T15:04:05") {
		return false
	}
	_, err := time.Parse("2006-01-02T15:04:05", line[:len("2006-01-02T15:04:05")])
	return err == nil
}

func (a *App) detailTabs() []string {
	if a.DetailMode == "system" || a.DetailMode == "events" || a.DetailMode == "exec" || a.DetailMode == "shell" || a.DetailMode == "pull" || a.DetailMode == "create" {
		return []string{a.DetailMode}
	}
	item := a.current()
	if item != nil && item.Kind == "container" {
		return []string{"summary", "logs", "stats", "env", "config", "relationships", "top"}
	}
	if item != nil && item.Kind == "pod" {
		return []string{"summary", "stats", "relationships", "config"}
	}
	if item != nil && item.Kind == "image" {
		return []string{"summary", "history", "config"}
	}
	if item != nil && item.Kind == "network" {
		return []string{"summary", "relationships", "config"}
	}
	if item != nil && item.Kind == "volume" {
		return []string{"summary", "relationships", "config"}
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
	a.DetailMode = next
	if next == "logs" {
		a.LogsFollow = true
	}
}

func (a *App) move(delta int) {
	items := a.Items[a.Mode]
	if len(items) == 0 {
		return
	}
	a.Selected[a.Mode] = max(0, min(len(items)-1, a.Selected[a.Mode]+delta))
	a.DetailMode = "summary"
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
}
func (a *App) toggleMode(mode string) {
	if a.Mode == mode {
		a.FocusMain = false
		return
	}
	a.Mode = mode
	a.FocusMain = false
	a.DetailMode = "summary"
}

func (a *App) menuEntries() [][2]string {
	item := a.current()
	if item == nil {
		return append(actionsForMode(a.Mode), panelUtilityActions()...)
	}
	if item.Kind == "container" {
		pause, label := "pause", "Pause"
		if strings.ToLower(item.State) == "paused" {
			pause, label = "unpause", "Unpause"
		}
		entries := [][2]string{{"Start [S]", "start"}, {"Stop [s]", "stop"}, {"Restart [r]", "restart"}, {label + " [p]", pause}, {"Kill [K]", "kill"}, {"Logs [m]", "logs"}, {"Attach [a]", "attach"}, {"Stats [t]", "stats"}, {"Environment", "env"}, {"Config [i]", "config"}, {"Relationships", "relationships"}, {"Top", "top"}, {"Exec shell [E]", "shell"}, {"Exec command", "exec"}, {"Copy to container", "copy_to"}, {"Copy from container", "copy_from"}, {"Hide stopped [e]", "hide_stopped"}, {"Remove [d]", "remove"}, {"Create container [C]", "run"}}
		return append(entries, panelUtilityActions()...)
	}
	if item.Kind == "pod" {
		entries := [][2]string{{"Start [S]", "start"}, {"Stop [s]", "stop"}, {"Restart [r]", "restart"}, {"Pause [p]", "pause"}, {"Unpause [p]", "unpause"}, {"Kill [K]", "kill"}, {"Stats [t]", "stats"}, {"Config [i]", "config"}, {"Relationships", "relationships"}, {"Remove [d]", "remove"}, {"Create pod", "create_pod"}}
		return append(entries, panelUtilityActions()...)
	}
	if item.Kind == "network" {
		entries := [][2]string{{"Config [i]", "config"}, {"Connect container", "network_connect"}, {"Disconnect container", "network_disconnect"}, {"Remove [d]", "remove"}, {"Create network", "create_network"}}
		return append(entries, panelUtilityActions()...)
	}
	if item.Kind == "volume" {
		entries := [][2]string{{"Config [i]", "config"}, {"Relationships", "relationships"}, {"Mount volume", "volume_mount"}, {"Unmount volume", "volume_unmount"}, {"Remove [d]", "remove"}, {"Create volume", "create_volume"}}
		return append(entries, panelUtilityActions()...)
	}
	if item.Kind == "secret" {
		entries := [][2]string{{"Config [i]", "config"}, {"Remove [d]", "remove"}, {"Create secret", "create_secret"}}
		return append(entries, panelUtilityActions()...)
	}
	if item.Kind == "image" {
		entries := [][2]string{{"Config [i]", "config"}, {"Tag image", "image_tag"}, {"Untag image", "image_untag"}, {"Save image", "image_save"}, {"Search registry", "image_search"}, {"Pull image [P]", "pull"}, {"Build image [B]", "build"}, {"Push image [U]", "push"}, {"Load image", "image_load"}, {"Import image", "image_import"}, {"Registry login", "registry_login"}, {"Registry logout", "registry_logout"}, {"Prune images", "prune_images"}, {"Remove [d]", "remove"}}
		return append(entries, panelUtilityActions()...)
	}
	return append([][2]string{{"Config [i]", "config"}, {"Remove [d]", "remove"}}, panelUtilityActions()...)
}

func actionsForMode(mode string) [][2]string {
	switch mode {
	case "containers":
		return [][2]string{{"Create container [C]", "run"}}
	case "pods":
		return [][2]string{{"Create pod", "create_pod"}}
	case "images":
		return [][2]string{{"Pull image [P]", "pull"}, {"Build image [B]", "build"}, {"Push image [U]", "push"}, {"Load image", "image_load"}, {"Import image", "image_import"}, {"Registry login", "registry_login"}, {"Registry logout", "registry_logout"}, {"Search registry", "image_search"}, {"Prune images", "prune_images"}}
	case "volumes":
		return [][2]string{{"Create volume", "create_volume"}, {"Prune volumes", "prune_volumes"}}
	case "networks":
		return [][2]string{{"Create network", "create_network"}, {"Prune networks", "prune_networks"}}
	case "secrets":
		return [][2]string{{"Create secret", "create_secret"}}
	default:
		return nil
	}
}

func panelUtilityActions() [][2]string {
	return [][2]string{{"Refresh", "refresh"}, {"System info", "system_info"}, {"Events", "events"}, {"System prune", "system_prune"}}
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

func (a *App) toggleHideStopped() {
	a.HideStopped = !a.HideStopped
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

func (a *App) scroll(delta int) {
	viewRows := a.DetailViewRows
	if viewRows < 1 {
		viewRows = 1
	}
	maximum := max(0, len(a.DetailLines)-viewRows)
	current := a.DetailScroll
	current = max(0, min(maximum, current+delta))
	a.setDetailScroll(current)
	if a.DetailMode == "logs" {
		a.LogsFollow = current >= maximum
	}
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

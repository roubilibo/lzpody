package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
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

type textRegion struct {
	start, end int
	style      string
}

func addRegion(regions [][]textRegion, row, start, end int, style string) {
	if row < 0 || row >= len(regions) || start >= end || style == "" {
		return
	}
	regions[row] = append(regions[row], textRegion{start: start, end: end, style: style})
}

func styleLine(line, base string, regions []textRegion) string {
	runes := []rune(line)
	if len(runes) == 0 {
		return base + "\x1b[0m"
	}
	styles := make([]string, len(runes))
	for index := range styles {
		styles[index] = base
	}
	for _, region := range regions {
		start := max(0, region.start)
		end := min(len(runes), region.end)
		for index := start; index < end; index++ {
			styles[index] = region.style
		}
	}
	var output strings.Builder
	start := 0
	for start < len(runes) {
		style := styles[start]
		end := start + 1
		for end < len(runes) && styles[end] == style {
			end++
		}
		output.WriteString(style)
		output.WriteString(string(runes[start:end]))
		output.WriteString("\x1b[0m")
		start = end
	}
	return output.String()
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

type PodmanError struct{ Message string }

func (e *PodmanError) Error() string { return e.Message }

type PodmanClient struct {
	SocketPath string
	APIRoot    string
	HTTP       *http.Client
}

func NewPodmanClient(socketPath string) *PodmanClient {
	if socketPath == "" {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir == "" {
			runtimeDir = fmt.Sprintf("/run/user/%d", os.Getuid())
		}
		socketPath = os.Getenv("LZPODY_SOCKET")
		if socketPath == "" {
			socketPath = os.Getenv("PODMAN_TUI_SOCKET")
		}
		if socketPath == "" {
			socketPath = filepath.Join(runtimeDir, "podman/podman.sock")
		}
	}
	version := os.Getenv("LZPODY_API_VERSION")
	if version == "" {
		version = os.Getenv("PODMAN_TUI_API_VERSION")
	}
	if version == "" {
		version = defaultAPIVersion
	}
	return &PodmanClient{
		SocketPath: socketPath,
		APIRoot:    "/" + strings.Trim(version, "/") + "/libpod",
		HTTP: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (c *PodmanClient) request(method, path string, query url.Values, body io.Reader) (any, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, "http://podman"+path, body)
	if err != nil {
		return nil, &PodmanError{Message: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
			return nil, &PodmanError{Message: fmt.Sprintf("Podman socket not found: %s. Recover with: systemctl --user restart podman.socket", c.SocketPath)}
		}
		return nil, &PodmanError{Message: "Cannot connect to Podman: " + err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &PodmanError{Message: "Cannot read Podman response: " + err.Error()}
	}
	if resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(raw))
		if len(message) > 240 {
			message = message[:237] + "..."
		}
		if message == "" {
			message = resp.Status
		}
		return nil, &PodmanError{Message: fmt.Sprintf("Podman API %d: %s", resp.StatusCode, message)}
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	return string(raw), nil
}

func (c *PodmanClient) get(path string, query url.Values) (any, error) {
	return c.request(http.MethodGet, path, query, nil)
}

func (c *PodmanClient) post(path string, query url.Values) error {
	_, err := c.request(http.MethodPost, path, query, nil)
	return err
}

func (c *PodmanClient) del(path string, query url.Values) error {
	_, err := c.request(http.MethodDelete, path, query, nil)
	return err
}

func listResult(value any) []map[string]any {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(list))
	for _, entry := range list {
		if item, ok := entry.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result
}

func (c *PodmanClient) containers() ([]map[string]any, error) {
	v, err := c.get(c.APIRoot+"/containers/json", url.Values{"all": {"true"}})
	return listResult(v), err
}
func (c *PodmanClient) pods() ([]map[string]any, error) {
	v, err := c.get(c.APIRoot+"/pods/json", nil)
	return listResult(v), err
}
func (c *PodmanClient) images() ([]map[string]any, error) {
	v, err := c.get(c.APIRoot+"/images/json", nil)
	return listResult(v), err
}
func (c *PodmanClient) volumes() ([]map[string]any, error) {
	v, err := c.get(c.APIRoot+"/volumes/json", nil)
	if wrapper, ok := v.(map[string]any); ok {
		v = wrapper["Volumes"]
	}
	return listResult(v), err
}
func (c *PodmanClient) networks() ([]map[string]any, error) {
	v, err := c.get(c.APIRoot+"/networks/json", nil)
	return listResult(v), err
}

func idPath(base, id string) string { return base + "/" + url.PathEscape(id) }

func (c *PodmanClient) inspect(kind, id string) (any, error) {
	return c.get(idPath(c.APIRoot+"/"+kind, id)+"/json", nil)
}
func (c *PodmanClient) logs(id string) (string, error) {
	v, err := c.get(idPath(c.APIRoot+"/containers", id)+"/logs", url.Values{
		"stdout": {"true"}, "stderr": {"true"}, "timestamps": {"true"}, "tail": {"200"},
	})
	if err != nil {
		return "", err
	}
	return valueText(v), nil
}
func (c *PodmanClient) stats(id string) (any, error) {
	return c.get(c.APIRoot+"/containers/stats", url.Values{"containers": {id}, "stream": {"false"}})
}
func (c *PodmanClient) podStats(id string) (any, error) {
	return c.get(c.APIRoot+"/pods/stats", url.Values{"namesOrIDs": {id}, "all": {"true"}, "stream": {"false"}})
}
func (c *PodmanClient) top(id string) (any, error) {
	return c.get(idPath(c.APIRoot+"/containers", id)+"/top", nil)
}

func lifecycleQuery(action string) url.Values {
	if action == "kill" {
		return url.Values{"signal": {"SIGKILL"}}
	}
	if action == "stop" || action == "restart" {
		return url.Values{"timeout": {"10"}}
	}
	return nil
}

func (c *PodmanClient) resourceAction(kind, id, action string) error {
	base := c.APIRoot + "/" + kind + "/" + url.PathEscape(id)
	if action == "remove" {
		return c.del(base, url.Values{"force": {"true"}})
	}
	allowed := map[string]bool{"start": true, "stop": true, "restart": true, "pause": true, "unpause": true, "kill": true}
	if !allowed[action] {
		return fmt.Errorf("unsupported %s action: %s", kind, action)
	}
	return c.post(base+"/"+action, lifecycleQuery(action))
}

func (c *PodmanClient) remove(kind, id string) error {
	return c.del(idPath(c.APIRoot+"/"+kind, id), url.Values{"force": {"true"}})
}

type Item struct {
	Kind, ID, Name, State, Image, Status, Health string
	CPU                                          float64
	Ports                                        []string
	Details                                      map[string]any
}

func valueText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		encoded, _ := json.MarshalIndent(v, "", "  ")
		return string(encoded)
	}
}

func firstName(value any) string {
	if values, ok := value.([]any); ok && len(values) > 0 {
		return fmt.Sprint(values[0])
	}
	return valueText(value)
}

func numberValue(value any) float64 {
	if text, ok := value.(string); ok {
		text = strings.TrimSuffix(strings.TrimSpace(text), "%")
		value = text
	}
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case json.Number:
		result, _ := v.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(v, 64)
		return result
	default:
		return 0
	}
}

func sizeText(value any) string {
	size := numberValue(value)
	if size == 0 && value == nil {
		return ""
	}
	for _, unit := range []string{"B", "KiB", "MiB", "GiB", "TiB"} {
		if size < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", size, unit)
		}
		size /= 1024
	}
	return ""
}

func bytesText(value any) string {
	result := sizeText(value)
	if result == "" {
		return "0 B"
	}
	return result
}

func healthStatus(raw map[string]any, fallback string) string {
	if raw == nil {
		return fallback
	}
	candidates := []any{raw["Health"], raw["Healthcheck"]}
	if state, ok := raw["State"].(map[string]any); ok {
		candidates = append(candidates, state["Health"], state["Healthcheck"], state["HealthStatus"])
	}
	for _, candidate := range candidates {
		if text, ok := candidate.(string); ok && strings.TrimSpace(text) != "" {
			return strings.ToLower(strings.TrimSpace(text))
		}
		if object, ok := candidate.(map[string]any); ok {
			if status := object["Status"]; status != nil {
				return strings.ToLower(strings.TrimSpace(fmt.Sprint(status)))
			}
			if status := object["status"]; status != nil {
				return strings.ToLower(strings.TrimSpace(fmt.Sprint(status)))
			}
		}
	}
	if config, ok := raw["Config"].(map[string]any); ok {
		if _, present := config["Healthcheck"]; present {
			if config["Healthcheck"] == nil {
				return "no healthcheck"
			}
			return "not started"
		}
	}
	return fallback
}

func portMappings(raw map[string]any) []string {
	values := raw["Ports"]
	if values == nil {
		values = raw["PortMappings"]
	}
	if object, ok := values.(map[string]any); ok {
		values = []any{object}
	}
	list, ok := values.([]any)
	if !ok {
		return nil
	}
	result := []string{}
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			if text := strings.TrimSpace(valueText(entry)); text != "" {
				result = append(result, text)
			}
			continue
		}
		containerPort := object["container_port"]
		if containerPort == nil {
			containerPort = object["containerPort"]
		}
		if containerPort == nil {
			containerPort = object["container_port_num"]
		}
		hostPort := object["host_port"]
		if hostPort == nil {
			hostPort = object["hostPort"]
		}
		protocol := fmt.Sprint(object["protocol"])
		if protocol == "<nil>" || protocol == "" {
			protocol = "tcp"
		}
		hostIP := fmt.Sprint(object["host_ip"])
		if hostIP == "<nil>" || hostIP == "" {
			hostIP = fmt.Sprint(object["hostIP"])
		}
		if hostIP == "<nil>" || hostIP == "" {
			hostIP = "0.0.0.0"
		}
		if hostPort != nil && numberValue(hostPort) != 0 {
			result = append(result, fmt.Sprintf("%s:%s->%s/%s", hostIP, scalarText(hostPort), scalarText(containerPort), protocol))
		} else if containerPort != nil {
			result = append(result, fmt.Sprintf("%s/%s", scalarText(containerPort), protocol))
		}
	}
	return result
}

func scalarText(value any) string {
	if value == nil {
		return ""
	}
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return fmt.Sprint(value)
}

func containerItem(raw map[string]any) Item {
	id := scalarText(raw["Id"])
	if id == "" {
		id = scalarText(raw["ID"])
	}
	name := strings.TrimPrefix(firstName(raw["Names"]), "/")
	if name == "" {
		name = scalarText(raw["Name"])
	}
	if name == "" {
		name = shortID(id)
	}
	return Item{Kind: "container", ID: id, Name: name, State: defaultText(raw["State"], "unknown"), Image: scalarText(raw["Image"]), Status: scalarText(raw["Status"]), Health: healthStatus(raw, "unknown"), Ports: portMappings(raw)}
}

func podItem(raw map[string]any) Item {
	id := scalarText(raw["Id"])
	if id == "" {
		id = scalarText(raw["ID"])
	}
	count := 0
	if containers, ok := raw["Containers"].([]any); ok {
		count = len(containers)
	}
	return Item{Kind: "pod", ID: id, Name: defaultText(raw["Name"], shortID(id)), State: defaultText(raw["Status"], "unknown"), Status: fmt.Sprintf("%d containers", count)}
}

func imageItem(raw map[string]any) Item {
	id := defaultText(raw["Id"], defaultText(raw["ID"], ""))
	name := firstName(raw["RepoTags"])
	if name == "" {
		name = shortID(id)
	}
	return Item{Kind: "image", ID: id, Name: name, State: "image", Status: sizeText(raw["Size"]), Details: raw}
}

func volumeItem(raw map[string]any) Item {
	name := scalarText(raw["Name"])
	return Item{Kind: "volume", ID: name, Name: name, State: "volume", Status: defaultText(raw["Driver"], "local"), Details: raw}
}

func networkItem(raw map[string]any) Item {
	name := defaultText(raw["Name"], raw["name"])
	driver := defaultText(raw["Driver"], raw["driver"])
	if driver == "" {
		driver = "bridge"
	}
	return Item{Kind: "network", ID: name, Name: name, State: "network", Status: driver, Details: raw}
}

func defaultText(value any, fallback any) string {
	text := scalarText(value)
	if text == "" || text == "<nil>" {
		return scalarText(fallback)
	}
	return text
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func containerStateLabel(item Item) string {
	state := strings.ToLower(item.State)
	if state == "exited" && strings.HasPrefix(item.Status, "Exited (") {
		if end := strings.Index(item.Status, ")"); end >= 0 {
			return strings.ToLower(item.Status[:end+1])
		}
	}
	return item.State
}

func statsPayload(value any) map[string]any {
	if list, ok := value.([]any); ok {
		if len(list) == 0 {
			return nil
		}
		value = list[0]
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if list, ok := object["Stats"].([]any); ok {
		if len(list) == 0 {
			return nil
		}
		object, _ = list[0].(map[string]any)
	}
	return object
}

var sparkChars = []rune("▁▂▃▄▅▆▇█")

func gauge(value float64, width int) string {
	if width <= 0 {
		return ""
	}
	ratio := value / 100
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio*float64(width) + 0.5)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func sparkline(values []float64, width int) string {
	if len(values) == 0 {
		return "(no samples)"
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	low, high := values[0], values[0]
	for _, value := range values[1:] {
		if value < low {
			low = value
		}
		if value > high {
			high = value
		}
	}
	if high == low {
		index := 0
		if high > 0 {
			index = len(sparkChars) / 2
		}
		return strings.Repeat(string(sparkChars[index]), len(values))
	}
	var result strings.Builder
	for _, value := range values {
		index := int(((value-low)/(high-low))*float64(len(sparkChars)-1) + 0.5)
		result.WriteRune(sparkChars[index])
	}
	return result.String()
}

func statsLines(history []map[string]any) []string {
	if len(history) == 0 {
		return []string{"No statistics available."}
	}
	valueFor := func(sample map[string]any, key, fallback string) float64 {
		value := sample[key]
		if value == nil {
			value = sample[fallback]
		}
		return numberValue(value)
	}
	latest := history[len(history)-1]
	cpuValues, memoryValues := []float64{}, []float64{}
	for _, sample := range history {
		cpuValues = append(cpuValues, valueFor(sample, "CPU", "AvgCPU"))
		memoryValues = append(memoryValues, valueFor(sample, "MemPerc", ""))
	}
	cpu, memory := cpuValues[len(cpuValues)-1], memoryValues[len(memoryValues)-1]
	return []string{
		"Live container statistics", "",
		fmt.Sprintf("CPU       %6.2f%%  [%s]", cpu, gauge(cpu, 20)),
		"          " + sparkline(cpuValues, 32),
		fmt.Sprintf("Memory    %6.2f%%  [%s]", memory, gauge(memory, 20)),
		"          " + sparkline(memoryValues, 32),
		fmt.Sprintf("Usage     %s / %s", bytesText(latest["MemUsage"]), bytesText(latest["MemLimit"])),
		fmt.Sprintf("PIDs      %d", int(numberValue(latest["PIDs"]))),
		"Block I/O in   " + bytesText(latest["BlockInput"]),
		"Block I/O out  " + bytesText(latest["BlockOutput"]), "",
		fmt.Sprintf("Samples: %d  ·  refresh interval: %gs", len(history), refreshInterval.Seconds()),
	}
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
}

func NewApp(client *PodmanClient) *App {
	items, selected := map[string][]Item{}, map[string]int{}
	for _, mode := range resourceModes {
		items[mode] = []Item{}
		selected[mode] = 0
	}
	return &App{Client: client, Mode: "containers", Items: items, Selected: selected, DetailMode: "summary", StatsHistory: map[string][]map[string]any{}, Status: "Connecting to rootless Podman..."}
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
	}
	a.Status = "Logs: " + item.Name
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
	}
	a.Status = "Stats: " + item.Name
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
	}
	if len(lines) == 0 {
		lines = []string{"(no processes)"}
	}
	a.DetailLines = lines
	a.DetailMode = "top"
	if len(silent) == 0 || !silent[0] {
		a.DetailScroll = 0
	}
	a.Status = "Top: " + item.Name
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
	a.Status = strings.Title(action) + " " + name + ": OK"
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

type Terminal struct{ state string }

func (t *Terminal) enter() error {
	if !isTTY() {
		return fmt.Errorf("lzpody requires an interactive terminal")
	}
	state, err := runOutput("stty", "-g")
	if err != nil {
		return fmt.Errorf("cannot configure terminal: %w", err)
	}
	t.state = strings.TrimSpace(state)
	if _, err := runOutput("stty", "raw", "-echo", "min", "0", "time", "2"); err != nil {
		return err
	}
	fmt.Print("\x1b[?25l")
	return nil
}
func (t *Terminal) restore() {
	if t.state != "" {
		_, _ = runOutput("stty", t.state)
		t.state = ""
	}
	// Leave the shell on a clean screen after curses-like rendering ends.
	fmt.Print("\x1b[0m\x1b[?25h\x1b[2J\x1b[H")
}
func (t *Terminal) suspend()      { t.restore() }
func (t *Terminal) resume() error { return t.enter() }
func runOutput(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Stdin = os.Stdin
	output, err := command.Output()
	return string(output), err
}
func isTTY() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalSize() (int, int) {
	type windowSize struct {
		rows, columns, horizontal, vertical uint16
	}
	var size windowSize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size))); errno == 0 && size.columns > 0 && size.rows > 0 {
		return int(size.columns), int(size.rows)
	}
	columns, _ := strconv.Atoi(os.Getenv("COLUMNS"))
	rows, _ := strconv.Atoi(os.Getenv("LINES"))
	if columns < 70 {
		columns = 100
	}
	if rows < 22 {
		rows = 30
	}
	return columns, rows
}

func clip(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) > width {
		return string(runes[:width])
	}
	return text + strings.Repeat(" ", width-len(runes))
}
func putLine(lines []string, row, column, width int, text string) {
	if row < 0 || row >= len(lines) || column >= len([]rune(lines[row])) || width <= 0 {
		return
	}
	prefix := []rune(lines[row])
	value := []rune(clip(text, width))
	for i, char := range value {
		index := column + i
		if index >= len(prefix) {
			break
		}
		prefix[index] = char
	}
	lines[row] = string(prefix)
}
func boxLine(width int, left, right rune) string {
	if width < 2 {
		return strings.Repeat("─", width)
	}
	return string(left) + strings.Repeat("─", width-2) + string(right)
}

func (a *App) render() {
	width, height := terminalSize()
	uiTheme := loadUITheme()
	lines := make([]string, height)
	styles := make([]string, height)
	regions := make([][]textRegion, height)
	for i := range lines {
		lines[i] = strings.Repeat(" ", width)
		styles[i] = uiTheme.Normal
	}
	if height < 22 || width < 70 {
		lines[0] = clip("Terminal too small. Minimum size is 70x22.", width)
		printFrame(lines, styles, regions)
		return
	}
	putLine(lines, 0, 0, width, " lzpody ")
	putLine(lines, 0, 14, width-14, "native Libpod · "+uiTheme.Name)
	addRegion(regions, 0, 0, 9, uiTheme.Title)
	addRegion(regions, 0, 14, width, uiTheme.Muted)
	styles[0] = uiTheme.Normal
	putLine(lines, 1, width-27, 25, "[F5] refresh  [q] quit")
	styles[1] = uiTheme.Muted
	if a.Filter != "" {
		putLine(lines, 1, 1, width/2, "Filter: "+a.Filter)
	}
	top, bottom := 2, height-3
	leftWidth := max(32, width/3)
	rightX := leftWidth + 2
	rightWidth := width - rightX - 1
	leftHeight := bottom - top + 1
	panelHeight := leftHeight / len(resourceModes)
	for index, mode := range resourceModes {
		panelTop := top + index*panelHeight
		panelH := panelHeight
		if index == len(resourceModes)-1 {
			panelH = bottom - panelTop + 1
		}
		panelStyle := uiTheme.Border
		if mode == a.Mode && !a.FocusMain {
			panelStyle = uiTheme.Title
		}
		addRegion(regions, panelTop, 1, leftWidth+1, panelStyle)
		putLine(lines, panelTop, 1, leftWidth, boxLine(leftWidth, '┌', '┐'))
		putLine(lines, panelTop, 3, leftWidth-4, fmt.Sprintf("[%d] %s (%d)", index+1, resourceLabels[mode], len(a.Items[mode])))
		styles[panelTop] = panelStyle
		for r := 1; r < panelH-1; r++ {
			putLine(lines, panelTop+r, 1, leftWidth, "│")
			putLine(lines, panelTop+r, leftWidth, 1, "│")
			addRegion(regions, panelTop+r, 1, leftWidth+1, panelStyle)
			styles[panelTop+r] = panelStyle
		}
		putLine(lines, panelTop+panelH-1, 1, leftWidth, boxLine(leftWidth, '└', '┘'))
		addRegion(regions, panelTop+panelH-1, 1, leftWidth+1, panelStyle)
		styles[panelTop+panelH-1] = panelStyle
		visible := max(1, panelH-2)
		selected := a.Selected[mode]
		start := max(0, min(selected-visible+1, len(a.Items[mode])-visible))
		for row, item := range a.Items[mode][start:min(start+visible, len(a.Items[mode]))] {
			marker := "○"
			if item.Kind == "image" {
				marker = "◆"
			} else if item.Kind == "volume" || item.Kind == "network" {
				marker = "◇"
			} else if strings.EqualFold(item.State, "running") || strings.EqualFold(item.State, "running (healthy)") {
				marker = "●"
			}
			label := marker + " " + item.Name + "  " + item.State
			if item.Kind == "container" {
				label = marker + " " + item.Name + "  " + containerStateLabel(item) + fmt.Sprintf("  %5.2f%%", item.CPU)
			}
			if item.Kind == "image" {
				imageName := item.Name
				if slash := strings.LastIndex(imageName, "/"); slash >= 0 {
					imageName = imageName[slash+1:]
				}
				label = marker + " " + imageName + "  " + defaultText(item.Status, "unknown size")
			}
			if start+row == selected && mode == a.Mode && !a.FocusMain {
				label = "> " + label
			}
			putLine(lines, panelTop+1+row, 2, leftWidth-3, label)
			itemStyle := uiTheme.Stopped
			if strings.ToLower(item.State) == "running" {
				itemStyle = uiTheme.Running
			}
			if start+row == selected && mode == a.Mode && !a.FocusMain {
				itemStyle = uiTheme.Selected
			}
			addRegion(regions, panelTop+1+row, 1, leftWidth+1, itemStyle)
			styles[panelTop+1+row] = itemStyle
		}
		if len(a.Items[mode]) == 0 && panelH >= 4 {
			putLine(lines, panelTop+1, 3, leftWidth-4, "(empty)")
			addRegion(regions, panelTop+1, 1, leftWidth+1, uiTheme.Muted)
		}
	}
	for row := top; row <= bottom; row++ {
		addRegion(regions, row, rightX, rightX+rightWidth, uiTheme.Border)
	}
	putLine(lines, top, rightX, rightWidth, boxLine(rightWidth, '┌', '┐'))
	mainStyle := uiTheme.Border
	if a.FocusMain {
		mainStyle = uiTheme.Title
	}
	styles[top] = mainStyle
	for r := 1; r < bottom-top; r++ {
		putLine(lines, top+r, rightX, 1, "│")
		putLine(lines, top+r, rightX+rightWidth-1, 1, "│")
		styles[top+r] = mainStyle
	}
	putLine(lines, bottom, rightX, rightWidth, boxLine(rightWidth, '└', '┘'))
	styles[bottom] = mainStyle
	item := a.current()
	title := "Details"
	if item != nil {
		title = item.Name
	}
	position := ""
	if len(a.DetailLines) > 0 {
		first := a.DetailScroll + 1
		last := min(len(a.DetailLines), a.DetailScroll+max(1, bottom-top-2))
		position = fmt.Sprintf("  (%d-%d/%d)", first, last, len(a.DetailLines))
	}
	putLine(lines, top, rightX+2, rightWidth-4, title+" ["+a.DetailMode+"]"+position)
	addRegion(regions, top, rightX, rightX+rightWidth, uiTheme.Title)
	styles[top] = uiTheme.Title
	tabs := []string{}
	for _, tab := range a.detailTabs() {
		label := strings.Title(tab)
		if tab == a.DetailMode {
			tabs = append(tabs, "["+label+"]")
		} else {
			tabs = append(tabs, label)
		}
	}
	putLine(lines, top+1, rightX+2, rightWidth-4, strings.Join(tabs, "  "))
	addRegion(regions, top+1, rightX, rightX+rightWidth, uiTheme.Key)
	styles[top+1] = uiTheme.Key
	detailRows := bottom - top - 2
	a.DetailViewRows = max(1, detailRows)
	start := max(0, min(a.DetailScroll, max(0, len(a.DetailLines)-detailRows)))
	for index, line := range a.DetailLines[start:min(start+detailRows, len(a.DetailLines))] {
		putLine(lines, top+2+index, rightX+2, rightWidth-4, line)
		addRegion(regions, top+2+index, rightX, rightX+rightWidth, uiTheme.Normal)
		styles[top+2+index] = uiTheme.Normal
	}
	putLine(lines, height-2, 1, width-2, a.Status)
	statusLower := strings.ToLower(a.Status)
	if strings.Contains(statusLower, "error") || strings.Contains(statusLower, "cannot") || strings.Contains(statusLower, "not found") || strings.Contains(statusLower, "invalid") || strings.Contains(statusLower, "api 4") || strings.Contains(statusLower, "api 5") {
		styles[height-2] = uiTheme.Error
	} else {
		styles[height-2] = uiTheme.Muted
	}
	footer := "←/→ h/l panels  ↑/↓ j/k items  Tab  1-5 focus  Enter main  [/] tabs  x/? menu  / filter  q quit"
	if a.FocusMain {
		footer = "↑/↓ j/k scroll  PgUp/PgDn Ctrl-U/D  Home/End  [/] tabs  Esc panels  x/? menu  q quit"
	}
	if a.MenuOpen {
		footer = "↑/↓ j/k select  Enter choose  Esc close menu"
	}
	putLine(lines, height-1, 1, width-2, footer)
	styles[height-1] = uiTheme.Key
	if a.MenuOpen {
		entries := a.menuEntries()
		boxWidth := min(42, max(24, width-6))
		boxHeight := min(len(entries)+2, max(5, height-4))
		menuTop := max(1, (height-boxHeight)/2)
		menuLeft := max(2, (width-boxWidth)/2)
		menuStyle := uiTheme.Title
		putLine(lines, menuTop, menuLeft, boxWidth, boxLine(boxWidth, '┌', '┐'))
		addRegion(regions, menuTop, menuLeft, menuLeft+boxWidth, menuStyle)
		styles[menuTop] = menuStyle
		for row := 1; row < boxHeight-1; row++ {
			putLine(lines, menuTop+row, menuLeft, boxWidth, "│"+strings.Repeat(" ", boxWidth-2)+"│")
			addRegion(regions, menuTop+row, menuLeft, menuLeft+boxWidth, uiTheme.Normal)
			styles[menuTop+row] = uiTheme.Normal
		}
		putLine(lines, menuTop+boxHeight-1, menuLeft, boxWidth, boxLine(boxWidth, '└', '┘'))
		addRegion(regions, menuTop+boxHeight-1, menuLeft, menuLeft+boxWidth, menuStyle)
		styles[menuTop+boxHeight-1] = menuStyle
		putLine(lines, menuTop, menuLeft+2, boxWidth-4, "Actions")
		styles[menuTop] = menuStyle
		visible := max(1, boxHeight-2)
		start := max(0, min(a.MenuIndex-visible+1, len(entries)-visible))
		for index, entry := range entries[start:min(start+visible, len(entries))] {
			row := menuTop + 1 + index
			putLine(lines, row, menuLeft+2, boxWidth-4, entry[0])
			if start+index == a.MenuIndex {
				styles[row] = uiTheme.Selected
				addRegion(regions, row, menuLeft+2, menuLeft+boxWidth-2, uiTheme.Selected)
			} else {
				styles[row] = uiTheme.Normal
				addRegion(regions, row, menuLeft+2, menuLeft+boxWidth-2, uiTheme.Normal)
			}
		}
	}
	printFrame(lines, styles, regions)
}

func printFrame(lines, styles []string, regions [][]textRegion) {
	var frame strings.Builder
	for index, line := range lines {
		base := ""
		if index < len(styles) {
			base = styles[index]
		}
		var rowRegions []textRegion
		if index < len(regions) {
			rowRegions = regions[index]
		}
		frame.WriteString(styleLine(line, base, rowRegions))
		if index < len(lines)-1 {
			frame.WriteString("\r\n")
		}
	}
	fmt.Print("\x1b[H\x1b[2J" + frame.String())
}

func themeName() string {
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".local/state/omarchy/current/theme.name"))
	if err != nil {
		return "Omarchy"
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "Omarchy"
	}
	return strings.Title(strings.ReplaceAll(value, "-", " "))
}

func readKey() (string, error) {
	var one [1]byte
	n, err := os.Stdin.Read(one[:])
	if n == 0 {
		// With VMIN=0/VTIME set, a terminal timeout may surface as a
		// zero-byte read or EOF. It is not a real end-of-input while the
		// terminal is still attached.
		if isTTY() {
			return "", nil
		}
		return "", err
	}
	if err != nil {
		return "", err
	}
	switch one[0] {
	case 'q':
		return "q", nil
	case 'y', 'Y':
		return "y", nil
	case 3:
		return "q", nil
	case 10, 13:
		return "enter", nil
	case 9:
		return "tab", nil
	case 4:
		return "pagedown", nil
	case 21:
		return "pageup", nil
	case 27:
		var first [1]byte
		n, _ := os.Stdin.Read(first[:])
		if n == 0 {
			return "esc", nil
		}
		if first[0] != '[' {
			return "esc", nil
		}
		sequence := []byte{'['}
		for len(sequence) < 8 {
			var next [1]byte
			n, _ := os.Stdin.Read(next[:])
			if n == 0 {
				break
			}
			sequence = append(sequence, next[0])
			if next[0] >= 0x40 && next[0] <= 0x7e {
				break
			}
		}
		switch string(sequence) {
		case "[A":
			return "up", nil
		case "[B":
			return "down", nil
		case "[C":
			return "right", nil
		case "[D":
			return "left", nil
		case "[H":
			return "home", nil
		case "[F":
			return "end", nil
		case "[5~":
			return "pageup", nil
		case "[6~":
			return "pagedown", nil
		case "[Z":
			return "backtab", nil
		case "[15~":
			return "refresh", nil
		}
		return "esc", nil
	case 'j':
		return "down", nil
	case 'k':
		return "up", nil
	case 'h':
		return "left", nil
	case 'l':
		return "right", nil
	case 'x', '?':
		return "menu", nil
	case ' ':
		return "space", nil
	case '/':
		return "filter", nil
	case '[':
		return "prevtab", nil
	case ']':
		return "nexttab", nil
	case 'i':
		return "config", nil
	case 't':
		return "stats", nil
	case 'm':
		return "logs", nil
	case 'a':
		return "attach", nil
	case 'E':
		return "shell", nil
	case 'e':
		return "hide", nil
	case 'S':
		return "start", nil
	case 's':
		return "stop", nil
	case 'r', 'R':
		return "restart", nil
	case 'p':
		return "pause", nil
	case 'K':
		return "kill", nil
	case 'd':
		return "remove", nil
	case '1', '2', '3', '4', '5':
		return string(one[0]), nil
	}
	return string(one[0]), nil
}

func runExternal(term *Terminal, command string, args ...string) {
	term.suspend()
	cmd := exec.Command(command, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = cmd.Run()
	_ = term.resume()
}

func (a *App) filterPrompt(term *Terminal) {
	term.suspend()
	fmt.Printf("Filter [%s]: ", a.Filter)
	reader := bufio.NewReader(os.Stdin)
	value, _ := reader.ReadString('\n')
	a.Filter = strings.TrimSpace(value)
	_ = term.resume()
	a.Selected[a.Mode] = 0
	a.refresh("")
}

func (a *App) confirm(term *Terminal, action string) bool {
	item := a.current()
	if item == nil {
		return false
	}
	term.suspend()
	fmt.Printf("%s %s? [y/N] ", strings.Title(action), item.Name)
	reader := bufio.NewReader(os.Stdin)
	value, _ := reader.ReadString('\n')
	_ = term.resume()
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "y")
}

func runTUI(client *PodmanClient) error {
	term := &Terminal{}
	if err := term.enter(); err != nil {
		return err
	}
	defer term.restore()
	app := NewApp(client)
	app.refresh("")
	for {
		if time.Since(app.LastRefresh) >= refreshInterval {
			keep := ""
			if item := app.current(); item != nil {
				keep = item.ID
			}
			app.refresh(keep)
		}
		app.render()
		key, err := readKey()
		if err != nil {
			return nil
		}
		if app.MenuOpen {
			switch key {
			case "q", "esc":
				app.MenuOpen = false
			case "up":
				app.MenuIndex = max(0, app.MenuIndex-1)
			case "down":
				app.MenuIndex = min(len(app.menuEntries())-1, app.MenuIndex+1)
			case "enter", "space", "y":
				entries := app.menuEntries()
				if len(entries) == 0 {
					continue
				}
				action := entries[app.MenuIndex][1]
				app.MenuOpen = false
				switch action {
				case "refresh":
					app.refresh("")
				case "logs":
					app.loadLogs()
					app.FocusMain = true
				case "stats":
					app.loadStats()
					app.FocusMain = true
				case "env":
					app.loadEnv()
					app.FocusMain = true
				case "config":
					app.loadInspect("config")
					app.FocusMain = true
				case "top":
					app.loadTop()
					app.FocusMain = true
				case "hide_stopped":
					app.toggleHideStopped()
				case "shell", "attach":
					if item := app.current(); item != nil {
						if action == "shell" {
							runExternal(term, "podman", "exec", "-it", item.Name, "sh")
						} else {
							runExternal(term, "podman", "attach", item.Name)
						}
						app.refresh(item.ID)
					}
				case "stop", "remove", "kill":
					if app.confirm(term, action) {
						app.Status = strings.Title(action) + "..."
						app.perform(action)
					}
				default:
					app.perform(action)
				}
			}
			continue
		}
		switch key {
		case "q":
			return nil
		case "refresh":
			keep := ""
			if item := app.current(); item != nil {
				keep = item.ID
			}
			app.refresh(keep)
		case "menu":
			app.MenuOpen = true
			app.MenuIndex = 0
		case "up":
			if app.FocusMain {
				app.scroll(-1)
			} else {
				app.move(-1)
			}
		case "down":
			if app.FocusMain {
				app.scroll(1)
			} else {
				app.move(1)
			}
		case "left":
			if !app.FocusMain {
				app.moveFocus(-1)
			}
		case "right", "tab":
			if !app.FocusMain {
				app.moveFocus(1)
			}
		case "backtab":
			if !app.FocusMain {
				app.moveFocus(-1)
			}
		case "enter":
			if !app.FocusMain {
				app.FocusMain = true
				app.DetailScroll = 0
				app.loadDetail()
			}
		case "esc":
			app.FocusMain = false
		case "pageup":
			if app.FocusMain {
				app.pageScroll(-1)
			}
		case "pagedown":
			if app.FocusMain {
				app.pageScroll(1)
			}
		case "home":
			if app.FocusMain {
				app.DetailScroll = 0
			}
		case "end":
			if app.FocusMain {
				viewRows := app.DetailViewRows
				if viewRows < 1 {
					viewRows = 1
				}
				app.DetailScroll = max(0, len(app.DetailLines)-viewRows)
			}
		case "prevtab":
			app.cycleTab(-1)
		case "nexttab":
			app.cycleTab(1)
		case "filter":
			if !app.FocusMain {
				app.filterPrompt(term)
			}
		case "config":
			app.loadInspect("config")
			app.FocusMain = true
		case "stats":
			app.loadStats()
			app.FocusMain = true
		case "logs":
			if !app.FocusMain && app.Mode == "containers" {
				app.loadLogs()
				app.FocusMain = true
			}
		case "attach":
			if !app.FocusMain && app.Mode == "containers" {
				if item := app.current(); item != nil {
					runExternal(term, "podman", "attach", item.Name)
					app.refresh(item.ID)
				}
			}
		case "shell":
			if !app.FocusMain && app.Mode == "containers" {
				if item := app.current(); item != nil {
					runExternal(term, "podman", "exec", "-it", item.Name, "sh")
					app.refresh(item.ID)
				}
			}
		case "hide":
			if !app.FocusMain && app.Mode == "containers" {
				app.toggleHideStopped()
			}
		case "start", "stop", "restart", "pause", "kill", "remove":
			if !app.FocusMain {
				if key == "pause" {
					if item := app.current(); item != nil && strings.EqualFold(item.State, "paused") {
						app.perform("unpause")
					} else {
						app.perform("pause")
					}
					continue
				}
				if key == "stop" || key == "kill" || key == "remove" {
					if app.confirm(term, key) {
						app.perform(key)
					}
				} else {
					app.perform(key)
				}
			}
		default:
			if len(key) == 1 && key[0] >= '1' && key[0] <= '5' {
				app.toggleMode(resourceModes[int(key[0]-'1')])
			}
		}
	}
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

func main() {
	showVersion := flag.Bool("version", false, "print version")
	showHelp := flag.Bool("help", false, "show help")
	flag.Parse()
	if *showVersion {
		fmt.Println("lzpody dev (Go)")
		return
	}
	if *showHelp {
		fmt.Println("lzpody - native Podman Libpod TUI\n\nUsage: lzpody [--version|--help]\n\nEnvironment: LZPODY_SOCKET, LZPODY_API_VERSION")
		return
	}
	if err := runTUI(NewPodmanClient("")); err != nil {
		fmt.Fprintln(os.Stderr, "lzpody:", err)
		os.Exit(1)
	}
}

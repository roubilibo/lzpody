package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestContainerItemAndPorts(t *testing.T) {
	item := containerItem(map[string]any{
		"Id": "c1", "Names": []any{"/mysql8"}, "State": "running", "Image": "mysql:8.4",
		"Ports": []any{
			map[string]any{"host_ip": "127.0.0.1", "host_port": float64(3306), "container_port": float64(3306), "protocol": "tcp"},
			map[string]any{"container_port": float64(33060), "protocol": "tcp"},
		},
	})
	if item.Name != "mysql8" || item.State != "running" || item.Image != "mysql:8.4" {
		t.Fatalf("unexpected container item: %+v", item)
	}
	want := []string{"127.0.0.1:3306->3306/tcp", "33060/tcp"}
	if fmt.Sprint(item.Ports) != fmt.Sprint(want) {
		t.Fatalf("ports = %v, want %v", item.Ports, want)
	}
}

func TestModelsAndStats(t *testing.T) {
	if got := containerStateLabel(containerItem(map[string]any{"Id": "c1", "Names": []any{"demo"}, "State": "exited", "Status": "Exited (0) 2 seconds ago"})); got != "exited (0)" {
		t.Fatalf("state label = %q", got)
	}
	if got := healthStatus(map[string]any{"Config": map[string]any{"Healthcheck": nil}}, "unknown"); got != "no healthcheck" {
		t.Fatalf("health = %q", got)
	}
	if got := podItem(map[string]any{"Id": "p1", "Name": "dev", "Status": "Running", "Containers": []any{map[string]any{"Id": "c1"}}}); got.Status != "1 containers" {
		t.Fatalf("pod status = %q", got.Status)
	}
	if got := imageItem(map[string]any{"Id": "sha256:abc", "RepoTags": []any{"mysql:8.4"}, "Size": float64(2048)}); got.Status != "2.0 KiB" {
		t.Fatalf("image status = %q", got.Status)
	}
	if got := networkItem(map[string]any{"name": "podman", "driver": "bridge"}); got.Name != "podman" || got.Status != "bridge" {
		t.Fatalf("network = %+v", got)
	}
	sample := statsPayload(map[string]any{"Stats": []any{map[string]any{"CPU": 12.5}}})
	if numberValue(sample["CPU"]) != 12.5 || !strings.Contains(statsLines([]map[string]any{sample})[2], "12.50%") {
		t.Fatalf("stats payload/lines failed: %+v", sample)
	}
}

func TestLifecycleQueries(t *testing.T) {
	if got := lifecycleQuery("start"); got != nil {
		t.Fatalf("start query = %v", got)
	}
	if got := lifecycleQuery("stop").Get("timeout"); got != "10" {
		t.Fatalf("stop query = %v", got)
	}
	if got := lifecycleQuery("kill").Get("signal"); got != "SIGKILL" {
		t.Fatalf("kill query = %v", got)
	}
}

func TestPodmanClientUsesUnixSocketAndNativePaths(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podman.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v5.0.0/libpod/containers/json" || r.URL.Query().Get("all") != "true" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"Id":"c1","Names":["/demo"],"State":"running"}]`))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	items, err := NewPodmanClient(socketPath).containers()
	if err != nil || len(items) != 1 || scalarText(items[0]["Id"]) != "c1" {
		t.Fatalf("containers() = %#v, err=%v", items, err)
	}
}

func TestPodmanClientReportsMissingSocket(t *testing.T) {
	client := NewPodmanClient(filepath.Join(t.TempDir(), "missing.sock"))
	_, err := client.containers()
	if err == nil || !strings.Contains(err.Error(), "Podman socket not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestBubbleTeaOwnsTerminalLifecycle(t *testing.T) {
	model := newBubbleModel(NewApp(NewPodmanClient("/tmp/missing.sock")))
	if model.width != 0 || model.height != 0 {
		t.Fatalf("model probed terminal size before Bubble Tea: %dx%d", model.width, model.height)
	}
	if got := model.View(); got != "Loading terminal…" {
		t.Fatalf("initial view = %q", got)
	}
}

func TestBubbleTeaOwnsPodmanIOThroughCommands(t *testing.T) {
	app := NewApp(NewPodmanClient(filepath.Join(t.TempDir(), "missing.sock")))
	model := newBubbleModel(app)
	command := model.immediateRefreshCmd()
	if command == nil {
		t.Fatal("refresh command is nil")
	}
	rawMessage := command()
	message, ok := rawMessage.(bubbleRefreshMsg)
	if !ok {
		t.Fatalf("refresh command returned %T, want bubbleRefreshMsg", rawMessage)
	}
	if app.Status != "Connecting to rootless Podman..." {
		t.Fatalf("command mutated model before Update: %q", app.Status)
	}
	model.Update(message)
	if !strings.Contains(app.Status, "Podman socket not found") {
		t.Fatalf("Update did not apply command result: %q", app.Status)
	}
}

func TestGoProjectFilesExist(t *testing.T) {
	if _, err := os.Stat("go.mod"); err != nil {
		t.Fatal(err)
	}
}

func TestOmarchyThemePaletteIsLoaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMARCHY_PATH", filepath.Join(home, "omarchy"))
	if err := os.MkdirAll(filepath.Join(home, ".local/state/omarchy/current"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "omarchy/themes/demo-theme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local/state/omarchy/current/theme.name"), []byte("demo-theme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	colors := "accent = \"#010203\"\nforeground = \"#040506\"\nselection = \"#070809\"\nmuted = \"#0a0b0c\"\ngreen = \"#0d0e0f\"\nred = \"#101112\"\nyellow = \"#131415\"\n"
	if err := os.WriteFile(filepath.Join(home, "omarchy/themes/demo-theme/colors.toml"), []byte(colors), 0o644); err != nil {
		t.Fatal(err)
	}
	theme := loadUITheme()
	if theme.Name != "Demo Theme" {
		t.Fatalf("theme name = %q", theme.Name)
	}
	if theme.Title.GetForeground() == nil || theme.Normal.GetForeground() == nil {
		t.Fatalf("theme colors were not applied: title=%v normal=%v", theme.Title.GetForeground(), theme.Normal.GetForeground())
	}
}

func TestDetailPaginationUsesVisibleRows(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.DetailLines = make([]string, 20)
	app.DetailViewRows = 5
	app.scroll(100)
	if app.DetailScroll != 15 {
		t.Fatalf("scroll = %d, want 15", app.DetailScroll)
	}
	app.pageScroll(-1)
	if app.DetailScroll != 11 {
		t.Fatalf("page scroll = %d, want 11", app.DetailScroll)
	}
}

func TestNavigationAndContainerMenuParity(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo", State: "running"}}
	app.moveFocus(-1)
	if app.Mode != "networks" {
		t.Fatalf("moveFocus(-1) mode = %q", app.Mode)
	}
	app.moveFocus(1)
	if app.Mode != "containers" {
		t.Fatalf("moveFocus(1) mode = %q", app.Mode)
	}
	app.FocusMain = true
	app.toggleMode("containers")
	if app.FocusMain {
		t.Fatal("selecting a panel did not return focus to the side")
	}
	entries := app.menuEntries()
	wanted := map[string]bool{"Start [S]": false, "Stop [s]": false, "Logs [m]": false, "Exec shell [E]": false, "Remove [d]": false}
	for _, entry := range entries {
		if _, ok := wanted[entry[0]]; ok {
			wanted[entry[0]] = true
		}
	}
	for label, present := range wanted {
		if !present {
			t.Errorf("menu entry %q missing", label)
		}
	}
}

func TestBubbleTeaKeyHandlingPreservesTUIActions(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo", State: "running"}}
	model := bubbleModel{app: app, width: 100, height: 30, refreshAfter: refreshInterval}

	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !app.MenuOpen || app.MenuIndex != 0 {
		t.Fatalf("menu state = open:%v index:%d", app.MenuOpen, app.MenuIndex)
	}
	model.Update(tea.KeyMsg{Type: tea.KeyDown})
	if app.MenuIndex != 1 {
		t.Fatalf("menu index = %d, want 1", app.MenuIndex)
	}
	model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if app.MenuOpen {
		t.Fatal("escape did not close menu")
	}

	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if app.ConfirmAction != "stop" {
		t.Fatalf("confirm action = %q, want stop", app.ConfirmAction)
	}
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if app.ConfirmAction != "" {
		t.Fatal("cancel did not close confirmation")
	}

	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !app.FilterInput {
		t.Fatal("filter input did not open")
	}
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if app.FilterDraft != "d" {
		t.Fatalf("filter draft = %q, want d", app.FilterDraft)
	}
	model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if app.FilterInput {
		t.Fatal("escape did not close filter input")
	}
}

func TestBubbleTeaViewIncludesNativeLayoutAndConfirmation(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo", State: "running"}}
	app.DetailLines = []string{"Name:    demo", "State:   running"}
	app.ConfirmAction = "stop"
	view := (bubbleModel{app: app, width: 100, height: 30}).View()
	for _, want := range []string{"native Libpod", "Confirm action", "Stop demo?", "Y/Enter confirm"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Bubble Tea view does not contain %q", want)
		}
	}
}

func TestPanelStyleDoesNotBleedIntoDetailText(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Mode = "images"
	app.Items["images"] = []Item{{Kind: "image", ID: "img1", Name: "mysql:8.4", State: "image", Status: "1.0 MiB"}}
	app.DetailMode = "config"
	app.DetailLines = []string{"line 0", "line 1", "line 2", "line 3", "line 4", "line 5", "line 6", "line 7", "line 8", "detail-body"}

	view := (bubbleModel{app: app, width: 100, height: 30}).View()
	lines := strings.Split(view, "\n")
	if len(lines) <= 13 {
		t.Fatalf("rendered view has %d lines, want detail row", len(lines))
	}
	line := lines[13]
	body := "detail-body"
	bodyStart := strings.Index(line, body)
	if bodyStart < 0 {
		t.Fatalf("detail body is missing from row: %q", line)
	}
	theme := loadUITheme()
	if !strings.Contains(line, theme.Normal.Render(body)) {
		t.Fatalf("detail body did not use the normal Lip Gloss style: %q", line)
	}
}

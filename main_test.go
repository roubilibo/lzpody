package main

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hinshun/vt10x"
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
	if numberValue(sample["CPU"]) != 12.5 || !strings.Contains(strings.Join(statsLines([]map[string]any{sample}), "\n"), "12.50") {
		t.Fatalf("stats payload/lines failed: %+v", sample)
	}
	lines := statsLines([]map[string]any{{"CPU": 12.5, "MemPerc": 4.5}})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "CPU (%)") || !strings.Contains(joined, "Memory (%)") || !strings.Contains(joined, "12.50") || !strings.Contains(joined, "┤") {
		t.Fatalf("cpu stats card missing: %v", lines)
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

func TestPodmanClientCreationAndImageOperations(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podman.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	seen := map[string]bool{}
	var pruneQuery url.Values
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method+" "+r.URL.Path] = true
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v5.0.0/libpod/containers/create":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if r.URL.Query().Get("name") != "demo" || payload["name"] != "demo" {
				t.Errorf("container name query=%q payload=%v", r.URL.Query().Get("name"), payload["name"])
			}
			_, _ = w.Write([]byte(`{"Id":"c1"}`))
		case "/v5.0.0/libpod/system/prune":
			pruneQuery = r.URL.Query()
			_, _ = w.Write([]byte(`{"SpaceReclaimed":1234}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	client := NewPodmanClient(socketPath)
	t.Setenv("REGISTRY_AUTH_FILE", filepath.Join(t.TempDir(), "auth.json"))
	if _, err := client.createContainer("alpine", "demo", "echo hi", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := client.createPod("dev"); err != nil {
		t.Fatal(err)
	}
	if err := client.createVolume("data"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.mountVolume("data"); err != nil {
		t.Fatal(err)
	}
	if err := client.unmountVolume("data"); err != nil {
		t.Fatal(err)
	}
	if err := client.createNetwork("frontend"); err != nil {
		t.Fatal(err)
	}
	if err := client.networkConnect("frontend", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := client.networkDisconnect("frontend", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := client.pullImage("docker.io/library/alpine:latest"); err != nil {
		t.Fatal(err)
	}
	if err := client.pushImage("alpine", "registry.example/alpine:latest"); err != nil {
		t.Fatal(err)
	}
	if err := client.buildImage(t.TempDir(), "demo:latest"); err != nil {
		t.Fatal(err)
	}
	if err := client.pruneImages(); err != nil {
		t.Fatal(err)
	}
	if output, err := client.systemPrune(true, true, true, []string{"until=24h", "label=app=demo"}); err != nil || !strings.Contains(output, "SpaceReclaimed") {
		t.Fatalf("systemPrune output=%q err=%v", output, err)
	}
	if pruneQuery.Get("all") != "true" || pruneQuery.Get("volumes") != "true" || pruneQuery.Get("build") != "true" || fmt.Sprint(pruneQuery["filter"]) != "[until=24h label=app=demo]" {
		t.Fatalf("system prune query = %v", pruneQuery)
	}
	secretFile := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secretFile, []byte("top-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.createSecret("demo-secret", secretFile); err != nil {
		t.Fatal(err)
	}
	if _, err := client.imageHistory("alpine"); err != nil {
		t.Fatal(err)
	}
	if err := client.tagImage("alpine", "registry.example/alpine", "test"); err != nil {
		t.Fatal(err)
	}
	if err := client.untagImage("alpine:test"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.searchImages("alpine", "5"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "alpine.tar")
	if err := client.saveImage("alpine", archive); err != nil {
		t.Fatal(err)
	}
	if err := client.loadImage(archive); err != nil {
		t.Fatal(err)
	}
	if err := client.importImage(archive, "demo:imported"); err != nil {
		t.Fatal(err)
	}
	if err := client.registryLogin("registry.example", "alice", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := client.registryLogout("registry.example"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"POST /v5.0.0/libpod/containers/create",
		"POST /v5.0.0/libpod/containers/c1/start",
		"POST /v5.0.0/libpod/pods/create",
		"POST /v5.0.0/libpod/volumes/create",
		"POST /v5.0.0/libpod/volumes/data/mount",
		"POST /v5.0.0/libpod/volumes/data/unmount",
		"POST /v5.0.0/libpod/networks/create",
		"POST /v5.0.0/libpod/networks/frontend/connect",
		"POST /v5.0.0/libpod/networks/frontend/disconnect",
		"POST /v5.0.0/libpod/images/pull",
		"POST /v5.0.0/libpod/images/alpine/push",
		"POST /v5.0.0/libpod/build",
		"POST /v5.0.0/libpod/images/prune",
		"POST /v5.0.0/libpod/system/prune",
		"POST /v5.0.0/libpod/secrets/create",
		"GET /v5.0.0/libpod/images/alpine/history",
		"POST /v5.0.0/libpod/images/alpine/tag",
		"POST /v5.0.0/libpod/images/alpine:test/untag",
		"GET /v5.0.0/libpod/images/search",
		"GET /v5.0.0/libpod/images/alpine/get",
		"POST /v5.0.0/libpod/images/load",
		"POST /v5.0.0/libpod/images/import",
		"POST /v5.0.0/auth",
	} {
		if !seen[path] {
			t.Errorf("missing request %q", path)
		}
	}
}

func TestPodmanClientExecAndCopy(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podman.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "result.txt", Mode: 0o644, Size: int64(len("from container"))}); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte("from container"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v5.0.0/libpod/containers/c1/exec":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"e1"}`))
		case "/v5.0.0/libpod/exec/e1/start":
			_, _ = w.Write([]byte("command output\n"))
		case "/v5.0.0/libpod/containers/c1/archive":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/x-tar")
				_, _ = w.Write(archive.Bytes())
			}
		default:
			if r.Method != http.MethodPut {
				http.Error(w, "unexpected request", http.StatusBadRequest)
			}
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	client := NewPodmanClient(socketPath)
	output, err := client.execCommand("c1", "printf hello")
	if err != nil || output != "command output" {
		t.Fatalf("exec output=%q err=%v", output, err)
	}
	source := filepath.Join(t.TempDir(), "local.txt")
	if err := os.WriteFile(source, []byte("to container"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := client.copyToContainer("c1", source, "/tmp"); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := client.copyFromContainer("c1", "/tmp/result.txt", destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "result.txt"))
	if err != nil || string(content) != "from container" {
		t.Fatalf("copied content=%q err=%v", content, err)
	}
}

func TestGlobalActionsAndDetailModes(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	labels := map[string]bool{}
	for _, entry := range app.menuEntries() {
		labels[entry[1]] = true
	}
	for _, action := range []string{"run", "system_info", "events", "system_prune"} {
		if !labels[action] {
			t.Errorf("container panel action %q is missing", action)
		}
	}
	for _, action := range []string{"pull", "create_pod", "create_network", "create_volume", "create_secret"} {
		if labels[action] {
			t.Errorf("container panel contains unrelated action %q", action)
		}
	}
	app.DetailMode = "system"
	if got := app.detailTabs(); fmt.Sprint(got) != "[system]" {
		t.Fatalf("system tabs = %v", got)
	}
	app.DetailMode = "events"
	if got := app.detailTabs(); fmt.Sprint(got) != "[events]" {
		t.Fatalf("event tabs = %v", got)
	}
	app.Mode = "images"
	app.DetailMode = "summary"
	app.Items["images"] = []Item{{Kind: "image", ID: "i1", Name: "alpine"}}
	if got := fmt.Sprint(app.detailTabs()); got != "[summary history config]" {
		t.Fatalf("image tabs = %s", got)
	}
}

func TestActionsAreScopedToResourcePanel(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	cases := []struct {
		mode    string
		item    Item
		include []string
		exclude []string
	}{
		{mode: "containers", item: Item{Kind: "container", ID: "c1", Name: "web"}, include: []string{"run", "shell", "exec"}, exclude: []string{"create_pod", "create_network", "image_search"}},
		{mode: "pods", item: Item{Kind: "pod", ID: "p1", Name: "app"}, include: []string{"create_pod", "start"}, exclude: []string{"run", "create_network", "image_search"}},
		{mode: "images", item: Item{Kind: "image", ID: "i1", Name: "alpine"}, include: []string{"pull", "image_search", "image_tag"}, exclude: []string{"run", "create_pod", "create_network", "image_history"}},
		{mode: "volumes", item: Item{Kind: "volume", ID: "v1", Name: "data"}, include: []string{"create_volume", "volume_mount"}, exclude: []string{"run", "create_network", "image_search"}},
		{mode: "networks", item: Item{Kind: "network", ID: "n1", Name: "frontend"}, include: []string{"create_network", "network_connect"}, exclude: []string{"run", "create_volume", "image_search"}},
		{mode: "secrets", item: Item{Kind: "secret", ID: "s1", Name: "db"}, include: []string{"create_secret", "config"}, exclude: []string{"run", "create_network", "image_search"}},
	}
	for _, testCase := range cases {
		app.Mode = testCase.mode
		app.Items[testCase.mode] = []Item{testCase.item}
		app.Selected[testCase.mode] = 0
		entries := app.menuEntries()
		actions := map[string]bool{}
		for _, entry := range entries {
			actions[entry[1]] = true
		}
		for _, action := range testCase.include {
			if !actions[action] {
				t.Errorf("%s panel is missing action %q", testCase.mode, action)
			}
		}
		for _, action := range testCase.exclude {
			if actions[action] {
				t.Errorf("%s panel contains unrelated action %q", testCase.mode, action)
			}
		}
	}
	app.Mode = "networks"
	app.DetailMode = "summary"
	app.Items["networks"] = []Item{{Kind: "network", ID: "n1", Name: "frontend"}}
	if got := fmt.Sprint(app.detailTabs()); got != "[summary relationships config]" {
		t.Fatalf("network tabs = %s", got)
	}
	app.DetailMode = "relationships"
	if got := fmt.Sprint(app.detailTabs()); got != "[summary relationships config]" {
		t.Fatalf("network relationship tabs = %s", got)
	}
}

func TestRegistryAuthFileLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	t.Setenv("REGISTRY_AUTH_FILE", path)
	if err := writeRegistryCredential("registry.example", "alice", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := (&PodmanClient{}).registryLogout("registry.example"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), "registry.example") {
		t.Fatalf("auth file after logout = %q err=%v", raw, err)
	}
}

func TestRelationshipLinesSelectsResourceLinks(t *testing.T) {
	lines := relationshipLines("network", map[string]any{"name": "frontend", "subnets": []any{map[string]any{"subnet": "10.89.0.0/24"}}, "containers": map[string]any{"c1": map[string]any{"name": "web"}}, "ignored": "value"})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "subnets") || !strings.Contains(joined, "containers") || strings.Contains(joined, "ignored") {
		t.Fatalf("relationship lines = %q", joined)
	}
}

func TestPodmanClientReportsMissingSocket(t *testing.T) {
	client := NewPodmanClient(filepath.Join(t.TempDir(), "missing.sock"))
	_, err := client.containers()
	if err == nil || !strings.Contains(err.Error(), "Podman socket not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodePodmanLogStream(t *testing.T) {
	first := []byte("2026-09-20 first\n")
	second := []byte("2026-09-20 second\n")
	stream := make([]byte, 0, 16+len(first)+len(second))
	for _, payload := range [][]byte{first, second} {
		header := make([]byte, 8)
		header[0] = 2
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		stream = append(stream, header...)
		stream = append(stream, payload...)
	}
	if got, want := decodePodmanLogStream(string(stream)), string(append(first, second...)); got != want {
		t.Fatalf("decoded stream = %q, want %q", got, want)
	}
	plain := "plain log without a multiplexed header\n"
	if got := decodePodmanLogStream(plain); got != plain {
		t.Fatalf("plain log changed to %q", got)
	}
}

func TestMarkLogTimestamps(t *testing.T) {
	lines := markLogTimestamps([]string{
		"2026-09-20T00:51:38.545438500+07:00 message",
		"continuation line",
	})
	want := []string{
		"> 2026-09-20T00:51:38.545438500+07:00 message",
		"continuation line",
	}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("marked logs = %v, want %v", lines, want)
	}
}

func TestStaleDetailResultIsIgnored(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo"}}
	app.DetailMode = "config"
	app.DetailRequestID = 2
	app.DetailLines = []string{"current"}
	app.applyDetail(detailResult{itemID: "c1", mode: "config", requestID: 1, lines: []string{"stale"}})
	if fmt.Sprint(app.DetailLines) != "[current]" {
		t.Fatalf("stale result replaced detail: %v", app.DetailLines)
	}
	app.applyDetail(detailResult{itemID: "c1", mode: "config", requestID: 2, lines: []string{"current request"}})
	if len(app.DetailLines) == 0 || app.DetailLines[0] != "current request" {
		t.Fatalf("current result was ignored: %v", app.DetailLines)
	}
}

func TestRefreshDoesNotOverwriteLiveDetailStatus(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo"}}
	app.DetailMode = "logs"
	app.Status = "Logs: demo"
	app.applyRefresh(resourceSnapshot{
		items:       map[string][]Item{"containers": app.Items["containers"]},
		status:      "Updated 01:23:45",
		lastRefresh: time.Now(),
	}, "c1")
	if app.Status != "Logs: demo" {
		t.Fatalf("live detail status = %q, want Logs: demo", app.Status)
	}
	app.applyRefresh(resourceSnapshot{
		items:       map[string][]Item{"containers": app.Items["containers"]},
		status:      "Podman socket not found",
		hasError:    true,
		lastRefresh: time.Now(),
	}, "c1")
	if app.Status != "Podman socket not found" {
		t.Fatalf("error status = %q", app.Status)
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
	colors := "accent = \"#010203\"\nforeground = \"#040506\"\nselection = \"#070809\"\nmuted = \"#0a0b0c\"\ngreen = \"#0d0e0f\"\nred = \"#101112\"\nyellow = \"#131415\"\ncyan = \"#161718\"\n"
	if err := os.WriteFile(filepath.Join(home, "omarchy/themes/demo-theme/colors.toml"), []byte(colors), 0o644); err != nil {
		t.Fatal(err)
	}
	theme := loadUITheme()
	if theme.Name != "Demo Theme" {
		t.Fatalf("theme name = %q", theme.Name)
	}
	if theme.Title.GetForeground() == nil || theme.Normal.GetForeground() == nil || theme.GraphCPU.GetForeground() == nil || theme.GraphMemory.GetForeground() == nil {
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

func TestSystemAndContainerConfigHaveBottomSpacer(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.DetailMode = "system"
	app.DetailViewRows = 3
	app.applyDetail(detailResult{mode: "system", lines: []string{"system-1", "system-2"}})
	app.scroll(100)
	if len(app.DetailLines) != 2+logsBottomSpacer || app.DetailScroll != logsBottomSpacer-1 {
		t.Fatalf("system detail end = lines:%d scroll:%d", len(app.DetailLines), app.DetailScroll)
	}
	for _, line := range app.DetailLines[2:] {
		if line != "" {
			t.Fatalf("system spacer contains %q", line)
		}
	}

	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo"}}
	app.DetailMode = "config"
	app.DetailViewRows = 3
	app.applyDetail(detailResult{itemID: "c1", mode: "config", lines: []string{"config-1", "config-2"}})
	app.scroll(100)
	if len(app.DetailLines) != 2+logsBottomSpacer || app.DetailScroll != logsBottomSpacer-1 {
		t.Fatalf("container config end = lines:%d scroll:%d", len(app.DetailLines), app.DetailScroll)
	}
}

func TestLogsFollowLatestAndPauseOnManualScroll(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo"}}
	app.DetailMode = "logs"
	app.DetailViewRows = 5
	app.applyDetail(detailResult{itemID: "c1", mode: "logs", lines: make([]string, 20)})
	if app.DetailScroll != 15+logsBottomSpacer || !app.LogsFollow {
		t.Fatalf("initial log follow = scroll:%d follow:%v, want %d/true", app.DetailScroll, app.LogsFollow, 15+logsBottomSpacer)
	}

	app.scroll(-1)
	if app.DetailScroll != 14+logsBottomSpacer || app.LogsFollow {
		t.Fatalf("manual log scroll = scroll:%d follow:%v, want %d/false", app.DetailScroll, app.LogsFollow, 14+logsBottomSpacer)
	}
	app.applyDetail(detailResult{itemID: "c1", mode: "logs", lines: make([]string, 30)})
	if app.DetailScroll != 14+logsBottomSpacer {
		t.Fatalf("paused log follow moved to %d, want %d", app.DetailScroll, 14+logsBottomSpacer)
	}

	app.scroll(100)
	if app.DetailScroll != 25+logsBottomSpacer || !app.LogsFollow {
		t.Fatalf("return to log tail = scroll:%d follow:%v, want %d/true", app.DetailScroll, app.LogsFollow, 25+logsBottomSpacer)
	}
	app.applyDetail(detailResult{itemID: "c1", mode: "logs", lines: make([]string, 40)})
	if app.DetailScroll != 35+logsBottomSpacer {
		t.Fatalf("continued log follow = %d, want %d", app.DetailScroll, 35+logsBottomSpacer)
	}
}

func TestLogsWrapToDetailWidth(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo"}}
	app.DetailMode = "logs"
	app.DetailViewRows = 3
	app.DetailViewWidth = 5
	app.applyDetail(detailResult{itemID: "c1", mode: "logs", lines: []string{"abcdefghijkl"}})
	want := []string{"abcde", "fghij", "kl"}
	if fmt.Sprint(app.DetailLines[:len(want)]) != fmt.Sprint(want) {
		t.Fatalf("wrapped logs = %v, want %v", app.DetailLines[:len(want)], want)
	}
	if len(app.DetailLines) != len(want)+logsBottomSpacer {
		t.Fatalf("log spacer rows = %d, want %d", len(app.DetailLines)-len(want), logsBottomSpacer)
	}
	for index, line := range app.DetailLines {
		if len([]rune(line)) > app.DetailViewWidth {
			t.Fatalf("wrapped log line %d has width %d, want <= %d", index, len([]rune(line)), app.DetailViewWidth)
		}
	}
}

func TestNavigationAndContainerMenuParity(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo", State: "running"}}
	app.moveFocus(-1)
	if app.Mode != "secrets" {
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

func TestContainerCreateOptionsParsing(t *testing.T) {
	values := []string{
		"alpine", "demo", "echo hi", "dev", "8080:80/tcp", "PORT=80,MODE=dev",
		"data:/var/lib/data:ro", "frontend", "always", "1.5", "512m", "1000:1000",
		"/app", "add:NET_ADMIN,drop:ALL", "wget -qO- http://localhost/health",
		"db-secret", "app=demo", "/dev/null:/dev/null:rwm", "label=disable",
	}
	options := parseContainerCreate(values)
	if options.Image != "alpine" || options.Name != "demo" || options.Restart != "always" || options.Workdir != "/app" {
		t.Fatalf("parsed create options = %+v", options)
	}
	if got := parseEnvironment(options.Environment)["PORT"]; got != "80" {
		t.Fatalf("parsed environment PORT = %q", got)
	}
	if got := parsePortMappings(options.Ports); len(got) != 1 || got[0]["host_port"] != 8080 {
		t.Fatalf("parsed ports = %+v", got)
	}
	if got := parseMounts(options.Mounts); len(got) != 1 || got[0]["destination"] != "/var/lib/data" {
		t.Fatalf("parsed mounts = %+v", got)
	}
	if got := parseCapabilities(options.Capabilities); len(got["add"]) != 1 || len(got["drop"]) != 1 {
		t.Fatalf("parsed capabilities = %+v", got)
	}
	if got := parseSecrets([]string{"db-secret,target=/run/secrets/db,type=mount,uid=1000,mode=0400"}); len(got) != 1 || got[0]["source"] != "db-secret" || got[0]["target"] != "/run/secrets/db" {
		t.Fatalf("parsed secrets = %+v", got)
	}
	if got := parseNetworks([]string{"frontend,ip=10.89.0.10,alias=web+api", "backend"}); len(got) != 2 || got["frontend"].(map[string]any)["ip"] != "10.89.0.10" {
		t.Fatalf("parsed networks = %+v", got)
	}
}

func TestContainerFormUsesTabbedPanels(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.beginPrompt("run")
	model := newBubbleModel(app)
	model.updateContainerForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpine")})
	model.updateContainerForm(tea.KeyMsg{Type: tea.KeyTab})
	model.updateContainerForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("named-container")})
	model.updateContainerForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if app.ContainerForm == nil || app.ContainerForm.tab != 1 || app.ContainerForm.fields[0].value != "alpine" || app.ContainerForm.fields[1].value != "named-container" {
		t.Fatalf("container form state = %+v", app.ContainerForm)
	}
	if got := app.ContainerForm.values()[1]; got != "named-container" {
		t.Fatalf("container form name = %q", got)
	}
	if !app.FocusMain || app.DetailMode != "create" {
		t.Fatalf("container form focus/mode = focus:%v mode:%q", app.FocusMain, app.DetailMode)
	}
	model.width, model.height = 100, 30
	view := model.View()
	if !strings.Contains(view, "Create container [form]") || !strings.Contains(view, "[Network]") {
		t.Fatalf("container form is not rendered in the detail panel:\n%s", view)
	}
}

func TestNetworkFormUsesDetailPanel(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.beginPrompt("create_network")
	if app.ContainerForm == nil || app.ContainerForm.action != "create_network" || len(app.ContainerForm.fields) != 8 {
		t.Fatalf("network form state = %+v", app.ContainerForm)
	}
	model := newBubbleModel(app)
	model.width, model.height = 100, 30
	model.updateContainerForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("frontend")})
	model.updateContainerForm(tea.KeyMsg{Type: tea.KeyTab})
	model.updateContainerForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bridge")})
	if got := app.ContainerForm.values(); got[0] != "frontend" || got[1] != "bridge" {
		t.Fatalf("network form values = %v", got)
	}
	view := model.View()
	if !strings.Contains(view, "Create network [form]") || !strings.Contains(view, "[Network]") {
		t.Fatalf("network form is not rendered in the detail panel:\n%s", view)
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

func TestBubbleTeaMouseNavigation(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{
		{Kind: "container", ID: "c1", Name: "first"},
		{Kind: "container", ID: "c2", Name: "second"},
	}
	model := bubbleModel{app: app, width: 100, height: 30}

	model.Update(tea.MouseMsg{X: 3, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if app.Mode != "containers" || app.Selected["containers"] != 1 {
		t.Fatalf("mouse item selection = mode:%s index:%d", app.Mode, app.Selected["containers"])
	}

	model.Update(tea.MouseMsg{X: 49, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if app.DetailMode != "logs" || !app.FocusMain {
		t.Fatalf("mouse tab selection = mode:%s focus:%v", app.DetailMode, app.FocusMain)
	}

	app.DetailLines = make([]string, 50)
	app.DetailViewRows = 5
	app.DetailScroll = 10
	model.Update(tea.MouseMsg{X: 45, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if app.DetailScroll != 13 {
		t.Fatalf("mouse wheel scroll = %d, want 13", app.DetailScroll)
	}
}

func TestBubbleTeaRightClickOpensResourceMenu(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo"}}
	app.Items["images"] = []Item{{Kind: "image", ID: "i1", Name: "alpine"}}
	model := bubbleModel{app: app, width: 100, height: 30}

	model.Update(tea.MouseMsg{X: 3, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if !app.MenuOpen || app.Mode != "containers" || app.current().Kind != "container" {
		t.Fatalf("right-click container menu state = open:%v mode:%s", app.MenuOpen, app.Mode)
	}
	if action := app.selectedMenuAction(); action != "start" {
		t.Fatalf("container context action = %q, want start", action)
	}

	app.closeMenu()
	model.Update(tea.MouseMsg{X: 3, Y: 13, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if !app.MenuOpen || app.Mode != "images" || app.current().Kind != "image" {
		t.Fatalf("right-click image menu state = open:%v mode:%s", app.MenuOpen, app.Mode)
	}
	if action := app.selectedMenuAction(); action != "config" {
		t.Fatalf("image context action = %q, want config", action)
	}
}

func TestMenuEntryTextAlignsShortcuts(t *testing.T) {
	width := 30
	start := menuEntryText([2]string{"Start [S]", "start"}, width)
	stop := menuEntryText([2]string{"Hide stopped [e]", "hide_stopped"}, width)
	plain := menuEntryText([2]string{"Environment", "env"}, width)

	if len([]rune(start)) != width || len([]rune(stop)) != width || len([]rune(plain)) != width {
		t.Fatalf("menu entries are not padded to %d columns", width)
	}
	if strings.Index(start, "S") != strings.Index(stop, "e") {
		t.Fatalf("shortcuts are not aligned: start=%q stop=%q", start, stop)
	}
	if !strings.HasPrefix(start, "S       ") || !strings.HasPrefix(stop, "e       ") {
		t.Fatalf("shortcuts are not in the left column: start=%q stop=%q", start, stop)
	}
	if strings.TrimSpace(plain) != "Environment" {
		t.Fatalf("plain menu label changed: %q", plain)
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

func TestExecPromptUsesContextOverlay(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "mysql8"}}
	app.beginPrompt("exec")
	app.Prompt.value = "printf hello"

	view := (bubbleModel{app: app, width: 100, height: 30}).View()
	for _, want := range []string{"Exec command", "Container: mysql8", "> printf hello", "Enter run   Esc cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("exec prompt overlay does not contain %q", want)
		}
	}
}

func TestExecPromptAcceptsSpaces(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "mysql8"}}
	app.beginPrompt("exec")
	model := bubbleModel{app: app, width: 100, height: 30}

	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	if app.Prompt == nil || app.Prompt.value != "e -" {
		t.Fatalf("exec prompt value = %q, want %q", app.Prompt.value, "e -")
	}
}

func TestEmbeddedShellInputAndOutput(t *testing.T) {
	if got := string(shellKeyBytes(tea.KeyMsg{Type: tea.KeyEnter})); got != "\r" {
		t.Fatalf("shell enter bytes = %q, want carriage return", got)
	}
	if got := string(shellKeyBytes(tea.KeyMsg{Type: tea.KeySpace})); got != " " {
		t.Fatalf("shell space bytes = %q, want space", got)
	}
	emulator := vt10x.New(vt10x.WithSize(20, 3))
	session := &shellSession{emulator: emulator}
	_, _ = emulator.Write([]byte("abc"))
	_, _ = emulator.Write([]byte("\x1b[2D\x1b[KX"))
	lines := shellSnapshot(session)
	if !strings.Contains(lines[0], "aX") {
		t.Fatalf("terminal emulator did not apply cursor movement: %q", lines[0])
	}
	if !strings.Contains(fmt.Sprint(lines), "▌") {
		t.Fatalf("terminal emulator cursor is not visible: %v", lines)
	}
}

func TestPullProgressText(t *testing.T) {
	line := pullProgressText(`{"status":"Downloading","id":"layer1","progressDetail":{"current":524288,"total":1048576}}`)
	for _, want := range []string{"layer1", "Downloading", "512.0 KiB/1.0 MiB"} {
		if !strings.Contains(line, want) {
			t.Fatalf("pull progress = %q, missing %q", line, want)
		}
	}
	if got := pullProgressText(`{"error":"unauthorized"}`); got != "Error: unauthorized" {
		t.Fatalf("pull error progress = %q", got)
	}
}

func TestPullOverlayClosesOnAnyKey(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Pull = &pullSession{reference: "alpine:latest"}
	app.PullOverlay = true
	model := bubbleModel{app: app, width: 100, height: 30}

	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if app.Pull != nil || app.PullOverlay {
		t.Fatalf("pull overlay state = pull:%v overlay:%v", app.Pull != nil, app.PullOverlay)
	}
}

func TestPullOverlayStartsBeforeConnection(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.beginPrompt("pull")
	model := bubbleModel{app: app, width: 100, height: 30}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatal("pull prompt submitted before Enter")
	}
	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || app.Pull == nil || !app.PullOverlay {
		t.Fatalf("pull overlay did not start immediately: cmd:%v pull:%v overlay:%v", cmd != nil, app.Pull != nil, app.PullOverlay)
	}
	if app.Pull.lines[0] != "Connecting to Podman..." {
		t.Fatalf("initial pull status = %q", app.Pull.lines[0])
	}
}

func TestShiftEUsesEmbeddedShell(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Items["containers"] = []Item{{Kind: "container", ID: "c1", Name: "demo"}}
	model := bubbleModel{app: app, width: 100, height: 30}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if cmd == nil {
		t.Fatal("Shift+E did not create an embedded shell command")
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

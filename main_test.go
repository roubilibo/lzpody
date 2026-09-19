package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestTUIRequiresTTY(t *testing.T) {
	if isTTY() {
		t.Skip("test process has a tty")
	}
	if err := runTUI(NewPodmanClient("/tmp/missing.sock")); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("runTUI error = %v", err)
	}
}

func TestGoProjectFilesExist(t *testing.T) {
	if _, err := os.Stat("go.mod"); err != nil {
		t.Fatal(err)
	}
}

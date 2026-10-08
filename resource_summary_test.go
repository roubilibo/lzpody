package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVolumeSummaryUsesInspectFieldsAndStableMapOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5.0.0/libpod/volumes/db-data/json" {
			t.Errorf("volume inspect path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Name":"db-data","Driver":"local","CreatedAt":"2026-01-02T03:04:05Z","Mountpoint":"/var/lib/containers/storage/volumes/db-data/_data","Labels":{"team":"db","env":"prod"},"Options":{"device":"/dev/sdb","type":"xfs"}}`))
	}))
	defer server.Close()

	item := Item{Kind: "volume", ID: "db-data", Name: "db-data", Status: "local", Details: map[string]any{"Scope": "local", "MountCount": float64(2)}}
	result := fetchDetail(NewPodmanClient(server.URL), item, "summary", nil, "")
	joined := strings.Join(result.lines, "\n")
	for _, want := range []string{
		"Name:       db-data",
		"Driver:     local",
		"Scope: local",
		"Created: 2026-01-02T03:04:05Z",
		"Mountpoint: /var/lib/containers/storage/volumes/db-data/_data",
		"Mount Count: 2",
		"Labels: env=prod, team=db",
		"Options: device=/dev/sdb, type=xfs",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("volume summary missing %q:\n%s", want, joined)
		}
	}
}

func TestNetworkSummaryIncludesConfigurationAndConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5.0.0/libpod/networks/frontend/json" {
			t.Errorf("network inspect path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"id":"network-id","name":"frontend","driver":"bridge","created":"2026-02-03T04:05:06Z","network_interface":"podman1","subnets":[{"subnet":"10.89.0.0/24","gateway":"10.89.0.1"},{"subnet":"fd10::/64","gateway":"fd10::1"}],"ipv6_enabled":true,"internal":false,"dns_enabled":true,"containers":{"c2":{"name":"api","ipv4_address":"10.89.0.3/24","ipv6_address":"fd10::3/64"},"c1":{"Name":"web","IPv4Address":"10.89.0.2/24"}},"labels":{"team":"app","env":"dev"}}`))
	}))
	defer server.Close()

	item := Item{Kind: "network", ID: "frontend", Name: "frontend", Status: "bridge"}
	result := fetchDetail(NewPodmanClient(server.URL), item, "summary", nil, "")
	joined := strings.Join(result.lines, "\n")
	for _, want := range []string{
		"Name:       frontend",
		"ID:         network-id",
		"Driver:     bridge",
		"Created: 2026-02-03T04:05:06Z",
		"Interface: podman1",
		"Subnets:",
		"10.89.0.0/24 · gateway 10.89.0.1",
		"fd10::/64 · gateway fd10::1",
		"IPv6: true",
		"Internal: false",
		"DNS enabled: true",
		"Connected containers (2):",
		"api · 10.89.0.3/24, fd10::3/64",
		"web · 10.89.0.2/24",
		"Labels: env=dev, team=app",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("network summary missing %q:\n%s", want, joined)
		}
	}
	if strings.Index(joined, "  api ·") > strings.Index(joined, "  web ·") {
		t.Fatalf("network container order is unstable: %s", joined)
	}
}

func TestVolumeSummaryFallsBackWhenInspectFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "inspect unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	item := Item{
		Kind: "volume", ID: "cache", Name: "cache", Status: "local",
		Details: map[string]any{"Mountpoint": "/var/lib/volumes/cache", "Scope": "local", "MountCount": float64(0)},
	}
	result := fetchDetail(NewPodmanClient(server.URL), item, "summary", nil, "")
	joined := strings.Join(result.lines, "\n")
	for _, want := range []string{"Name:       cache", "Driver:     local", "Scope: local", "Mountpoint: /var/lib/volumes/cache", "Mount Count: 0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("fallback summary missing %q:\n%s", want, joined)
		}
	}
}

func TestNetworkSummaryReportsZeroConnectedContainers(t *testing.T) {
	lines := networkSummaryLines(Item{Kind: "network", Name: "isolated", ID: "isolated", Status: "bridge"}, map[string]any{"containers": map[string]any{}})
	if got := fmt.Sprint(lines); !strings.Contains(got, "Connected containers (0):") {
		t.Fatalf("empty network summary = %v", lines)
	}
}

func TestVolumeSummaryWrapsLongValuesAtDetailWidth(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.Mode = "volumes"
	app.Items["volumes"] = []Item{{Kind: "volume", ID: "cache", Name: "cache"}}
	app.DetailMode = "summary"
	line := "Mountpoint: /var/lib/containers/storage/volumes/cache/_data"
	app.DetailRawLines = []string{line}
	app.reflowDetail(20)
	if len(app.DetailLines) < 2 {
		t.Fatalf("long volume summary was not wrapped: %v", app.DetailLines)
	}
	for _, wrapped := range app.DetailLines {
		if len(wrapped) > 20 {
			t.Fatalf("wrapped line exceeds detail width: %q", wrapped)
		}
	}
	if strings.Join(app.DetailLines, "") != line {
		t.Fatalf("wrapped volume summary lost data: %v", app.DetailLines)
	}
}

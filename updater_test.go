package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReleaseBinaryName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
		wantErr            bool
	}{
		{goos: "linux", goarch: "amd64", want: "lzpody-linux-amd64"},
		{goos: "linux", goarch: "arm64", want: "lzpody-linux-arm64"},
		{goos: "linux", goarch: "arm", want: "lzpody-linux-armv7"},
		{goos: "darwin", goarch: "arm64", wantErr: true},
		{goos: "linux", goarch: "386", wantErr: true},
	}
	for _, test := range tests {
		got, err := releaseBinaryName(test.goos, test.goarch)
		if (err != nil) != test.wantErr || got != test.want {
			t.Errorf("releaseBinaryName(%q, %q) = %q, %v", test.goos, test.goarch, got, err)
		}
	}
}

func TestReleaseIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{current: "v0.2.5", latest: "v0.2.6", want: true},
		{current: "0.2.5", latest: "v0.2.5", want: false},
		{current: "v0.3.0", latest: "v0.2.9", want: false},
		{current: "v0.2.6-rc.1", latest: "v0.2.6", want: true},
	}
	for _, test := range tests {
		got, err := releaseIsNewer(test.current, test.latest)
		if err != nil || got != test.want {
			t.Errorf("releaseIsNewer(%q, %q) = %v, %v; want %v", test.current, test.latest, got, err, test.want)
		}
	}
	if _, err := releaseIsNewer("dev", "v0.2.6"); err == nil {
		t.Fatal("development build version was accepted")
	}
}

func TestCheckLatestReleaseSelectsMatchingAssets(t *testing.T) {
	var baseURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"tag_name":"v0.2.6","assets":[{"name":"lzpody-linux-arm64","browser_download_url":%q},{"name":"lzpody-linux-amd64","browser_download_url":%q},{"name":"SHA256SUMS","browser_download_url":%q}]}`, baseURL+"/arm64", baseURL+"/amd64", baseURL+"/SHA256SUMS")
	}))
	defer server.Close()
	baseURL = server.URL

	release, err := checkLatestRelease(context.Background(), "v0.2.5", "linux", "amd64", baseURL+"/latest", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if release == nil || release.TagName != "v0.2.6" || release.BinaryName != "lzpody-linux-amd64" || release.BinaryURL != baseURL+"/amd64" || release.ChecksumsURL != baseURL+"/SHA256SUMS" {
		t.Fatalf("selected release = %+v", release)
	}
	upToDate, err := checkLatestRelease(context.Background(), "v0.2.6", "linux", "amd64", baseURL+"/latest", server.Client())
	if err != nil || upToDate != nil {
		t.Fatalf("up-to-date result = %+v, %v", upToDate, err)
	}
}

func TestInstallReleaseReplacesBinaryOnlyAfterChecksumPasses(t *testing.T) {
	binary := []byte("new lzpody executable")
	checksum := fmt.Sprintf("%x", sha256.Sum256(binary))
	for _, corrupt := range []bool{false, true} {
		name := "valid"
		if corrupt {
			name = "invalid"
		}
		t.Run(name, func(t *testing.T) {
			manifestChecksum := checksum
			if corrupt {
				manifestChecksum = strings.Repeat("0", sha256.Size*2)
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/SHA256SUMS":
					_, _ = fmt.Fprintf(w, "%s  lzpody-linux-amd64\n", manifestChecksum)
				case "/binary":
					_, _ = w.Write(binary)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			target := filepath.Join(t.TempDir(), "lzpody")
			if err := os.WriteFile(target, []byte("old executable"), 0o755); err != nil {
				t.Fatal(err)
			}
			release := updateRelease{TagName: "v0.2.6", BinaryName: "lzpody-linux-amd64", BinaryURL: server.URL + "/binary", ChecksumsURL: server.URL + "/SHA256SUMS"}
			err := installRelease(context.Background(), release, target, server.Client())
			got, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if corrupt {
				if err == nil || string(got) != "old executable" {
					t.Fatalf("failed verification changed target: err=%v contents=%q", err, got)
				}
			} else if err != nil || string(got) != string(binary) {
				t.Fatalf("update result = %v, contents=%q", err, got)
			}
		})
	}
}

func TestUpdateCheckFromDevBuildDoesNotCallNetwork(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	model := newBubbleModel(app)
	command := model.checkForUpdatesCmd()
	if command == nil || !app.UpdateChecking {
		t.Fatal("update check did not start")
	}
	message := command()
	_, _ = model.Update(message)
	if app.UpdateChecking || app.UpdateConfirm || !strings.Contains(app.Status, "release binaries") {
		t.Fatalf("dev update state = checking:%v confirm:%v status:%q", app.UpdateChecking, app.UpdateConfirm, app.Status)
	}
}

func TestUpdateConfirmationCanBeCancelled(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	model := newBubbleModel(app)
	_, _ = model.Update(updateCheckMsg{release: &updateRelease{TagName: "v0.2.6"}, target: "/tmp/lzpody"})
	if !app.UpdateConfirm || app.UpdateTarget != "/tmp/lzpody" {
		t.Fatalf("available update did not open confirmation: %+v", app)
	}
	_, command := model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if command != nil || app.UpdateConfirm || app.UpdateRelease != nil || app.UpdateInProgress {
		t.Fatalf("cancelled update state = confirm:%v release:%+v inProgress:%v", app.UpdateConfirm, app.UpdateRelease, app.UpdateInProgress)
	}
}

func TestUpdateConfirmationStartsAsyncInstallAndReportsRestart(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	model := newBubbleModel(app)
	_, _ = model.Update(updateCheckMsg{release: &updateRelease{TagName: "v0.2.6", BinaryName: "lzpody-linux-amd64", BinaryURL: "https://example.test/binary", ChecksumsURL: "https://example.test/SHA256SUMS"}, target: "/tmp/lzpody"})
	_, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil || !app.UpdateInProgress || app.UpdateConfirm {
		t.Fatalf("confirmed update state = confirm:%v inProgress:%v command:%v", app.UpdateConfirm, app.UpdateInProgress, command != nil)
	}
	_, quitCommand := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if quitCommand != nil {
		t.Fatal("update allowed quitting while replacing the executable")
	}
	_, _ = model.Update(updateInstallMsg{tag: "v0.2.6"})
	if app.UpdateInProgress || !strings.Contains(app.Status, "Restart lzpody") {
		t.Fatalf("successful update status = %q, inProgress:%v", app.Status, app.UpdateInProgress)
	}
}

func TestDevelopmentMenuDoesNotOfferSelfUpdate(t *testing.T) {
	if version != "dev" {
		t.Skip("test requires a development build")
	}
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	for _, entry := range app.visibleMenuEntries() {
		if entry[1] == "check_updates" {
			t.Fatal("development build offered self-update")
		}
	}
}

func TestPeriodicRefreshDoesNotOverwriteUpdateStatus(t *testing.T) {
	app := NewApp(NewPodmanClient("/tmp/unused-lzpody.sock"))
	app.UpdateInProgress = true
	app.Status = "Downloading lzpody v0.2.6..."
	app.applyRefresh(resourceSnapshot{items: map[string][]Item{}, status: "Updated 12:00:00"}, "")
	if app.Status != "Downloading lzpody v0.2.6..." {
		t.Fatalf("update status replaced by periodic refresh: %q", app.Status)
	}
}

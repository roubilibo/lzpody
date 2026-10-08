package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	latestReleaseAPI = "https://api.github.com/repos/roubilibo/lzpody/releases/latest"
	maxReleaseInfo   = 1 << 20
	maxReleaseBinary = 128 << 20
)

type updateRelease struct {
	TagName      string
	BinaryName   string
	BinaryURL    string
	ChecksumsURL string
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func checkLatestRelease(ctx context.Context, currentVersion, goos, goarch, apiURL string, client *http.Client) (*updateRelease, error) {
	if currentVersion == "dev" {
		return nil, fmt.Errorf("self-update is available only for release binaries")
	}
	assetName, err := releaseBinaryName(goos, goarch)
	if err != nil {
		return nil, err
	}
	if apiURL == "" {
		apiURL = latestReleaseAPI
	}
	if client == nil {
		client = updateHTTPClient()
	}
	body, err := getUpdateBody(ctx, client, apiURL, maxReleaseInfo)
	if err != nil {
		return nil, err
	}
	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("decode latest release: %w", err)
	}
	if release.Draft || release.Prerelease || release.TagName == "" {
		return nil, fmt.Errorf("GitHub returned an invalid stable release")
	}
	newer, err := releaseIsNewer(currentVersion, release.TagName)
	if err != nil {
		return nil, err
	}
	if !newer {
		return nil, nil
	}
	result := &updateRelease{TagName: release.TagName, BinaryName: assetName}
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			result.BinaryURL = asset.BrowserDownloadURL
		case "SHA256SUMS":
			result.ChecksumsURL = asset.BrowserDownloadURL
		}
	}
	if result.BinaryURL == "" || result.ChecksumsURL == "" {
		return nil, fmt.Errorf("release %s is missing the %s binary or SHA256SUMS", release.TagName, assetName)
	}
	if err := requireHTTPSURL(result.BinaryURL); err != nil {
		return nil, fmt.Errorf("release binary URL: %w", err)
	}
	if err := requireHTTPSURL(result.ChecksumsURL); err != nil {
		return nil, fmt.Errorf("release checksums URL: %w", err)
	}
	return result, nil
}

func releaseBinaryName(goos, goarch string) (string, error) {
	if goos != "linux" {
		return "", fmt.Errorf("self-update is currently supported on Linux only")
	}
	arch := goarch
	if arch == "arm" {
		arch = "armv7"
	}
	switch arch {
	case "amd64", "arm64", "armv7":
		return "lzpody-linux-" + arch, nil
	default:
		return "", fmt.Errorf("self-update is not supported on %s/%s", goos, goarch)
	}
}

func releaseIsNewer(current, latest string) (bool, error) {
	currentVersion, err := parseReleaseVersion(current)
	if err != nil {
		return false, fmt.Errorf("cannot compare installed version %q: %w", current, err)
	}
	latestVersion, err := parseReleaseVersion(latest)
	if err != nil {
		return false, fmt.Errorf("invalid latest release version %q: %w", latest, err)
	}
	for i := range currentVersion.core {
		if latestVersion.core[i] != currentVersion.core[i] {
			return latestVersion.core[i] > currentVersion.core[i], nil
		}
	}
	return currentVersion.prerelease && !latestVersion.prerelease, nil
}

type parsedReleaseVersion struct {
	core       [3]uint64
	prerelease bool
}

func parseReleaseVersion(value string) (parsedReleaseVersion, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	value = strings.SplitN(value, "+", 2)[0]
	versionAndPrerelease := strings.SplitN(value, "-", 2)
	parts := strings.Split(versionAndPrerelease[0], ".")
	if len(parts) != 3 {
		return parsedReleaseVersion{}, fmt.Errorf("expected semantic version MAJOR.MINOR.PATCH")
	}
	parsed := parsedReleaseVersion{prerelease: len(versionAndPrerelease) == 2}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return parsedReleaseVersion{}, fmt.Errorf("invalid numeric version component")
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return parsedReleaseVersion{}, fmt.Errorf("invalid numeric version component")
		}
		parsed.core[i] = number
	}
	if parsed.prerelease && versionAndPrerelease[1] == "" {
		return parsedReleaseVersion{}, fmt.Errorf("empty prerelease identifier")
	}
	return parsed, nil
}

func updateTargetPath(currentVersion string) (string, error) {
	if currentVersion == "dev" {
		return "", fmt.Errorf("self-update is available only for release binaries")
	}
	if _, err := releaseBinaryName(runtime.GOOS, runtime.GOARCH); err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	info, err := os.Stat(executable)
	if err != nil {
		return "", fmt.Errorf("inspect current executable: %w", err)
	}
	if info.Mode()&0111 == 0 {
		return "", fmt.Errorf("current executable is not marked executable")
	}
	dir := filepath.Dir(executable)
	testFile, err := os.CreateTemp(dir, ".lzpody-update-check-*")
	if err != nil {
		return "", fmt.Errorf("cannot update binary in %s; use its package manager or install lzpody in a user-writable directory: %w", dir, err)
	}
	name := testFile.Name()
	if err := testFile.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("check update directory: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return "", fmt.Errorf("check update directory: %w", err)
	}
	return executable, nil
}

func installRelease(ctx context.Context, release updateRelease, target string, client *http.Client) error {
	if err := requireHTTPSURL(release.BinaryURL); err != nil {
		return fmt.Errorf("release binary URL: %w", err)
	}
	if err := requireHTTPSURL(release.ChecksumsURL); err != nil {
		return fmt.Errorf("release checksums URL: %w", err)
	}
	if client == nil {
		client = updateHTTPClient()
	}
	checksumsBody, err := getUpdateBody(ctx, client, release.ChecksumsURL, maxReleaseInfo)
	if err != nil {
		return fmt.Errorf("download release checksums: %w", err)
	}
	expectedChecksum, err := checksumForAsset(string(checksumsBody), release.BinaryName)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("inspect current binary: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".lzpody-update-*")
	if err != nil {
		return fmt.Errorf("create temporary update: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set update permissions: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, release.BinaryURL, nil)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("prepare binary download: %w", err)
	}
	request.Header.Set("User-Agent", "lzpody-updater")
	response, err := client.Do(request)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("download release binary: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		_ = temp.Close()
		return fmt.Errorf("download release binary: %s", response.Status)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, maxReleaseBinary+1))
	bodyCloseErr := response.Body.Close()
	if copyErr != nil {
		_ = temp.Close()
		return fmt.Errorf("write update binary: %w", copyErr)
	}
	if bodyCloseErr != nil {
		_ = temp.Close()
		return fmt.Errorf("finish binary download: %w", bodyCloseErr)
	}
	if written == 0 || written > maxReleaseBinary {
		_ = temp.Close()
		return fmt.Errorf("release binary has an invalid size")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, expectedChecksum) {
		_ = temp.Close()
		return fmt.Errorf("release binary checksum verification failed")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync update binary: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close update binary: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("replace current binary: %w", err)
	}
	return nil
}

func requireHTTPSURL(value string) error {
	if !strings.HasPrefix(strings.ToLower(value), "https://") {
		return fmt.Errorf("must use HTTPS")
	}
	return nil
}

func checksumForAsset(manifest, assetName string) (string, error) {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != assetName {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA256 checksum for %s", assetName)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("invalid SHA256 checksum for %s", assetName)
		}
		return fields[0], nil
	}
	return "", fmt.Errorf("checksum for %s is missing from SHA256SUMS", assetName)
}

func getUpdateBody(ctx context.Context, client *http.Client, target string, maxBytes int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("prepare update request: %w", err)
	}
	request.Header.Set("User-Agent", "lzpody-updater")
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("update request: %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("update response exceeds size limit")
	}
	return body, nil
}

func updateHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" {
				return fmt.Errorf("refusing insecure update redirect")
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many update redirects")
			}
			return nil
		},
	}
}

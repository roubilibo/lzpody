package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	} else if strings.Contains(contentType, "json") {
		return nil, &PodmanError{Message: "Podman returned invalid JSON"}
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

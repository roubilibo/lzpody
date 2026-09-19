package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type PodmanError struct{ Message string }

func (e *PodmanError) Error() string { return e.Message }

type PodmanClient struct {
	SocketPath string
	APIRoot    string
	HTTP       *http.Client
}

type containerCreateOptions struct {
	Image, Name, Command, Pod                            string
	Ports, Environment, Mounts, Networks                 []string
	Restart, CPU, Memory, User, Workdir                  string
	Capabilities, Secrets, Labels, Devices, SecurityOpts []string
	Healthcheck                                          string
}

type networkCreateOptions struct {
	Name, Driver, Subnet, Gateway, IPRange, Labels, Options string
	IPv6, Internal                                          bool
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
	return c.requestWithContentType(method, path, query, body, "application/json")
}

func (c *PodmanClient) requestWithContentType(method, path string, query url.Values, body io.Reader, contentType string) (any, error) {
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
	req.Header.Set("Content-Type", contentType)
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
	responseType := strings.ToLower(resp.Header.Get("Content-Type"))
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	} else if strings.Contains(responseType, "json") {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var streamed any
			if json.Unmarshal([]byte(line), &streamed) == nil {
				value = streamed
			}
		}
		if value != nil {
			return value, nil
		}
		return nil, &PodmanError{Message: "Podman returned invalid JSON"}
	}
	return string(raw), nil
}

func (c *PodmanClient) postValue(path string, query url.Values, body io.Reader, contentType string) (any, error) {
	return c.requestWithContentType(http.MethodPost, path, query, body, contentType)
}

func (c *PodmanClient) postJSON(path string, query url.Values, value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, &PodmanError{Message: "Cannot encode request: " + err.Error()}
	}
	return c.postValue(path, query, bytes.NewReader(raw), "application/json")
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

func (c *PodmanClient) secrets() ([]map[string]any, error) {
	v, err := c.get(c.APIRoot+"/secrets/json", nil)
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
	return decodePodmanLogStream(valueText(v)), nil
}

func decodePodmanLogStream(value string) string {
	data := []byte(value)
	if len(data) < 8 {
		return strings.ToValidUTF8(value, "")
	}

	var output strings.Builder
	for len(data) > 0 {
		if len(data) < 8 || (data[0] != 1 && data[0] != 2) || data[1] != 0 || data[2] != 0 || data[3] != 0 {
			return strings.ToValidUTF8(value, "")
		}
		frameSize := binary.BigEndian.Uint32(data[4:8])
		if uint64(frameSize) > uint64(len(data)-8) {
			return strings.ToValidUTF8(value, "")
		}
		output.Write(data[8 : 8+int(frameSize)])
		data = data[8+int(frameSize):]
	}

	result := output.String()
	if !utf8.ValidString(result) {
		return strings.ToValidUTF8(result, "")
	}
	return result
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

func (c *PodmanClient) createContainer(image, name, command, pod string) (string, error) {
	return c.createContainerWithOptions(containerCreateOptions{Image: image, Name: name, Command: command, Pod: pod})
}

func (c *PodmanClient) createContainerWithOptions(options containerCreateOptions) (string, error) {
	payload := map[string]any{"image": options.Image}
	if options.Command != "" {
		payload["command"] = []string{"/bin/sh", "-c", options.Command}
	}
	if options.Pod != "" {
		payload["pod"] = options.Pod
	}
	if env := parseEnvironment(options.Environment); len(env) > 0 {
		payload["env"] = env
	}
	if ports := parsePortMappings(options.Ports); len(ports) > 0 {
		payload["portmappings"] = ports
	}
	if mounts := parseMounts(options.Mounts); len(mounts) > 0 {
		payload["mounts"] = mounts
	}
	if networkMap := parseNetworks(options.Networks); len(networkMap) > 0 {
		payload["networks"] = networkMap
	}
	if options.Restart != "" {
		payload["restart_policy"] = options.Restart
	}
	if options.User != "" {
		payload["user"] = options.User
	}
	if options.Workdir != "" {
		payload["work_dir"] = options.Workdir
	}
	if options.CPU != "" || options.Memory != "" {
		limits := map[string]any{}
		if options.Memory != "" {
			limits["memory"] = map[string]any{"limit": memoryLimitValue(options.Memory)}
		}
		if options.CPU != "" {
			limits["cpu"] = map[string]any{"cpus": options.CPU}
		}
		payload["resource_limits"] = limits
	}
	if caps := parseCapabilities(options.Capabilities); len(caps["add"]) > 0 || len(caps["drop"]) > 0 {
		payload["cap_add"] = caps["add"]
		payload["cap_drop"] = caps["drop"]
	}
	if options.Healthcheck != "" {
		payload["healthconfig"] = map[string]any{"test": []string{"CMD-SHELL", options.Healthcheck}}
	}
	if secrets := compactValues(options.Secrets); len(secrets) > 0 {
		payload["secrets"] = parseSecrets(secrets)
	}
	if labels := parseKeyValues(options.Labels); len(labels) > 0 {
		payload["labels"] = labels
	}
	if devices := parseDevices(options.Devices); len(devices) > 0 {
		payload["devices"] = devices
	}
	if security := compactValues(options.SecurityOpts); len(security) > 0 {
		payload["security_opt"] = security
	}
	query := url.Values{}
	if options.Name != "" {
		query.Set("name", options.Name)
		// Newer Libpod schemas read the name from SpecGenerator while older
		// API versions read it from the query string. Send both for compatibility.
		payload["name"] = options.Name
	}
	value, err := c.postJSON(c.APIRoot+"/containers/create", query, payload)
	if err != nil {
		return "", err
	}
	object, _ := value.(map[string]any)
	id := defaultText(object["Id"], object["ID"])
	if id == "" {
		return "", &PodmanError{Message: "Podman did not return a container ID"}
	}
	if err := c.post(c.APIRoot+"/containers/"+url.PathEscape(id)+"/start", nil); err != nil {
		return "", err
	}
	return id, nil
}

func memoryLimitValue(value string) any {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return int64(0)
	}
	multiplier := float64(1)
	for _, unit := range []struct {
		suffix     string
		multiplier float64
	}{{"kb", 1 << 10}, {"mb", 1 << 20}, {"gb", 1 << 30}, {"tb", 1 << 40}, {"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"t", 1 << 40}} {
		if strings.HasSuffix(trimmed, unit.suffix) {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, unit.suffix))
			multiplier = unit.multiplier
			break
		}
	}
	amount, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return value
	}
	return int64(amount * multiplier)
}

func compactValues(values []string) []string {
	result := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func parseEnvironment(values []string) map[string]string {
	result := map[string]string{}
	for _, value := range compactValues(values) {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			result[strings.TrimSpace(parts[0])] = parts[1]
		}
	}
	return result
}

func parseKeyValues(values []string) map[string]string {
	result := map[string]string{}
	for _, value := range compactValues(values) {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

func parsePortMappings(values []string) []map[string]any {
	result := []map[string]any{}
	for _, value := range compactValues(values) {
		protocol := "tcp"
		if slash := strings.LastIndex(value, "/"); slash >= 0 {
			protocol = value[slash+1:]
			value = value[:slash]
		}
		parts := strings.Split(value, ":")
		if len(parts) == 1 {
			if port, err := strconv.Atoi(parts[0]); err == nil {
				result = append(result, map[string]any{"container_port": port, "protocol": protocol})
			}
			continue
		}
		hostIP := ""
		if len(parts) == 3 {
			hostIP = parts[0]
			parts = parts[1:]
		}
		hostPort, hostErr := strconv.Atoi(parts[0])
		containerPort, containerErr := strconv.Atoi(parts[1])
		if hostErr == nil && containerErr == nil {
			result = append(result, map[string]any{"host_ip": hostIP, "host_port": hostPort, "container_port": containerPort, "protocol": protocol})
		}
	}
	return result
}

func parseMounts(values []string) []map[string]any {
	result := []map[string]any{}
	for _, value := range compactValues(values) {
		parts := strings.Split(value, ":")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		mountType := "volume"
		if strings.HasPrefix(parts[0], "/") || strings.HasPrefix(parts[0], ".") {
			mountType = "bind"
		}
		mount := map[string]any{"source": parts[0], "destination": parts[1], "type": mountType}
		if len(parts) > 2 && parts[2] != "" {
			mount["options"] = strings.Split(parts[2], ",")
		}
		result = append(result, mount)
	}
	return result
}

func parseCapabilities(values []string) map[string][]string {
	result := map[string][]string{"add": {}, "drop": {}}
	for _, value := range compactValues(values) {
		parts := strings.SplitN(value, ":", 2)
		if len(parts) == 2 && (parts[0] == "add" || parts[0] == "drop") {
			result[parts[0]] = append(result[parts[0]], compactValues(strings.Split(parts[1], ","))...)
		}
	}
	return result
}

func parseDevices(values []string) []map[string]any {
	result := []map[string]any{}
	for _, value := range compactValues(values) {
		parts := strings.Split(value, ":")
		if len(parts) < 2 {
			continue
		}
		device := map[string]any{"path_on_host": parts[0], "path_in_container": parts[1]}
		if len(parts) > 2 && parts[2] != "" {
			device["cgroup_permissions"] = parts[2]
		}
		result = append(result, device)
	}
	return result
}

func parseSecrets(values []string) []map[string]any {
	result := []map[string]any{}
	for _, value := range compactValues(values) {
		parts := strings.Split(value, ",")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		secret := map[string]any{"source": strings.TrimSpace(parts[0])}
		for _, option := range parts[1:] {
			keyValue := strings.SplitN(strings.TrimSpace(option), "=", 2)
			if len(keyValue) != 2 || keyValue[0] == "" || keyValue[1] == "" {
				continue
			}
			switch keyValue[0] {
			case "target", "type", "uid", "gid", "mode":
				secret[keyValue[0]] = keyValue[1]
			}
		}
		result = append(result, secret)
	}
	return result
}

func parseNetworks(values []string) map[string]any {
	result := map[string]any{}
	for _, value := range compactValues(values) {
		parts := strings.Split(value, ",")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		network := map[string]any{}
		aliases := []string{}
		for _, option := range parts[1:] {
			keyValue := strings.SplitN(strings.TrimSpace(option), "=", 2)
			if len(keyValue) != 2 || keyValue[1] == "" {
				continue
			}
			switch keyValue[0] {
			case "ip", "ip6":
				network[keyValue[0]] = keyValue[1]
			case "alias":
				aliases = append(aliases, compactValues(strings.Split(keyValue[1], "+"))...)
			}
		}
		if len(aliases) > 0 {
			network["aliases"] = aliases
		}
		result[strings.TrimSpace(parts[0])] = network
	}
	return result
}

func (c *PodmanClient) createPod(name string) error {
	payload := map[string]any{}
	if name != "" {
		payload["name"] = name
	}
	_, err := c.postJSON(c.APIRoot+"/pods/create", nil, payload)
	return err
}

func (c *PodmanClient) createVolume(name string) error {
	_, err := c.postJSON(c.APIRoot+"/volumes/create", nil, map[string]any{"Name": name, "Driver": "local"})
	return err
}

func (c *PodmanClient) mountVolume(name string) (string, error) {
	value, err := c.postValue(idPath(c.APIRoot+"/volumes", name)+"/mount", nil, nil, "application/json")
	return valueText(value), err
}

func (c *PodmanClient) unmountVolume(name string) error {
	return c.post(idPath(c.APIRoot+"/volumes", name)+"/unmount", nil)
}

func (c *PodmanClient) createNetwork(name string) error {
	return c.createNetworkWithOptions(networkCreateOptions{Name: name, Driver: "bridge"})
}

func (c *PodmanClient) createNetworkWithOptions(options networkCreateOptions) error {
	payload := map[string]any{"name": options.Name, "driver": defaultText(options.Driver, "bridge")}
	if subnets := parseNetworkSubnets(options.Subnet, options.Gateway, options.IPRange); len(subnets) > 0 {
		payload["subnets"] = subnets
	}
	if options.IPv6 {
		payload["ipv6_enabled"] = true
	}
	if options.Internal {
		payload["internal"] = true
	}
	if labels := parseKeyValues(strings.Split(options.Labels, ",")); len(labels) > 0 {
		payload["labels"] = labels
	}
	if driverOptions := parseKeyValues(strings.Split(options.Options, ",")); len(driverOptions) > 0 {
		payload["options"] = driverOptions
	}
	_, err := c.postJSON(c.APIRoot+"/networks/create", nil, payload)
	return err
}

func parseNetworkSubnets(subnetValue, gatewayValue, rangeValue string) []map[string]any {
	subnets := compactValues(strings.Split(subnetValue, ","))
	gateways := compactValues(strings.Split(gatewayValue, ","))
	ranges := compactValues(strings.Split(rangeValue, ","))
	result := []map[string]any{}
	for index, subnet := range subnets {
		entry := map[string]any{"subnet": subnet}
		if index < len(gateways) {
			entry["gateway"] = gateways[index]
		}
		if index < len(ranges) {
			bounds := strings.SplitN(ranges[index], "-", 2)
			if len(bounds) == 2 {
				entry["lease_range"] = map[string]string{"start_ip": strings.TrimSpace(bounds[0]), "end_ip": strings.TrimSpace(bounds[1])}
			}
		}
		result = append(result, entry)
	}
	return result
}

func (c *PodmanClient) createSecret(name, source string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return &PodmanError{Message: "Cannot read secret file: " + err.Error()}
	}
	_, err = c.postValue(c.APIRoot+"/secrets/create", url.Values{"name": {name}}, bytes.NewReader(data), "application/octet-stream")
	return err
}

func (c *PodmanClient) networkConnect(network, container string) error {
	_, err := c.postJSON(idPath(c.APIRoot+"/networks", network)+"/connect", nil, map[string]any{"container": container})
	return err
}

func (c *PodmanClient) networkDisconnect(network, container string) error {
	_, err := c.postJSON(idPath(c.APIRoot+"/networks", network)+"/disconnect", nil, map[string]any{"container": container})
	return err
}

func (c *PodmanClient) pullImage(reference string) error {
	value, err := c.postValue(c.APIRoot+"/images/pull", url.Values{"reference": {reference}}, nil, "application/json")
	if err != nil {
		return err
	}
	if object, ok := value.(map[string]any); ok {
		if message := scalarText(object["error"]); message != "" {
			return &PodmanError{Message: "Image pull failed: " + message}
		}
	}
	return nil
}

func (c *PodmanClient) pullImageStream(reference string) (io.ReadCloser, error) {
	path := c.APIRoot + "/images/pull?" + url.Values{"reference": {reference}}.Encode()
	req, err := http.NewRequest(http.MethodPost, "http://podman"+path, nil)
	if err != nil {
		return nil, &PodmanError{Message: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	streamClient := *c.HTTP
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
			return nil, &PodmanError{Message: fmt.Sprintf("Podman socket not found: %s. Recover with: systemctl --user restart podman.socket", c.SocketPath)}
		}
		return nil, &PodmanError{Message: "Cannot connect to Podman: " + err.Error()}
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, &PodmanError{Message: "Cannot read Podman response: " + readErr.Error()}
		}
		message := strings.TrimSpace(string(raw))
		if len(message) > 240 {
			message = message[:237] + "..."
		}
		if message == "" {
			message = resp.Status
		}
		return nil, &PodmanError{Message: fmt.Sprintf("Podman API %d: %s", resp.StatusCode, message)}
	}
	return resp.Body, nil
}

func (c *PodmanClient) pushImage(source, destination string) error {
	value, err := c.postValue(idPath(c.APIRoot+"/images", source)+"/push", url.Values{"destination": {destination}}, nil, "application/json")
	if err != nil {
		return err
	}
	if object, ok := value.(map[string]any); ok {
		if message := scalarText(object["error"]); message != "" {
			return &PodmanError{Message: "Image push failed: " + message}
		}
	}
	return nil
}

func (c *PodmanClient) pruneImages() error {
	return c.post(c.APIRoot+"/images/prune", nil)
}

func (c *PodmanClient) imageHistory(name string) (any, error) {
	return c.get(idPath(c.APIRoot+"/images", name)+"/history", nil)
}

func (c *PodmanClient) tagImage(name, repository, tag string) error {
	query := url.Values{"repo": {repository}}
	if tag != "" {
		query.Set("tag", tag)
	}
	return c.post(idPath(c.APIRoot+"/images", name)+"/tag", query)
}

func (c *PodmanClient) untagImage(name string) error {
	return c.post(idPath(c.APIRoot+"/images", name)+"/untag", nil)
}

func (c *PodmanClient) searchImages(term string, limit string) (any, error) {
	query := url.Values{"term": {term}}
	if limit != "" {
		query.Set("limit", limit)
	}
	return c.get(c.APIRoot+"/images/search", query)
}

func registryAuthFile() string {
	if path := os.Getenv("REGISTRY_AUTH_FILE"); path != "" {
		return path
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir != "" {
		return filepath.Join(runtimeDir, "containers/auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config/containers/auth.json")
}

func (c *PodmanClient) registryLogin(registry, username, password string) error {
	root := strings.TrimSuffix(c.APIRoot, "/libpod")
	_, err := c.postJSON(root+"/auth", nil, map[string]string{
		"username":      username,
		"password":      password,
		"serveraddress": registry,
	})
	if err != nil {
		return err
	}
	return writeRegistryCredential(registry, username, password)
}

func (c *PodmanClient) registryLogout(registry string) error {
	path := registryAuthFile()
	if path == "" {
		return &PodmanError{Message: "Cannot determine registry auth file"}
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &PodmanError{Message: "Cannot read registry auth file: " + err.Error()}
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return &PodmanError{Message: "Cannot decode registry auth file: " + err.Error()}
	}
	auths, _ := document["auths"].(map[string]any)
	if strings.EqualFold(strings.TrimSpace(registry), "all") || strings.TrimSpace(registry) == "" {
		auths = map[string]any{}
	} else {
		delete(auths, registry)
	}
	document["auths"] = auths
	encoded, _ := json.MarshalIndent(document, "", "  ")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return &PodmanError{Message: "Cannot write registry auth file: " + err.Error()}
	}
	return nil
}

func writeRegistryCredential(registry, username, password string) error {
	path := registryAuthFile()
	if path == "" {
		return &PodmanError{Message: "Cannot determine registry auth file"}
	}
	document := map[string]any{"auths": map[string]any{}}
	if raw, err := os.ReadFile(path); err == nil {
		if decodeErr := json.Unmarshal(raw, &document); decodeErr != nil {
			return &PodmanError{Message: "Cannot decode registry auth file: " + decodeErr.Error()}
		}
	}
	auths, ok := document["auths"].(map[string]any)
	if !ok {
		auths = map[string]any{}
		document["auths"] = auths
	}
	auths[registry] = map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(username + ":" + password))}
	encoded, _ := json.MarshalIndent(document, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return &PodmanError{Message: "Cannot create registry auth directory: " + err.Error()}
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return &PodmanError{Message: "Cannot write registry auth file: " + err.Error()}
	}
	return nil
}

func (c *PodmanClient) saveImage(name, destination string) error {
	resp, err := c.doArchiveRequest(http.MethodGet, idPath(c.APIRoot+"/images", name)+"/get", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	file, err := os.Create(destination)
	if err != nil {
		return &PodmanError{Message: "Cannot create image archive: " + err.Error()}
	}
	if _, err = io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		return &PodmanError{Message: "Cannot save image archive: " + err.Error()}
	}
	return file.Close()
}

func (c *PodmanClient) loadImage(source string) error {
	return c.uploadImageArchive(source, c.APIRoot+"/images/load", nil)
}

func (c *PodmanClient) importImage(source, reference string) error {
	query := url.Values{}
	if reference != "" {
		query.Set("reference", reference)
	}
	return c.uploadImageArchive(source, c.APIRoot+"/images/import", query)
}

func (c *PodmanClient) doArchiveRequest(method, path string, query url.Values, body io.Reader) (*http.Response, error) {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, "http://podman"+path, body)
	if err != nil {
		return nil, &PodmanError{Message: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-tar")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &PodmanError{Message: "Cannot connect to Podman: " + err.Error()}
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &PodmanError{Message: fmt.Sprintf("Podman API %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
	}
	return resp, nil
}

func (c *PodmanClient) uploadImageArchive(source, endpoint string, query url.Values) error {
	file, err := os.Open(source)
	if err != nil {
		return &PodmanError{Message: "Cannot open image archive: " + err.Error()}
	}
	defer file.Close()
	resp, err := c.doArchiveRequest(http.MethodPost, endpoint, query, file)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func (c *PodmanClient) systemPrune(all, volumes, build bool, filters []string) (string, error) {
	query := url.Values{
		"all":     {strconv.FormatBool(all)},
		"volumes": {strconv.FormatBool(volumes)},
		"build":   {strconv.FormatBool(build)},
	}
	for _, filter := range filters {
		if filter = strings.TrimSpace(filter); filter != "" {
			query.Add("filter", filter)
		}
	}
	value, err := c.postValue(c.APIRoot+"/system/prune", query, nil, "application/json")
	if err != nil {
		return "", err
	}
	if value == nil {
		return "No unused resources were pruned.", nil
	}
	return valueText(value), nil
}

func (c *PodmanClient) pruneResource(kind string) error {
	if kind != "pods" && kind != "volumes" && kind != "networks" {
		return fmt.Errorf("unsupported prune resource: %s", kind)
	}
	return c.post(c.APIRoot+"/"+kind+"/prune", nil)
}

func (c *PodmanClient) buildImage(contextDir, tag string) error {
	archive, err := tarDirectory(contextDir)
	if err != nil {
		return err
	}
	query := url.Values{}
	if tag != "" {
		query.Set("t", tag)
	}
	value, err := c.postValue(c.APIRoot+"/build", query, bytes.NewReader(archive), "application/x-tar")
	if err != nil {
		return err
	}
	if object, ok := value.(map[string]any); ok {
		if message := scalarText(object["error"]); message != "" {
			return &PodmanError{Message: "Image build failed: " + message}
		}
	}
	return nil
}

func tarDirectory(root string) ([]byte, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, &PodmanError{Message: "Invalid build context: " + err.Error()}
	}
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(writer, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
		return nil
	})
	closeErr := writer.Close()
	if err != nil {
		return nil, &PodmanError{Message: "Cannot archive build context: " + err.Error()}
	}
	if closeErr != nil {
		return nil, &PodmanError{Message: "Cannot finish build context: " + closeErr.Error()}
	}
	return output.Bytes(), nil
}

func (c *PodmanClient) execCommand(id, command string) (string, error) {
	value, err := c.postJSON(c.APIRoot+"/containers/"+url.PathEscape(id)+"/exec", nil, map[string]any{
		"AttachStdout": true, "AttachStderr": true, "Tty": false,
		"Cmd": []string{"/bin/sh", "-c", command},
	})
	if err != nil {
		return "", err
	}
	object, _ := value.(map[string]any)
	execID := defaultText(object["Id"], object["ID"])
	if execID == "" {
		return "", &PodmanError{Message: "Podman did not return an exec ID"}
	}
	value, err = c.postJSON(c.APIRoot+"/exec/"+url.PathEscape(execID)+"/start", nil, map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(valueText(value)), nil
}

func (c *PodmanClient) copyToContainer(id, source, destination string) error {
	file, err := os.Open(source)
	if err != nil {
		return &PodmanError{Message: "Cannot open local file: " + err.Error()}
	}
	defer file.Close()
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	info, err := file.Stat()
	if err != nil {
		return &PodmanError{Message: "Cannot stat local file: " + err.Error()}
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return &PodmanError{Message: "Cannot archive local file: " + err.Error()}
	}
	header.Name = filepath.Base(source)
	if err := writer.WriteHeader(header); err != nil {
		return &PodmanError{Message: "Cannot archive local file: " + err.Error()}
	}
	if _, err := io.Copy(writer, file); err != nil {
		return &PodmanError{Message: "Cannot archive local file: " + err.Error()}
	}
	if err := writer.Close(); err != nil {
		return &PodmanError{Message: "Cannot finish file archive: " + err.Error()}
	}
	_, err = c.requestWithContentType(http.MethodPut, idPath(c.APIRoot+"/containers", id)+"/archive", url.Values{"path": {destination}}, bytes.NewReader(archive.Bytes()), "application/x-tar")
	return err
}

func (c *PodmanClient) copyFromContainer(id, source, destination string) error {
	value, err := c.get(idPath(c.APIRoot+"/containers", id)+"/archive", url.Values{"path": {source}})
	if err != nil {
		return err
	}
	archive := []byte(valueText(value))
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return &PodmanError{Message: "Cannot read container archive: " + err.Error()}
		}
		cleanName := filepath.Clean(header.Name)
		if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) || filepath.IsAbs(cleanName) {
			return &PodmanError{Message: "Container archive contains an unsafe path"}
		}
		target := destination
		if info, statErr := os.Stat(destination); statErr == nil && info.IsDir() {
			target = filepath.Join(destination, cleanName)
		}
		if header.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, header.FileInfo().Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (c *PodmanClient) info() (any, error) {
	return c.get(c.APIRoot+"/info", nil)
}

func (c *PodmanClient) events(since time.Time) (any, error) {
	return c.get(c.APIRoot+"/events", url.Values{"stream": {"false"}, "since": {since.UTC().Format(time.RFC3339Nano)}})
}

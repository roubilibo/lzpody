package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (a *App) beginPrompt(action string) {
	var form *containerFormState
	switch action {
	case "run":
		form = newContainerForm()
	case "create_network":
		form = newNetworkForm()
	}
	if form != nil {
		a.ContainerForm = form
		a.Prompt = nil
		a.MenuOpen = false
		a.FocusMain = true
		a.DetailMode = "create"
		a.CursorVisible = true
		return
	}
	steps := map[string][]promptStep{
		"pull": {{label: "Image reference"}},
		"run": {
			{label: "Image"},
			{label: "Name (optional)"},
			{label: "Command (optional)"},
			{label: "Pod (optional)"},
			{label: "Ports host:container/protocol (optional)"},
			{label: "Environment KEY=value,... (optional)"},
			{label: "Mounts source:destination[:ro] (optional)"},
			{label: "Networks network[,ip=...,ip6=...,alias=a+b];... (optional)"},
			{label: "Restart policy (optional)"},
			{label: "CPU limit (optional)"},
			{label: "Memory limit (optional)"},
			{label: "User (optional)"},
			{label: "Workdir (optional)"},
			{label: "Capabilities add:CAP,drop:CAP (optional)"},
			{label: "Healthcheck command (optional)"},
			{label: "Secrets name[,type=...,target=...];... (optional)"},
			{label: "Labels key=value,... (optional)"},
			{label: "Devices host:container[:rwm] (optional)"},
			{label: "Security options comma-separated (optional)"},
		},
		"build":              {{label: "Context directory | image tag (tag optional)"}},
		"push":               {{label: "Local image | registry destination"}},
		"image_tag":          {{label: "Repository | tag (optional)"}},
		"image_search":       {{label: "Search term | limit (optional)"}},
		"image_save":         {{label: "Destination .tar path"}},
		"image_load":         {{label: "Source .tar path"}},
		"image_import":       {{label: "Source .tar path | reference (optional)"}},
		"registry_login":     {{label: "Registry"}, {label: "Username"}, {label: "Password", sensitive: true}},
		"registry_logout":    {{label: "Registry, or ALL"}},
		"create_pod":         {{label: "Pod name"}},
		"create_volume":      {{label: "Volume name"}},
		"create_network":     {{label: "Network name"}, {label: "Driver (bridge/macvlan/ipvlan)"}, {label: "Subnet(s), comma-separated"}, {label: "Gateway(s), comma-separated"}, {label: "IP range(s), comma-separated"}, {label: "IPv6? yes/no"}, {label: "Internal? yes/no"}, {label: "Labels key=value,... | driver options key=value,..."}},
		"create_secret":      {{label: "Secret name"}, {label: "Secret file path"}},
		"exec":               {{label: "Command"}},
		"rename":             {{label: "New container name"}},
		"wait":               {{label: "Condition (configured/created/exited/paused/running/stopped)"}},
		"export":             {{label: "Destination .tar path"}},
		"checkpoint":         {{label: "Checkpoint destination .tar.gz path"}},
		"restore":            {{label: "Checkpoint source .tar.gz path"}},
		"copy_to":            {{label: "Local file | container path"}},
		"copy_from":          {{label: "Container path | local path"}},
		"network_connect":    {{label: "Container name or ID"}},
		"network_disconnect": {{label: "Container name or ID"}},
		"prune_images":       {{label: "Type YES to prune unused images"}},
		"prune_pods":         {{label: "Type YES to prune unused pods"}},
		"prune_volumes":      {{label: "Type YES to prune unused volumes"}},
		"prune_networks":     {{label: "Type YES to prune unused networks"}},
		"system_prune":       {{label: "YES | all | volumes | build | filters (comma-separated)"}},
		"system_check":       {{label: "quick yes/no | repair yes/no | repair-lossy yes/no | max age (optional)"}},
		"settings":           {{label: "socket or http(s) URL | refresh seconds | theme | log limit | confirm yes/no"}},
		"commit":             {{label: "Repository | tag | comment"}},
		"kube_play":          {{label: "YAML path | network | start yes/no"}},
		"kube_down":          {{label: "YAML path | force yes/no"}},
		"kube_generate":      {{label: "Destination YAML path (optional) | service yes/no"}},
		"manifest_create":    {{label: "Manifest name | image (optional) | all yes/no"}},
		"manifest_add":       {{label: "Manifest name | images comma-separated | arch | os | variant"}},
		"manifest_push":      {{label: "Destination | all yes/no"}},
	}
	if selected, ok := steps[action]; ok {
		a.Prompt = &promptState{action: action, steps: selected}
		a.MenuOpen = false
		a.FocusMain = false
	}
}

func newContainerForm() *containerFormState {
	return &containerFormState{action: "run", title: "Create container", tabs: []string{"Basic", "Network", "Runtime", "Security"}, fields: []containerFormField{
		{label: "Image", hint: "reference, e.g. alpine:latest", tab: 0},
		{label: "Name", hint: "container name, e.g. web", tab: 0},
		{label: "Command", hint: "command, e.g. nginx -g 'daemon off;'", tab: 0},
		{label: "Pod", hint: "pod name, e.g. pod-name", tab: 0},
		{label: "Ports", hint: "host:container/protocol, e.g. 8080:80/tcp", tab: 1},
		{label: "Environment", hint: "KEY=value, e.g. APP_ENV=prod", tab: 1},
		{label: "Mounts", hint: "source:destination[:ro], e.g. /host:/container:ro", tab: 1},
		{label: "Networks", hint: "network options, e.g. frontend,alias=web", tab: 1},
		{label: "Restart policy", hint: "policy, e.g. unless-stopped", tab: 2},
		{label: "CPU limit", hint: "number of cores, e.g. 1.0", tab: 2},
		{label: "Memory limit", hint: "memory size, e.g. 512m", tab: 2},
		{label: "User", hint: "UID:GID, e.g. 1000:1000", tab: 2},
		{label: "Workdir", hint: "path, e.g. /app", tab: 2},
		{label: "Healthcheck", hint: "command, e.g. CMD-SHELL,curl -f http://localhost", tab: 2},
		{label: "Capabilities", hint: "add/drop capability, e.g. add:NET_ADMIN", tab: 3},
		{label: "Secrets", hint: "name and target, e.g. db-secret,target=/run/secrets/db", tab: 3},
		{label: "Labels", hint: "key=value, e.g. app=web", tab: 3},
		{label: "Devices", hint: "host:container[:rwm], e.g. /dev/kvm:/dev/kvm:rwm", tab: 3},
		{label: "Security options", hint: "option, e.g. no-new-privileges", tab: 3},
	}, activeField: 0}
}

func newNetworkForm() *containerFormState {
	return &containerFormState{action: "create_network", title: "Create network", tabs: []string{"Network"}, fields: []containerFormField{
		{label: "Name", hint: "network name, e.g. frontend", tab: 0},
		{label: "Driver", hint: "bridge, macvlan, or ipvlan", tab: 0},
		{label: "Subnets", hint: "CIDR list, e.g. 10.89.0.0/24", tab: 0},
		{label: "Gateways", hint: "gateway list, e.g. 10.89.0.1", tab: 0},
		{label: "IP ranges", hint: "range list, e.g. 10.89.0.10/28", tab: 0},
		{label: "IPv6", hint: "yes or no", tab: 0},
		{label: "Internal", hint: "yes or no", tab: 0},
		{label: "Labels | options", hint: "key=value | key=value, e.g. app=web | mtu=1500", tab: 0},
	}, activeField: 0}
}

func (f *containerFormState) values() []string {
	values := make([]string, len(f.fields))
	for index := range f.fields {
		values[index] = f.fields[index].value
	}
	return values
}

func (f *containerFormState) fieldIndexes(tab int) []int {
	indexes := []int{}
	for index, field := range f.fields {
		if field.tab == tab {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func (m Model) updatePrompt(message tea.KeyMsg) tea.Cmd {
	prompt := m.app.Prompt
	if prompt == nil {
		return nil
	}
	switch message.String() {
	case "esc", "ctrl+c":
		m.app.Prompt = nil
		return nil
	case "enter":
		value := strings.TrimSpace(prompt.value)
		prompt.values = append(prompt.values, value)
		if len(prompt.values) < len(prompt.steps) {
			prompt.value = ""
			return nil
		}
		m.app.Prompt = nil
		return m.promptCommand(prompt.action, prompt.values)
	case "backspace", "delete":
		if len(prompt.value) > 0 {
			runes := []rune(prompt.value)
			prompt.value = string(runes[:len(runes)-1])
		}
	case "space", " ":
		prompt.value += " "
	default:
		if message.Type == tea.KeyRunes {
			prompt.value += string(message.Runes)
		}
	}
	return nil
}

func (m Model) updateContainerForm(message tea.KeyMsg) tea.Cmd {
	form := m.app.ContainerForm
	if form == nil {
		return nil
	}
	moveField := func(delta int) {
		indexes := form.fieldIndexes(form.tab)
		if len(indexes) == 0 {
			return
		}
		position := 0
		for index, field := range indexes {
			if field == form.activeField {
				position = index
				break
			}
		}
		position = (position + delta + len(indexes)) % len(indexes)
		form.activeField = indexes[position]
	}
	switch message.String() {
	case "esc", "ctrl+c":
		m.app.ContainerForm = nil
		m.app.CursorVisible = false
		m.app.DetailMode = "summary"
		m.app.FocusMain = false
		return nil
	case "ctrl+s", "ctrl+enter":
		values := form.values()
		m.app.ContainerForm = nil
		m.app.CursorVisible = false
		m.app.DetailMode = "summary"
		m.app.FocusMain = false
		return m.promptCommand(form.action, values)
	case "tab", "enter", "down":
		moveField(1)
	case "shift+tab", "up":
		moveField(-1)
	case "ctrl+right", "]":
		form.tab = (form.tab + 1) % len(form.tabs)
		form.activeField = form.fieldIndexes(form.tab)[0]
	case "ctrl+left", "[":
		form.tab = (form.tab + len(form.tabs) - 1) % len(form.tabs)
		form.activeField = form.fieldIndexes(form.tab)[0]
	case "backspace", "delete":
		field := &form.fields[form.activeField]
		if len(field.value) > 0 {
			runes := []rune(field.value)
			field.value = string(runes[:len(runes)-1])
		}
	case "space", " ":
		form.fields[form.activeField].value += " "
	default:
		if message.Type == tea.KeyRunes {
			form.fields[form.activeField].value += string(message.Runes)
		}
	}
	return nil
}

func splitPrompt(value string, count int) []string {
	parts := strings.SplitN(value, "|", count)
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func promptValue(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return strings.TrimSpace(values[index])
}

func parseContainerCreate(values []string) containerCreateOptions {
	options := containerCreateOptions{
		Image:        promptValue(values, 0),
		Name:         promptValue(values, 1),
		Command:      promptValue(values, 2),
		Pod:          promptValue(values, 3),
		Ports:        strings.Split(promptValue(values, 4), ","),
		Environment:  strings.Split(promptValue(values, 5), ","),
		Mounts:       strings.Split(promptValue(values, 6), ","),
		Networks:     strings.Split(promptValue(values, 7), ";"),
		Restart:      promptValue(values, 8),
		CPU:          promptValue(values, 9),
		Memory:       promptValue(values, 10),
		User:         promptValue(values, 11),
		Workdir:      promptValue(values, 12),
		Capabilities: strings.Split(promptValue(values, 13), ","),
		Healthcheck:  promptValue(values, 14),
		Secrets:      strings.Split(promptValue(values, 15), ";"),
		Labels:       strings.Split(promptValue(values, 16), ","),
		Devices:      strings.Split(promptValue(values, 17), ","),
		SecurityOpts: strings.Split(promptValue(values, 18), ","),
	}
	return options
}

func parseNetworkCreate(values []string) networkCreateOptions {
	labelsOptions := splitPrompt(promptValue(values, 7), 2)
	labels, options := "", ""
	if len(labelsOptions) > 0 {
		labels = labelsOptions[0]
	}
	if len(labelsOptions) > 1 {
		options = labelsOptions[1]
	}
	return networkCreateOptions{
		Name: promptValue(values, 0), Driver: promptValue(values, 1),
		Subnet: promptValue(values, 2), Gateway: promptValue(values, 3), IPRange: promptValue(values, 4),
		IPv6: strings.EqualFold(promptValue(values, 5), "yes"), Internal: strings.EqualFold(promptValue(values, 6), "yes"),
		Labels: labels, Options: options,
	}
}

func (m Model) promptCommand(action string, values []string) tea.Cmd {
	if action == "settings" {
		return func() tea.Msg {
			message := bubbleActionMsg{action: action, name: "settings"}
			config := m.app.Config
			parts := splitPrompt(promptValue(values, 0), 5)
			if len(parts) > 0 && parts[0] != "" {
				if strings.HasPrefix(parts[0], "http://") || strings.HasPrefix(parts[0], "https://") {
					config.EndpointURL = strings.TrimRight(parts[0], "/")
					config.SocketPath = ""
				} else {
					config.SocketPath = parts[0]
					config.EndpointURL = ""
				}
			}
			if len(parts) > 1 && parts[1] != "" {
				seconds, err := strconv.ParseFloat(parts[1], 64)
				if err != nil || seconds < 0.1 || seconds > 60 {
					message.err = fmt.Errorf("refresh interval must be between 0.1 and 60 seconds")
					return message
				}
				config.RefreshSeconds = seconds
			}
			if len(parts) > 2 && parts[2] != "" {
				config.Theme = parts[2]
			}
			if len(parts) > 3 && parts[3] != "" {
				limit, err := strconv.Atoi(parts[3])
				if err != nil || limit < 20 || limit > 10000 {
					message.err = fmt.Errorf("log limit must be between 20 and 10000 lines")
					return message
				}
				config.LogLimit = limit
			}
			if len(parts) > 4 && parts[4] != "" {
				switch strings.ToLower(parts[4]) {
				case "yes", "true":
					config.ConfirmDestructive = true
				case "no", "false":
					config.ConfirmDestructive = false
				default:
					message.err = fmt.Errorf("confirm value must be yes or no")
					return message
				}
			}
			config = config.normalized()
			message.err = saveUserConfig(config)
			if message.err == nil {
				message.config = &config
				endpoint := config.SocketPath
				if config.EndpointURL != "" {
					endpoint = config.EndpointURL
				}
				message.output = fmt.Sprintf("Settings saved\nEndpoint: %s\nRefresh: %.1fs\nTheme: %s\nLog limit: %d", defaultText(endpoint, "Podman default"), config.RefreshSeconds, defaultText(config.Theme, "automatic"), config.LogLimit)
			}
			return message
		}
	}
	if action == "pull" {
		if len(values) == 0 || values[0] == "" {
			return func() tea.Msg { return pullStartedMsg{err: fmt.Errorf("image reference is required")} }
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.app.Pull = &pullSession{reference: values[0], lines: []string{"Connecting to Podman..."}, cancel: cancel}
		m.app.PullOverlay = true
		m.app.Status = "Connecting to Podman..."
		return m.startPullCmd(ctx, m.app.Pull, values[0])
	}
	if action == "rename" || action == "wait" || action == "export" || action == "checkpoint" || action == "restore" {
		item := m.app.current()
		itemCopy := Item{}
		if item != nil {
			itemCopy = *item
		}
		return func() tea.Msg {
			message := bubbleActionMsg{action: action, itemID: itemCopy.ID, name: itemCopy.Name}
			if itemCopy.Kind != "container" || itemCopy.ID == "" {
				message.err = fmt.Errorf("a selected container is required")
				return message
			}
			switch action {
			case "rename":
				message.err = m.app.Client.renameContainer(itemCopy.ID, promptValue(values, 0))
			case "wait":
				message.output, message.err = m.app.Client.waitContainer(itemCopy.ID, promptValue(values, 0))
			case "export":
				message.err = m.app.Client.exportContainer(itemCopy.ID, promptValue(values, 0))
			case "checkpoint":
				message.err = m.app.Client.checkpointContainer(itemCopy.ID, promptValue(values, 0))
			case "restore":
				message.err = m.app.Client.restoreContainer(itemCopy.ID, promptValue(values, 0))
			}
			return message
		}
	}
	if action == "commit" || action == "kube_play" || action == "kube_down" || action == "kube_generate" || action == "manifest_create" || action == "manifest_add" || action == "manifest_push" {
		item := m.app.current()
		itemCopy := Item{}
		if item != nil {
			itemCopy = *item
		}
		return func() tea.Msg {
			message := bubbleActionMsg{action: action, itemID: itemCopy.ID, name: itemCopy.Name}
			parts := splitPrompt(promptValue(values, 0), 5)
			var output string
			switch action {
			case "commit":
				if itemCopy.Kind != "container" {
					message.err = fmt.Errorf("select a container to commit")
				} else {
					fields := splitPrompt(promptValue(values, 0), 3)
					if len(fields) == 0 || fields[0] == "" {
						message.err = fmt.Errorf("image repository is required")
					} else {
						output, message.err = m.app.Client.commitContainer(itemCopy.ID, fields[0], promptValue(fields, 1), promptValue(fields, 2))
					}
				}
			case "kube_play":
				start := true
				if len(parts) > 2 && parts[2] != "" {
					start = !strings.EqualFold(parts[2], "no") && !strings.EqualFold(parts[2], "false")
				}
				if parts[0] == "" {
					message.err = fmt.Errorf("Kubernetes YAML path is required")
				} else {
					output, message.err = m.app.Client.playKube(parts[0], promptValue(parts, 1), start)
				}
			case "kube_down":
				force := len(parts) > 1 && (strings.EqualFold(parts[1], "yes") || strings.EqualFold(parts[1], "true"))
				if parts[0] == "" {
					message.err = fmt.Errorf("Kubernetes YAML path is required")
				} else {
					output, message.err = m.app.Client.downKube(parts[0], force)
				}
			case "kube_generate":
				if itemCopy.Kind != "container" && itemCopy.Kind != "pod" {
					message.err = fmt.Errorf("select a container or pod to generate Kubernetes YAML")
				} else {
					service := len(parts) > 1 && (strings.EqualFold(parts[1], "yes") || strings.EqualFold(parts[1], "true"))
					output, message.err = m.app.Client.generateKube([]string{itemCopy.ID}, service)
					if message.err == nil && parts[0] != "" {
						message.err = os.WriteFile(parts[0], []byte(output+"\n"), 0o600)
						if message.err == nil {
							output = "Kubernetes YAML saved to " + parts[0]
						}
					}
				}
			case "manifest_create":
				if parts[0] == "" {
					message.err = fmt.Errorf("manifest name is required")
				} else {
					all := len(parts) > 2 && (strings.EqualFold(parts[2], "yes") || strings.EqualFold(parts[2], "true"))
					output, message.err = m.app.Client.createManifest(parts[0], promptValue(parts, 1), all)
				}
			case "manifest_add":
				if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
					message.err = fmt.Errorf("manifest name and images are required")
				} else {
					message.err = m.app.Client.addManifest(parts[0], strings.Split(parts[1], ","), false, promptValue(parts, 2), promptValue(parts, 3), promptValue(parts, 4))
				}
			case "manifest_push":
				if itemCopy.Kind != "image" || parts[0] == "" {
					message.err = fmt.Errorf("select a manifest image and provide a destination")
				} else {
					all := len(parts) > 1 && (strings.EqualFold(parts[1], "yes") || strings.EqualFold(parts[1], "true"))
					output, message.err = m.app.Client.pushManifest(itemCopy.ID, parts[0], all)
				}
			}
			message.output = output
			return message
		}
	}
	if action == "build" || action == "push" || action == "system_prune" || action == "system_check" {
		value := promptValue(values, 0)
		if action == "build" {
			parts := splitPrompt(value, 2)
			if len(parts) == 0 || parts[0] == "" {
				return func() tea.Msg { return bubbleActionMsg{action: action, err: fmt.Errorf("build context is required")} }
			}
		}
		if action == "push" {
			parts := splitPrompt(value, 2)
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				return func() tea.Msg {
					return bubbleActionMsg{action: action, err: fmt.Errorf("source and destination are required")}
				}
			}
		}
		if action == "system_prune" {
			parts := splitPrompt(value, 5)
			if len(parts) == 0 || !strings.EqualFold(parts[0], "yes") {
				return func() tea.Msg {
					return bubbleActionMsg{action: action, err: fmt.Errorf("system prune cancelled: type YES to confirm")}
				}
			}
		}
		titles := map[string]string{"build": "Build image", "push": "Push image", "system_prune": "System prune", "system_check": "Storage check"}
		session, ctx := m.beginOperation(action, titles[action])
		return m.startOperationCmd(action, values, session, ctx)
	}
	client := m.app.Client
	item := m.app.current()
	itemCopy := Item{}
	if item != nil {
		itemCopy = *item
	}
	return func() tea.Msg {
		message := bubbleActionMsg{action: action, itemID: itemCopy.ID, name: itemCopy.Name}
		var err error
		switch action {
		case "run":
			if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
				err = fmt.Errorf("image reference is required")
			} else {
				_, err = client.createContainerWithOptions(parseContainerCreate(values))
			}
		case "image_tag":
			parts := splitPrompt(values[0], 2)
			if itemCopy.Kind != "image" || len(parts) == 0 || parts[0] == "" {
				err = fmt.Errorf("an image and repository are required")
			} else {
				err = client.tagImage(itemCopy.ID, parts[0], promptValue(parts, 1))
			}
		case "image_search":
			parts := splitPrompt(values[0], 2)
			if len(parts) == 0 || parts[0] == "" {
				err = fmt.Errorf("search term is required")
			} else {
				var value any
				value, err = client.searchImages(parts[0], promptValue(parts, 1))
				if err == nil {
					message.output = valueText(value)
				}
			}
		case "image_save":
			if itemCopy.Kind != "image" || len(values) == 0 || values[0] == "" {
				err = fmt.Errorf("an image and destination path are required")
			} else {
				err = client.saveImage(itemCopy.ID, values[0])
			}
		case "image_load":
			err = client.loadImage(promptValue(values, 0))
		case "image_import":
			parts := splitPrompt(values[0], 2)
			if len(parts) == 0 || parts[0] == "" {
				err = fmt.Errorf("image archive path is required")
			} else {
				err = client.importImage(parts[0], promptValue(parts, 1))
			}
		case "registry_login":
			if len(values) < 3 || values[0] == "" || values[1] == "" || values[2] == "" {
				err = fmt.Errorf("registry, username, and password are required")
			} else {
				err = client.registryLogin(values[0], values[1], values[2])
			}
		case "registry_logout":
			err = client.registryLogout(promptValue(values, 0))
		case "create_pod":
			err = client.createPod(values[0])
		case "create_volume":
			err = client.createVolume(values[0])
		case "create_network":
			err = client.createNetworkWithOptions(parseNetworkCreate(values))
		case "create_secret":
			if len(values) < 2 || values[0] == "" || values[1] == "" {
				err = fmt.Errorf("secret name and file path are required")
			} else {
				err = client.createSecret(values[0], values[1])
			}
		case "exec":
			if itemCopy.ID == "" || values[0] == "" {
				err = fmt.Errorf("a selected container and command are required")
			} else {
				message.output, err = client.execCommand(itemCopy.ID, values[0])
			}
		case "copy_to":
			parts := splitPrompt(values[0], 2)
			if itemCopy.ID == "" || len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				err = fmt.Errorf("a selected container, local file, and destination are required")
			} else {
				err = client.copyToContainer(itemCopy.ID, parts[0], parts[1])
			}
		case "copy_from":
			parts := splitPrompt(values[0], 2)
			if itemCopy.ID == "" || len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				err = fmt.Errorf("a selected container, source, and local path are required")
			} else {
				err = client.copyFromContainer(itemCopy.ID, parts[0], parts[1])
			}
		case "network_connect":
			if itemCopy.Kind != "network" || values[0] == "" {
				err = fmt.Errorf("a selected network and container are required")
			} else {
				err = client.networkConnect(itemCopy.Name, values[0])
			}
		case "network_disconnect":
			if itemCopy.Kind != "network" || values[0] == "" {
				err = fmt.Errorf("a selected network and container are required")
			} else {
				err = client.networkDisconnect(itemCopy.Name, values[0])
			}
		case "prune_images":
			if strings.EqualFold(values[0], "yes") {
				err = client.pruneImages()
			} else {
				err = fmt.Errorf("image prune cancelled")
			}
		case "prune_pods", "prune_volumes", "prune_networks":
			if strings.EqualFold(values[0], "yes") {
				err = client.pruneResource(strings.TrimPrefix(action, "prune_"))
			} else {
				err = fmt.Errorf("%s prune cancelled", strings.TrimPrefix(action, "prune_"))
			}
		}
		message.err = err
		return message
	}
}

func (m Model) volumeCommand(action string) tea.Cmd {
	item := m.app.current()
	if item == nil {
		return nil
	}
	client := m.app.Client
	itemCopy := *item
	return func() tea.Msg {
		message := bubbleActionMsg{action: action, itemID: itemCopy.ID, name: itemCopy.Name}
		if action == "volume_mount" {
			message.output, message.err = client.mountVolume(itemCopy.Name)
		} else {
			message.err = client.unmountVolume(itemCopy.Name)
		}
		return message
	}
}

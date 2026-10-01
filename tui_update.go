package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case cursorBlinkMsg:
		if m.app.ContainerForm != nil {
			m.app.CursorVisible = !m.app.CursorVisible
		}
		return m, cursorBlinkCmd()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeShell()
		return m, nil
	case shellStartedMsg:
		if msg.err != nil {
			m.app.Status = "Cannot open shell: " + msg.err.Error()
			return m, nil
		}
		m.app.Shell = msg.session
		m.app.DetailMode = "shell"
		m.app.DetailLines = []string{"Connected to " + msg.session.name + ".", ""}
		m.app.DetailRawLines = append([]string(nil), m.app.DetailLines...)
		m.app.DetailScroll = 0
		m.app.FocusMain = true
		m.app.Status = "Shell: " + msg.session.name + " (Esc to close)"
		m.resizeShell()
		return m, shellReadCmd(msg.session)
	case shellOutputMsg:
		if m.app.Shell != msg.session {
			return m, nil
		}
		_, _ = m.app.Shell.emulator.Write([]byte(msg.output))
		m.app.DetailLines = shellSnapshot(m.app.Shell)
		m.app.DetailRawLines = append([]string(nil), m.app.DetailLines...)
		m.app.DetailScroll = max(0, len(m.app.DetailLines)-max(1, m.app.DetailViewRows))
		return m, shellReadCmd(msg.session)
	case shellExitedMsg:
		if m.app.Shell != msg.session {
			return m, nil
		}
		m.app.Shell = nil
		if msg.err != nil {
			m.app.Status = "Shell exited: " + msg.err.Error()
		} else {
			m.app.Status = "Shell exited."
		}
		return m, nil
	case bubbleCapabilitiesMsg:
		if msg.err == nil {
			m.app.Capabilities = msg.capabilities
			m.app.CapabilitiesSet = true
		}
		return m, nil
	case pullStartedMsg:
		if msg.session != nil && m.app.Pull != msg.session {
			if msg.body != nil {
				_ = msg.body.Close()
			}
			return m, nil
		}
		if msg.err != nil {
			if msg.body != nil {
				_ = msg.body.Close()
			}
			m.app.Pull = nil
			m.app.PullOverlay = false
			m.app.Status = msg.err.Error()
			return m, nil
		}
		if m.app.Pull == nil || !m.app.PullOverlay {
			if msg.body != nil {
				_ = msg.body.Close()
			}
			return m, nil
		}
		msg.session.body = msg.body
		msg.session.scanner = msg.scanner
		m.app.Pull = msg.session
		m.app.PullOverlay = true
		m.app.Status = "Pulling " + msg.session.reference + "..."
		return m, pullReadCmd(msg.session)
	case operationStreamStartedMsg:
		if m.app.Operation != msg.session {
			if msg.body != nil {
				_ = msg.body.Close()
			}
			return m, nil
		}
		if msg.err != nil {
			m.finishOperation(msg.session, msg.err)
			return m, nil
		}
		if msg.session.done {
			_ = msg.body.Close()
			return m, nil
		}
		msg.session.body = msg.body
		msg.session.scanner = msg.scanner
		return m, operationReadCmd(msg.session)
	case operationProgressMsg:
		if m.app.Operation != msg.session || msg.session.done {
			return m, nil
		}
		if msg.line != "" {
			msg.session.lines = appendOperationLines(msg.session.lines, msg.line)
		}
		if msg.done {
			m.finishOperation(msg.session, msg.err)
			if msg.session.err == nil && !msg.session.cancelRequested {
				return m, m.immediateRefreshCmdWithStatus(msg.session.title + " complete")
			}
			return m, nil
		}
		return m, operationReadCmd(msg.session)
	case statsStreamStartedMsg:
		if m.app.StatsStream != msg.session {
			if msg.body != nil {
				_ = msg.body.Close()
			}
			return m, nil
		}
		if msg.err != nil {
			m.app.stopStatsStream()
			m.app.Status = "Live stats failed: " + msg.err.Error()
			return m, nil
		}
		msg.session.body = msg.body
		msg.session.scan = msg.scan
		m.app.Status = "Live stats: " + msg.session.itemID
		return m, statsStreamReadCmd(msg.session)
	case statsStreamSampleMsg:
		if m.app.StatsStream != msg.session {
			return m, nil
		}
		if msg.err != nil {
			m.app.stopStatsStream()
			m.app.Status = "Live stats failed: " + msg.err.Error()
			return m, nil
		}
		if msg.done {
			m.app.stopStatsStream()
			m.app.Status = "Live stats ended."
			return m, nil
		}
		if msg.sample != nil {
			history := append(m.app.StatsHistory[msg.session.itemID], msg.sample)
			if len(history) > 60 {
				history = history[len(history)-60:]
			}
			m.app.StatsHistory[msg.session.itemID] = history
			m.app.DetailRawLines = statsLines(history)
			m.app.DetailLines = append([]string(nil), m.app.DetailRawLines...)
			m.app.reflowDetail(m.app.DetailViewWidth)
			m.app.Status = "Live stats: " + msg.session.itemID
		}
		return m, statsStreamReadCmd(msg.session)
	case eventStreamStartedMsg:
		if m.app.EventStream != msg.session {
			if msg.body != nil {
				_ = msg.body.Close()
			}
			return m, nil
		}
		if msg.err != nil {
			m.app.stopEventStream()
			m.app.Status = "Live events failed: " + msg.err.Error()
			return m, nil
		}
		msg.session.body = msg.body
		msg.session.scan = msg.scan
		m.app.Status = "Live events connected"
		return m, eventStreamReadCmd(msg.session)
	case eventStreamLineMsg:
		if m.app.EventStream != msg.session {
			return m, nil
		}
		if msg.err != nil {
			m.app.stopEventStream()
			m.app.Status = "Live events failed: " + msg.err.Error()
			return m, nil
		}
		if msg.done {
			m.app.stopEventStream()
			m.app.Status = "Live events ended."
			return m, nil
		}
		if msg.line != "" && (m.app.EventFilter == "" || strings.Contains(strings.ToLower(msg.line), strings.ToLower(m.app.EventFilter))) {
			m.app.DetailRawLines = append(m.app.DetailRawLines, msg.line)
			limit := m.app.Config.LogLimit
			if limit <= 0 {
				limit = 200
			}
			if len(m.app.DetailRawLines) > limit {
				m.app.DetailRawLines = m.app.DetailRawLines[len(m.app.DetailRawLines)-limit:]
			}
			m.app.DetailLines = append([]string(nil), m.app.DetailRawLines...)
			m.app.reflowDetail(m.app.DetailViewWidth)
			m.app.DetailScroll = max(0, len(m.app.DetailLines)-max(1, m.app.DetailViewRows))
		}
		return m, eventStreamReadCmd(msg.session)
	case pullProgressMsg:
		if m.app.Pull != msg.session {
			return m, nil
		}
		if msg.line != "" {
			msg.session.lines = appendPullLine(msg.session.lines, msg.line)
		}
		if msg.done {
			msg.session.done = true
			msg.session.err = msg.err
			if msg.err != nil {
				m.app.Status = "Pull failed: " + msg.err.Error()
			} else {
				m.app.Status = "Pull complete: " + msg.session.reference
			}
			return m, m.immediateRefreshCmdWithStatus(m.app.Status)
		}
		return m, pullReadCmd(msg.session)
	case bubbleRefreshMsg:
		keepID := ""
		if item := m.app.current(); item != nil {
			keepID = item.ID
		}
		m.app.applyRefresh(msg.result, keepID)
		if msg.statusOverride != "" {
			m.app.Status = msg.statusOverride
		}
		if m.app.current() == nil {
			if m.app.DetailMode != "system" && m.app.DetailMode != "storage" && m.app.DetailMode != "help" && m.app.DetailMode != "events" {
				m.app.DetailLines = []string{"No item selected."}
				m.app.DetailMode = "summary"
			}
		}
		var detailCommand tea.Cmd
		if len(m.app.DetailLines) == 0 || m.app.DetailMode == "logs" || (m.app.DetailMode == "stats" && m.app.StatsStream == nil) || (m.app.DetailMode == "events" && m.app.EventStream == nil) || m.app.DetailMode == "top" || m.app.DetailMode == "system" || m.app.DetailMode == "storage" || m.app.DetailMode == "help" || m.app.DetailMode == "history" {
			detailCommand = m.detailCmd()
		}
		return m, tea.Batch(detailCommand, m.refreshCmd())
	case bubbleDetailMsg:
		m.app.applyDetail(msg.result)
		return m, nil
	case tea.MouseMsg:
		return m, m.mouseUpdate(msg)
	case bubbleActionMsg:
		if msg.err != nil {
			m.app.Status = msg.err.Error()
			return m, nil
		}
		if msg.config != nil {
			m.app.Config = *msg.config
			m.app.RefreshAfter = msg.config.refreshDuration()
			endpoint := msg.config.SocketPath
			if msg.config.EndpointURL != "" {
				endpoint = msg.config.EndpointURL
			}
			if endpoint != "" && endpoint != m.app.Client.SocketPath {
				m.app.Client = NewPodmanClient(endpoint)
			}
		}
		if msg.batch {
			m.app.clearMarked()
		}
		if msg.output != "" {
			m.app.DetailMode = "exec"
			m.app.DetailLines = splitLines(msg.output, "(no output)")
			m.app.DetailRawLines = append([]string(nil), m.app.DetailLines...)
			m.app.FocusMain = true
			if msg.action == "system_prune" {
				m.app.DetailMode = "system"
				m.app.Status = "System prune complete"
			} else {
				m.app.Status = "Exec: " + msg.name
			}
			return m, nil
		}
		status := strings.Title(msg.action) + " " + msg.name + ": OK"
		return m, m.immediateRefreshCmdWithStatus(status)
	case bubbleExternalMsg:
		if msg.err != nil {
			if msg.action == "shell" {
				m.app.Status = "Cannot open shell: " + msg.err.Error()
			} else {
				m.app.Status = "Cannot attach to container: " + msg.err.Error()
			}
		}
		status := ""
		if msg.err != nil {
			status = m.app.Status
		}
		return m, m.immediateRefreshCmdWithStatus(status)
	case tea.KeyMsg:
		if (m.app.StatsStream != nil || m.app.EventStream != nil) && (msg.String() == "q" || msg.String() == "ctrl+c") {
			m.app.stopLiveStreams()
		}
		if m.app.Shell != nil {
			return m, m.updateShell(msg)
		}
		if m.app.Pull != nil {
			if m.app.PullOverlay {
				if !m.app.Pull.done {
					if m.app.Pull.cancel != nil {
						m.app.Pull.cancel()
					}
					if m.app.Pull.body != nil {
						_ = m.app.Pull.body.Close()
					}
					m.app.Status = "Pull cancelled"
				}
				m.app.PullOverlay = false
				m.app.Pull = nil
				return m, nil
			}
		}
		if m.app.Operation != nil {
			if m.app.Operation.done {
				m.app.Operation = nil
				return m, nil
			}
			m.app.Operation.cancelRequested = true
			if m.app.Operation.cancel != nil {
				m.app.Operation.cancel()
			}
			if m.app.Operation.body != nil {
				_ = m.app.Operation.body.Close()
			}
			m.app.Operation.lines = append(m.app.Operation.lines, "Cancellation requested.")
			m.app.Operation.done = true
			m.app.Status = m.app.Operation.title + " cancelled"
			return m, nil
		}
		if m.app.Prompt != nil {
			return m, m.updatePrompt(msg)
		}
		if m.app.ContainerForm != nil {
			return m, m.updateContainerForm(msg)
		}
		if m.app.FilterInput {
			return m, m.updateFilter(msg)
		}
		if m.app.PaletteInput {
			return m, m.updatePalette(msg)
		}
		if m.app.ConfirmAction != "" {
			return m, m.updateConfirmation(msg)
		}
		if m.app.MenuOpen {
			return m, m.updateMenu(msg)
		}
		return m, m.updateMain(msg)
	default:
		return m, nil
	}
}
func (m Model) updateMain(message tea.KeyMsg) tea.Cmd {
	key := message.String()
	switch key {
	case "q", "ctrl+c":
		return tea.Quit
	case "x", "?":
		m.app.openMenu()
	case "1", "2", "3", "4", "5", "6":
		m.app.toggleMode(resourceModes[int(key[0]-'1')])
		return m.detailCmd()
	case "esc":
		m.app.FocusMain = false
	case "up", "k":
		if m.app.FocusMain {
			m.app.scroll(-1)
		} else {
			m.app.move(-1)
			return m.detailCmd()
		}
	case "down", "j":
		if m.app.FocusMain {
			m.app.scroll(1)
		} else {
			m.app.move(1)
			return m.detailCmd()
		}
	case "left", "h":
		if !m.app.FocusMain {
			m.app.moveFocus(-1)
			return m.detailCmd()
		}
	case "right", "l":
		if !m.app.FocusMain {
			m.app.moveFocus(1)
			return m.detailCmd()
		}
	case "tab":
		if !m.app.FocusMain {
			m.app.moveFocus(1)
			return m.detailCmd()
		}
	case "shift+tab":
		if !m.app.FocusMain {
			m.app.moveFocus(-1)
			return m.detailCmd()
		}
	case "enter":
		if !m.app.FocusMain {
			m.app.FocusMain = true
			m.app.DetailScroll = 0
			return m.detailCmd()
		}
	case "space":
		if !m.app.FocusMain {
			m.app.toggleMarked()
		}
	case "pgup", "ctrl+u":
		if m.app.FocusMain {
			m.app.pageScroll(-1)
		}
	case "pgdown", "ctrl+d":
		if m.app.FocusMain {
			m.app.pageScroll(1)
		}
	case "home":
		if m.app.FocusMain {
			m.app.DetailScroll = 0
			if m.app.DetailMode == "logs" {
				m.app.LogsFollow = false
			}
		}
	case "end":
		if m.app.FocusMain {
			m.app.DetailScroll = max(0, len(m.app.DetailLines)-max(1, m.app.DetailViewRows))
			if m.app.DetailMode == "logs" {
				m.app.LogsFollow = true
			}
		}
	case "[":
		m.app.cycleTab(-1)
		return m.detailCmd()
	case "]":
		m.app.cycleTab(1)
		return m.detailCmd()
	case "i":
		m.app.DetailMode = "config"
		m.app.FocusMain = true
		return m.detailCmd()
	case "t":
		return m.startStatsCmd()
	case "m":
		if !m.app.FocusMain && m.app.Mode == "containers" {
			m.app.DetailMode = "logs"
			m.app.LogsFollow = true
			m.app.FocusMain = true
			return m.detailCmd()
		}
	case "f5":
		return m.immediateRefreshCmd()
	case "/":
		if m.app.FocusMain && (m.app.DetailMode == "logs" || m.app.DetailMode == "events") {
			m.app.FilterInput = true
			m.app.FilterDraft = m.app.LogFilter
			if m.app.DetailMode == "events" {
				m.app.FilterDraft = m.app.EventFilter
			}
		} else if !m.app.FocusMain {
			m.app.FilterInput = true
			m.app.FilterDraft = m.app.Filter
		}
	case "e":
		if !m.app.FocusMain && m.app.Mode == "containers" {
			m.app.toggleHideStopped()
			return m.immediateRefreshCmd()
		}
	case "a":
		if !m.app.FocusMain && m.app.Mode == "containers" {
			return m.external("attach")
		}
	case "E":
		if !m.app.FocusMain && m.app.Mode == "containers" {
			return m.startShellCmd()
		}
	case "S":
		if !m.app.FocusMain {
			return m.actionCmd("start")
		}
	case "s":
		if !m.app.FocusMain {
			return m.requestAction("stop")
		}
	case "r", "R":
		if !m.app.FocusMain {
			return m.actionCmd("restart")
		}
	case "p":
		if !m.app.FocusMain {
			action := "pause"
			if item := m.app.current(); item != nil && strings.EqualFold(item.State, "paused") {
				action = "unpause"
			}
			return m.actionCmd(action)
		}
	case "K":
		if !m.app.FocusMain {
			return m.requestAction("kill")
		}
	case "d":
		if !m.app.FocusMain {
			return m.requestAction("remove")
		}
	case "P":
		if !m.app.FocusMain {
			m.app.beginPrompt("pull")
		}
	case "B":
		if !m.app.FocusMain {
			m.app.beginPrompt("build")
		}
	case "U":
		if !m.app.FocusMain {
			m.app.beginPrompt("push")
		}
	case "C":
		if !m.app.FocusMain {
			m.app.beginPrompt("run")
		}
	}
	return nil
}

func (m Model) updateMenu(message tea.KeyMsg) tea.Cmd {
	switch message.String() {
	case "q", "esc":
		m.app.closeMenu()
	case "up", "k":
		m.app.moveMenu(-1)
	case "down", "j":
		m.app.moveMenu(1)
	case "enter", "space", "y", "Y":
		return m.executeMenuAction()
	case "/":
		m.app.PaletteInput = true
		m.app.FilterDraft = m.app.MenuFilter
	}
	return nil
}

func (m Model) updatePalette(message tea.KeyMsg) tea.Cmd {
	switch message.String() {
	case "esc", "ctrl+c":
		m.app.PaletteInput = false
		m.app.FilterDraft = ""
	case "enter":
		m.app.MenuFilter = strings.TrimSpace(m.app.FilterDraft)
		m.app.PaletteInput = false
		m.app.FilterDraft = ""
		m.app.MenuIndex = 0
		if entries := m.app.visibleMenuEntries(); len(entries) == 1 {
			return m.executeMenuAction()
		}
	case "backspace", "delete":
		if len(m.app.FilterDraft) > 0 {
			runes := []rune(m.app.FilterDraft)
			m.app.FilterDraft = string(runes[:len(runes)-1])
		}
	case "space", " ":
		m.app.FilterDraft += " "
	default:
		if message.Type == tea.KeyRunes {
			m.app.FilterDraft += string(message.Runes)
		}
	}
	return nil
}

func (m Model) executeMenuAction() tea.Cmd {
	action := m.app.selectedMenuAction()
	if action != "stats" && action != "events" {
		m.app.stopLiveStreams()
	}
	m.app.closeMenu()
	switch action {
	case "refresh":
		return m.immediateRefreshCmd()
	case "logs":
		m.app.DetailMode = "logs"
		m.app.LogsFollow = true
		m.app.FocusMain = true
		return m.detailCmd()
	case "stats":
		return m.startStatsCmd()
	case "help":
		m.app.DetailMode = "help"
		m.app.FocusMain = true
		return m.detailCmd()
	case "ports":
		m.app.DetailMode = "ports"
		m.app.FocusMain = true
		return m.detailCmd()
	case "env":
		m.app.DetailMode = "env"
		m.app.FocusMain = true
		return m.detailCmd()
	case "config":
		m.app.DetailMode = "config"
		m.app.FocusMain = true
		return m.detailCmd()
	case "relationships":
		m.app.DetailMode = "relationships"
		m.app.FocusMain = true
		return m.detailCmd()
	case "top":
		m.app.DetailMode = "top"
		m.app.FocusMain = true
		return m.detailCmd()
	case "system_info":
		m.app.DetailMode = "system"
		m.app.FocusMain = true
		return m.detailCmd()
	case "system_df":
		m.app.DetailMode = "storage"
		m.app.FocusMain = true
		return m.detailCmd()
	case "events":
		return m.startEventCmd()
	case "exec", "copy_to", "copy_from", "pull", "run", "build", "push", "image_tag", "image_search", "image_save", "image_load", "image_import", "registry_login", "registry_logout", "create_pod", "create_volume", "create_network", "create_secret", "network_connect", "network_disconnect", "rename", "wait", "export", "checkpoint", "restore", "settings", "commit", "kube_play", "kube_down", "kube_generate", "manifest_create", "manifest_add", "manifest_push", "prune_images", "prune_pods", "prune_volumes", "prune_networks", "system_prune", "system_check":
		m.app.beginPrompt(action)
		return nil
	case "batch_start", "batch_stop", "batch_remove":
		if action == "batch_remove" {
			return m.requestAction(action)
		}
		return m.batchActionCmd(action)
	case "manifest_inspect":
		return m.actionCmd(action)
	case "image_untag":
		return m.requestAction("image_untag")
	case "volume_mount", "volume_unmount":
		return m.volumeCommand(action)
	case "shell":
		m.app.closeMenu()
		return m.startShellCmd()
	case "attach":
		return m.external(action)
	case "hide_stopped":
		m.app.toggleHideStopped()
		return m.immediateRefreshCmd()
	case "stop", "remove", "kill":
		return m.requestAction(action)
	default:
		if action != "" {
			return m.actionCmd(action)
		}
	}
	return nil
}

func (m Model) requestAction(action string) tea.Cmd {
	if m.app.current() == nil {
		return nil
	}
	if !m.app.Config.ConfirmDestructive {
		if action == "batch_remove" {
			return m.batchActionCmd(action)
		}
		return m.actionCmd(action)
	}
	m.app.ConfirmAction = action
	return nil
}

func (m Model) actionCmd(action string) tea.Cmd {
	item := m.app.current()
	if item == nil {
		return nil
	}
	client := m.app.Client
	itemCopy := *item
	return func() tea.Msg {
		message := bubbleActionMsg{action: action, itemID: itemCopy.ID, name: itemCopy.Name}
		var err error
		switch {
		case itemCopy.Kind == "container":
			switch action {
			case "init":
				err = client.initContainer(itemCopy.ID)
			case "healthcheck":
				message.output, err = client.healthcheckContainer(itemCopy.ID)
			case "diff":
				var value any
				value, err = client.containerChanges(itemCopy.ID)
				if err == nil {
					message.output = valueText(value)
				}
			case "mount":
				message.output, err = client.mountContainer(itemCopy.ID)
			case "unmount":
				err = client.unmountContainer(itemCopy.ID)
			case "commit":
				err = fmt.Errorf("commit requires image repository input")
			case "kube_generate":
				err = fmt.Errorf("Kubernetes YAML generation requires input")
			case "systemd":
				message.output, err = client.generateSystemd(itemCopy.Kind, itemCopy.ID)
			case "quadlet":
				message.output, err = client.quadletFile(itemCopy.Kind, itemCopy.ID)
			default:
				err = client.resourceAction("containers", itemCopy.ID, action)
			}
		case itemCopy.Kind == "pod":
			if action == "systemd" || action == "quadlet" {
				if action == "systemd" {
					message.output, err = client.generateSystemd(itemCopy.Kind, itemCopy.ID)
				} else {
					message.output, err = client.quadletFile(itemCopy.Kind, itemCopy.ID)
				}
			} else {
				err = client.resourceAction("pods", itemCopy.ID, action)
			}
		case itemCopy.Kind == "image" && action == "image_untag":
			err = client.untagImage(itemCopy.ID)
		case itemCopy.Kind == "image" && action == "manifest_inspect":
			var value any
			value, err = client.inspectManifest(itemCopy.ID)
			if err == nil {
				message.output = valueText(value)
			}
		case action == "remove":
			err = client.remove(itemCopy.Kind+"s", itemCopy.ID)
		default:
			err = &PodmanError{Message: "Action \"" + action + "\" is not available for " + itemCopy.Kind + "."}
		}
		message.err = err
		return message
	}
}

func (m Model) batchActionCmd(action string) tea.Cmd {
	items := append([]Item(nil), m.app.markedItems()...)
	if len(items) == 0 {
		m.app.Status = "Select at least one container or pod with Space first."
		return nil
	}
	client := m.app.Client
	mode := m.app.Mode
	return func() tea.Msg {
		for _, item := range items {
			var err error
			if action == "batch_remove" {
				err = client.remove(item.Kind+"s", item.ID)
			} else {
				err = client.resourceAction(mode, item.ID, strings.TrimPrefix(action, "batch_"))
			}
			if err != nil {
				return bubbleActionMsg{action: action, name: item.Name, batch: true, err: err}
			}
		}
		return bubbleActionMsg{action: action, name: fmt.Sprintf("%d %s", len(items), mode), batch: true}
	}
}

func (m Model) updateConfirmation(message tea.KeyMsg) tea.Cmd {
	switch message.String() {
	case "y", "Y", "enter":
		action := m.app.ConfirmAction
		m.app.ConfirmAction = ""
		if action == "batch_remove" {
			return m.batchActionCmd(action)
		}
		if item := m.app.current(); item != nil {
			m.app.Status = strings.Title(action) + " " + item.Name + "..."
			return m.actionCmd(action)
		}
	case "n", "N", "q", "esc", "ctrl+c":
		m.app.ConfirmAction = ""
	}
	return nil
}

func (m Model) updateFilter(message tea.KeyMsg) tea.Cmd {
	switch message.String() {
	case "enter":
		if (m.app.DetailMode == "logs" || m.app.DetailMode == "events") && m.app.FocusMain {
			if m.app.DetailMode == "logs" {
				m.app.LogFilter = strings.TrimSpace(m.app.FilterDraft)
			} else {
				m.app.EventFilter = strings.TrimSpace(m.app.FilterDraft)
			}
			m.app.FilterInput = false
			m.app.DetailScroll = 0
			m.app.LogsFollow = true
			if m.app.DetailMode == "events" && m.app.EventStream != nil {
				return m.startEventCmd()
			}
			return m.detailCmd()
		}
		m.app.Filter = strings.TrimSpace(m.app.FilterDraft)
		m.app.FilterInput = false
		m.app.Selected[m.app.Mode] = 0
		return m.immediateRefreshCmd()
	case "esc", "ctrl+c":
		m.app.FilterInput = false
	case "backspace", "delete":
		if len(m.app.FilterDraft) > 0 {
			runes := []rune(m.app.FilterDraft)
			m.app.FilterDraft = string(runes[:len(runes)-1])
		}
	default:
		if message.Type == tea.KeyRunes && len(message.Runes) > 0 {
			m.app.FilterDraft += string(message.Runes)
		}
	}
	return nil
}

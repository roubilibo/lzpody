package main

import (
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
	case pullStartedMsg:
		if msg.err != nil {
			m.app.Pull = nil
			m.app.PullOverlay = false
			m.app.Status = msg.err.Error()
			return m, nil
		}
		if m.app.Pull == nil || !m.app.PullOverlay {
			_ = msg.session.body.Close()
			return m, nil
		}
		m.app.Pull = msg.session
		m.app.PullOverlay = true
		m.app.Status = "Pulling " + msg.session.reference + "..."
		return m, pullReadCmd(msg.session)
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
			if m.app.DetailMode != "system" && m.app.DetailMode != "events" {
				m.app.DetailLines = []string{"No item selected."}
				m.app.DetailMode = "summary"
			}
		}
		var detailCommand tea.Cmd
		if len(m.app.DetailLines) == 0 || m.app.DetailMode == "logs" || m.app.DetailMode == "stats" || m.app.DetailMode == "top" || m.app.DetailMode == "events" || m.app.DetailMode == "system" || m.app.DetailMode == "history" {
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
		if m.app.Shell != nil {
			return m, m.updateShell(msg)
		}
		if m.app.Pull != nil {
			if m.app.PullOverlay {
				m.app.PullOverlay = false
				m.app.Pull = nil
				return m, nil
			}
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
		m.app.DetailMode = "stats"
		m.app.FocusMain = true
		return m.detailCmd()
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
		if !m.app.FocusMain {
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
	}
	return nil
}

func (m Model) executeMenuAction() tea.Cmd {
	action := m.app.selectedMenuAction()
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
		m.app.DetailMode = "stats"
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
	case "events":
		m.app.DetailMode = "events"
		m.app.FocusMain = true
		return m.detailCmd()
	case "exec", "copy_to", "copy_from", "pull", "run", "build", "push", "image_tag", "image_search", "image_save", "image_load", "image_import", "registry_login", "registry_logout", "create_pod", "create_volume", "create_network", "create_secret", "network_connect", "network_disconnect", "prune_images", "prune_pods", "prune_volumes", "prune_networks", "system_prune":
		m.app.beginPrompt(action)
		return nil
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
		var err error
		switch {
		case itemCopy.Kind == "container":
			err = client.resourceAction("containers", itemCopy.ID, action)
		case itemCopy.Kind == "pod":
			err = client.resourceAction("pods", itemCopy.ID, action)
		case itemCopy.Kind == "image" && action == "image_untag":
			err = client.untagImage(itemCopy.ID)
		case action == "remove":
			err = client.remove(itemCopy.Kind+"s", itemCopy.ID)
		default:
			err = &PodmanError{Message: "Action \"" + action + "\" is not available for " + itemCopy.Kind + "."}
		}
		return bubbleActionMsg{action: action, itemID: itemCopy.ID, name: itemCopy.Name, err: err}
	}
}

func (m Model) updateConfirmation(message tea.KeyMsg) tea.Cmd {
	switch message.String() {
	case "y", "Y", "enter":
		action := m.app.ConfirmAction
		m.app.ConfirmAction = ""
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

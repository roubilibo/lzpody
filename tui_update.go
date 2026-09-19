package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
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
			m.app.DetailLines = []string{"No item selected."}
			m.app.DetailMode = "summary"
		}
		return m, tea.Batch(m.detailCmd(), m.refreshCmd())
	case bubbleDetailMsg:
		m.app.applyDetail(msg.result)
		return m, nil
	case bubbleActionMsg:
		if msg.err != nil {
			m.app.Status = msg.err.Error()
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
	case "1", "2", "3", "4", "5":
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
		}
	case "end":
		if m.app.FocusMain {
			m.app.DetailScroll = max(0, len(m.app.DetailLines)-max(1, m.app.DetailViewRows))
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
			return m.external("shell")
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
	case "top":
		m.app.DetailMode = "top"
		m.app.FocusMain = true
		return m.detailCmd()
	case "shell", "attach":
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

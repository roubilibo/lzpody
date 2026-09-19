package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type bubbleRefreshMsg struct{}

type bubbleActionMsg struct {
	action string
}

type bubbleExternalMsg struct {
	itemID string
	action string
	err    error
}

type bubbleModel struct {
	app          *App
	width        int
	height       int
	refreshAfter time.Duration
}

func newBubbleModel(app *App) bubbleModel {
	width, height := terminalSize()
	return bubbleModel{app: app, width: width, height: height, refreshAfter: refreshInterval}
}

func (m bubbleModel) Init() tea.Cmd {
	return m.refreshCmd()
}

func (m bubbleModel) refreshCmd() tea.Cmd {
	return tea.Tick(m.refreshAfter, func(time.Time) tea.Msg {
		return bubbleRefreshMsg{}
	})
}

func (m bubbleModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case bubbleRefreshMsg:
		m.refresh()
		return m, m.refreshCmd()
	case bubbleActionMsg:
		m.app.perform(msg.action)
		return m, nil
	case bubbleExternalMsg:
		m.app.refresh(msg.itemID)
		if msg.err != nil {
			if msg.action == "shell" {
				m.app.Status = "Cannot open shell: " + msg.err.Error()
			} else {
				m.app.Status = "Cannot attach to container: " + msg.err.Error()
			}
		}
		return m, nil
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

func (m bubbleModel) View() string {
	width, height := m.width, m.height
	if width <= 0 || height <= 0 {
		width, height = terminalSize()
	}
	return strings.ReplaceAll(m.app.frame(width, height), "\r\n", "\n")
}

func (m bubbleModel) refresh() {
	keepID := ""
	if item := m.app.current(); item != nil {
		keepID = item.ID
	}
	m.app.refresh(keepID)
}

func (m bubbleModel) updateMain(message tea.KeyMsg) tea.Cmd {
	key := message.String()
	switch key {
	case "q", "ctrl+c":
		return tea.Quit
	case "x", "?":
		m.app.openMenu()
	case "1", "2", "3", "4", "5":
		m.app.toggleMode(resourceModes[int(key[0]-'1')])
	case "esc":
		m.app.FocusMain = false
	case "up", "k":
		if m.app.FocusMain {
			m.app.scroll(-1)
		} else {
			m.app.move(-1)
		}
	case "down", "j":
		if m.app.FocusMain {
			m.app.scroll(1)
		} else {
			m.app.move(1)
		}
	case "left", "h":
		if !m.app.FocusMain {
			m.app.moveFocus(-1)
		}
	case "right", "l":
		if !m.app.FocusMain {
			m.app.moveFocus(1)
		}
	case "tab":
		if !m.app.FocusMain {
			m.app.moveFocus(1)
		}
	case "shift+tab":
		if !m.app.FocusMain {
			m.app.moveFocus(-1)
		}
	case "enter":
		if !m.app.FocusMain {
			m.app.FocusMain = true
			m.app.DetailScroll = 0
			m.app.loadDetail()
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
	case "]":
		m.app.cycleTab(1)
	case "i":
		m.app.loadInspect("config")
		m.app.FocusMain = true
	case "t":
		m.app.loadStats()
		m.app.FocusMain = true
	case "m":
		if !m.app.FocusMain && m.app.Mode == "containers" {
			m.app.loadLogs()
			m.app.FocusMain = true
		}
	case "f5":
		m.refresh()
	case "/":
		if !m.app.FocusMain {
			m.app.FilterInput = true
			m.app.FilterDraft = m.app.Filter
		}
	case "e":
		if !m.app.FocusMain && m.app.Mode == "containers" {
			m.app.toggleHideStopped()
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
			m.app.perform("start")
		}
	case "s":
		if !m.app.FocusMain {
			return m.requestAction("stop")
		}
	case "r", "R":
		if !m.app.FocusMain {
			m.app.perform("restart")
		}
	case "p":
		if !m.app.FocusMain {
			action := "pause"
			if item := m.app.current(); item != nil && strings.EqualFold(item.State, "paused") {
				action = "unpause"
			}
			m.app.perform(action)
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

func (m bubbleModel) updateMenu(message tea.KeyMsg) tea.Cmd {
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

func (m bubbleModel) executeMenuAction() tea.Cmd {
	action := m.app.selectedMenuAction()
	m.app.closeMenu()
	switch action {
	case "refresh":
		m.refresh()
	case "logs":
		m.app.loadLogs()
		m.app.FocusMain = true
	case "stats":
		m.app.loadStats()
		m.app.FocusMain = true
	case "env":
		m.app.loadEnv()
		m.app.FocusMain = true
	case "config":
		m.app.loadInspect("config")
		m.app.FocusMain = true
	case "top":
		m.app.loadTop()
		m.app.FocusMain = true
	case "shell", "attach":
		return m.external(action)
	case "hide_stopped":
		m.app.toggleHideStopped()
	case "stop", "remove", "kill":
		return m.requestAction(action)
	default:
		if action != "" {
			m.app.perform(action)
		}
	}
	return nil
}

func (m bubbleModel) requestAction(action string) tea.Cmd {
	if m.app.current() == nil {
		return nil
	}
	m.app.ConfirmAction = action
	return nil
}

func (m bubbleModel) updateConfirmation(message tea.KeyMsg) tea.Cmd {
	switch message.String() {
	case "y", "Y", "enter":
		action := m.app.ConfirmAction
		m.app.ConfirmAction = ""
		if item := m.app.current(); item != nil {
			m.app.Status = strings.Title(action) + " " + item.Name + "..."
			return tea.Tick(10*time.Millisecond, func(time.Time) tea.Msg {
				return bubbleActionMsg{action: action}
			})
		}
	case "n", "N", "q", "esc", "ctrl+c":
		m.app.ConfirmAction = ""
	}
	return nil
}

func (m bubbleModel) updateFilter(message tea.KeyMsg) tea.Cmd {
	switch message.String() {
	case "enter":
		m.app.Filter = strings.TrimSpace(m.app.FilterDraft)
		m.app.FilterInput = false
		m.app.Selected[m.app.Mode] = 0
		m.refresh()
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

func (m bubbleModel) external(action string) tea.Cmd {
	item := m.app.current()
	if item == nil {
		return nil
	}
	var command *exec.Cmd
	if action == "shell" {
		command = exec.Command("podman", "exec", "-it", item.Name, "sh")
	} else {
		command = exec.Command("podman", "attach", item.Name)
	}
	itemID := item.ID
	return tea.ExecProcess(command, func(err error) tea.Msg {
		if _, exited := err.(*exec.ExitError); exited {
			err = nil
		}
		return bubbleExternalMsg{itemID: itemID, action: action, err: err}
	})
}

func runBubbleTUI(client *PodmanClient) error {
	if !isTTY() {
		return fmt.Errorf("lzpody requires an interactive terminal")
	}
	app := NewApp(client)
	app.refresh("")
	model := newBubbleModel(app)
	_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

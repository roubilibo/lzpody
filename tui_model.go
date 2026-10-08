package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type bubbleRefreshMsg struct {
	result         resourceSnapshot
	statusOverride string
	requestID      uint64
	periodic       bool
}

type bubbleRefreshTickMsg struct{}

type bubbleDetailMsg struct {
	result detailResult
}

type bubbleActionMsg struct {
	action string
	itemID string
	name   string
	output string
	config *UserConfig
	batch  bool
	err    error
}

type bubbleExternalMsg struct {
	itemID string
	action string
	err    error
}

type cursorBlinkMsg struct{}

type Model struct {
	app          *App
	width        int
	height       int
	refreshAfter time.Duration
}

// Model is the Elm Architecture state for the Bubble Tea program. Update is
// the message reducer, View is the renderer, and commands are the only way
// the event loop schedules asynchronous work.
var _ tea.Model = Model{}

func newBubbleModel(app *App) Model {
	refreshAfter := app.RefreshAfter
	if refreshAfter <= 0 {
		refreshAfter = refreshInterval
	}
	return Model{app: app, refreshAfter: refreshAfter}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.immediateRefreshCmd(), m.refreshCmd(), m.capabilitiesCmd(), cursorBlinkCmd())
}

func cursorBlinkCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return cursorBlinkMsg{}
	})
}

func (m Model) refreshCmd() tea.Cmd {
	return tea.Tick(m.refreshAfter, func(time.Time) tea.Msg {
		return bubbleRefreshTickMsg{}
	})
}

func (m Model) immediateRefreshCmd() tea.Cmd {
	return m.immediateRefreshCmdWithStatus("")
}

func (m Model) immediateRefreshCmdWithStatus(status string) tea.Cmd {
	return m.refreshSnapshotCmd(status, false)
}

func (m Model) refreshSnapshotCmd(status string, periodic bool) tea.Cmd {
	client := m.app.Client
	filter := m.app.Filter
	hideStopped := m.app.HideStopped
	m.app.RefreshRequestID++
	requestID := m.app.RefreshRequestID
	return func() tea.Msg {
		return bubbleRefreshMsg{result: fetchResourceSnapshot(client, filter, hideStopped), statusOverride: status, requestID: requestID, periodic: periodic}
	}
}

func (m Model) detailCmd() tea.Cmd {
	item := m.app.current()
	if item == nil && m.app.DetailMode != "system" && m.app.DetailMode != "storage" && m.app.DetailMode != "help" && m.app.DetailMode != "events" {
		return nil
	}
	client := m.app.Client
	itemCopy := Item{}
	if item != nil {
		itemCopy = *item
	}
	mode := m.app.DetailMode
	history := append([]map[string]any(nil), m.app.StatsHistory[item.ID]...)
	logFilter := m.app.LogFilter
	m.app.DetailRequestID++
	requestID := m.app.DetailRequestID
	return func() tea.Msg {
		result := fetchDetail(client, itemCopy, mode, history, logFilter)
		result.requestID = requestID
		return bubbleDetailMsg{result: result}
	}
}

// bubbleModel is kept as an internal compatibility alias for existing tests.
type bubbleModel = Model

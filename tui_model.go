package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type bubbleRefreshMsg struct {
	result         resourceSnapshot
	statusOverride string
}

type bubbleDetailMsg struct {
	result detailResult
}

type bubbleActionMsg struct {
	action string
	itemID string
	name   string
	err    error
}

type bubbleExternalMsg struct {
	itemID string
	action string
	err    error
}

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
	return Model{app: app, refreshAfter: refreshInterval}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.immediateRefreshCmd(), m.refreshCmd())
}

func (m Model) refreshCmd() tea.Cmd {
	client := m.app.Client
	filter := m.app.Filter
	hideStopped := m.app.HideStopped
	return tea.Tick(m.refreshAfter, func(time.Time) tea.Msg {
		return bubbleRefreshMsg{result: fetchResourceSnapshot(client, filter, hideStopped)}
	})
}

func (m Model) immediateRefreshCmd() tea.Cmd {
	return m.immediateRefreshCmdWithStatus("")
}

func (m Model) immediateRefreshCmdWithStatus(status string) tea.Cmd {
	client := m.app.Client
	filter := m.app.Filter
	hideStopped := m.app.HideStopped
	return func() tea.Msg {
		return bubbleRefreshMsg{result: fetchResourceSnapshot(client, filter, hideStopped), statusOverride: status}
	}
}

func (m Model) detailCmd() tea.Cmd {
	item := m.app.current()
	if item == nil {
		return nil
	}
	client := m.app.Client
	itemCopy := *item
	mode := m.app.DetailMode
	history := append([]map[string]any(nil), m.app.StatsHistory[item.ID]...)
	return func() tea.Msg {
		return bubbleDetailMsg{result: fetchDetail(client, itemCopy, mode, history)}
	}
}

// bubbleModel is kept as an internal compatibility alias for existing tests.
type bubbleModel = Model

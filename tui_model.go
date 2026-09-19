package main

import (
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
	width, height := terminalSize()
	return Model{app: app, width: width, height: height, refreshAfter: refreshInterval}
}

func (m Model) Init() tea.Cmd {
	return m.refreshCmd()
}

func (m Model) refreshCmd() tea.Cmd {
	return tea.Tick(m.refreshAfter, func(time.Time) tea.Msg {
		return bubbleRefreshMsg{}
	})
}

// bubbleModel is kept as an internal compatibility alias for existing tests.
type bubbleModel = Model

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	tea "github.com/charmbracelet/bubbletea"
)

type statsStreamSession struct {
	itemID string
	kind   string
	ctx    context.Context
	cancel context.CancelFunc
	body   io.ReadCloser
	scan   *bufio.Scanner
}

type statsStreamStartedMsg struct {
	session *statsStreamSession
	body    io.ReadCloser
	scan    *bufio.Scanner
	err     error
}

type statsStreamSampleMsg struct {
	session *statsStreamSession
	sample  map[string]any
	err     error
	done    bool
}

func (a *App) stopStatsStream() {
	if a.StatsStream == nil {
		return
	}
	if a.StatsStream.cancel != nil {
		a.StatsStream.cancel()
	}
	if a.StatsStream.body != nil {
		_ = a.StatsStream.body.Close()
	}
	a.StatsStream = nil
}

func (m Model) startStatsCmd() tea.Cmd {
	item := m.app.current()
	if item == nil || (item.Kind != "container" && item.Kind != "pod") {
		return nil
	}
	m.app.stopLiveStreams()
	ctx, cancel := context.WithCancel(context.Background())
	session := &statsStreamSession{itemID: item.ID, kind: item.Kind, ctx: ctx, cancel: cancel}
	m.app.StatsStream = session
	m.app.DetailMode = "stats"
	m.app.FocusMain = true
	m.app.Status = "Connecting to live stats..."
	client := m.app.Client
	return func() tea.Msg {
		body, err := client.statsStream(ctx, item.Kind, item.ID)
		if err != nil {
			return statsStreamStartedMsg{session: session, err: err}
		}
		scan := bufio.NewScanner(body)
		scan.Buffer(make([]byte, 4096), 1024*1024)
		return statsStreamStartedMsg{session: session, body: body, scan: scan}
	}
}

func (c *PodmanClient) statsStream(ctx context.Context, kind, id string) (io.ReadCloser, error) {
	var path string
	if kind == "pod" {
		path = c.APIRoot + "/pods/stats?" + url.Values{"namesOrIDs": {id}, "all": {"true"}, "stream": {"true"}}.Encode()
	} else {
		path = c.APIRoot + "/containers/stats?" + url.Values{"containers": {id}, "stream": {"true"}}.Encode()
	}
	return c.openStream(ctx, http.MethodGet, path, nil, "application/json")
}

func statsStreamReadCmd(session *statsStreamSession) tea.Cmd {
	return func() tea.Msg {
		if session == nil || session.scan == nil {
			return statsStreamSampleMsg{session: session, done: true}
		}
		if !session.scan.Scan() {
			return statsStreamSampleMsg{session: session, done: true, err: session.scan.Err()}
		}
		var value any
		if err := json.Unmarshal([]byte(session.scan.Text()), &value); err != nil {
			return statsStreamSampleMsg{session: session, err: err}
		}
		return statsStreamSampleMsg{session: session, sample: statsPayload(value)}
	}
}

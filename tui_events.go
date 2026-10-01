package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type eventStreamSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	body   io.ReadCloser
	scan   *bufio.Scanner
}

type eventStreamStartedMsg struct {
	session *eventStreamSession
	body    io.ReadCloser
	scan    *bufio.Scanner
	err     error
}

type eventStreamLineMsg struct {
	session *eventStreamSession
	line    string
	err     error
	done    bool
}

func (c *PodmanClient) eventsStream(ctx context.Context, since time.Time) (io.ReadCloser, error) {
	query := url.Values{"stream": {"true"}, "since": {since.UTC().Format(time.RFC3339Nano)}}
	return c.openStream(ctx, http.MethodGet, c.APIRoot+"/events?"+query.Encode(), nil, "application/json")
}

func (a *App) stopEventStream() {
	if a.EventStream == nil {
		return
	}
	if a.EventStream.cancel != nil {
		a.EventStream.cancel()
	}
	if a.EventStream.body != nil {
		_ = a.EventStream.body.Close()
	}
	a.EventStream = nil
}

func (a *App) stopLiveStreams() {
	a.stopStatsStream()
	a.stopEventStream()
}

func (m Model) startEventCmd() tea.Cmd {
	m.app.stopLiveStreams()
	ctx, cancel := context.WithCancel(context.Background())
	session := &eventStreamSession{ctx: ctx, cancel: cancel}
	m.app.EventStream = session
	m.app.DetailMode = "events"
	m.app.FocusMain = true
	m.app.DetailLines = nil
	m.app.DetailRawLines = nil
	m.app.Status = "Connecting to live events..."
	client := m.app.Client
	return func() tea.Msg {
		body, err := client.eventsStream(ctx, time.Now().Add(-10*time.Second))
		if err != nil {
			return eventStreamStartedMsg{session: session, err: err}
		}
		scan := bufio.NewScanner(body)
		scan.Buffer(make([]byte, 4096), 1024*1024)
		return eventStreamStartedMsg{session: session, body: body, scan: scan}
	}
}

func eventStreamReadCmd(session *eventStreamSession) tea.Cmd {
	return func() tea.Msg {
		if session == nil || session.scan == nil {
			return eventStreamLineMsg{session: session, done: true}
		}
		if !session.scan.Scan() {
			return eventStreamLineMsg{session: session, done: true, err: session.scan.Err()}
		}
		return eventStreamLineMsg{session: session, line: strings.TrimSpace(session.scan.Text())}
	}
}

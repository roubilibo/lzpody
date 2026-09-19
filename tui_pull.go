package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type pullSession struct {
	body      io.ReadCloser
	scanner   *bufio.Scanner
	reference string
	lines     []string
	done      bool
	err       error
}

type pullStartedMsg struct {
	session *pullSession
	err     error
}

type pullProgressMsg struct {
	session *pullSession
	line    string
	done    bool
	err     error
}

func (m Model) startPullCmd(reference string) tea.Cmd {
	client := m.app.Client
	return func() tea.Msg {
		body, err := client.pullImageStream(reference)
		if err != nil {
			return pullStartedMsg{err: err}
		}
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		return pullStartedMsg{session: &pullSession{body: body, scanner: scanner, reference: reference, lines: []string{"Pulling " + reference + "..."}}}
	}
}

func pullReadCmd(session *pullSession) tea.Cmd {
	return func() tea.Msg {
		for session.scanner.Scan() {
			line := pullProgressText(session.scanner.Text())
			if line != "" {
				return pullProgressMsg{session: session, line: line}
			}
		}
		err := session.scanner.Err()
		_ = session.body.Close()
		return pullProgressMsg{session: session, done: true, err: err}
	}
}

func pullProgressText(raw string) string {
	var event map[string]any
	if json.Unmarshal([]byte(raw), &event) != nil {
		return strings.TrimSpace(raw)
	}
	if message := scalarText(event["error"]); message != "" {
		return "Error: " + message
	}
	status := scalarText(event["status"])
	id := scalarText(event["id"])
	progress := scalarText(event["progress"])
	if progress == "" {
		if detail, ok := event["progressDetail"].(map[string]any); ok {
			current, total := numberValue(detail["current"]), numberValue(detail["total"])
			if total > 0 {
				progress = fmt.Sprintf("%s/%s", bytesText(current), bytesText(total))
			} else if current > 0 {
				progress = bytesText(current)
			}
		}
	}
	parts := []string{}
	if id != "" {
		parts = append(parts, id)
	}
	if status != "" {
		parts = append(parts, status)
	}
	if progress != "" {
		parts = append(parts, progress)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func appendPullLine(lines []string, line string) []string {
	if line == "" {
		return lines
	}
	lines = append(lines, line)
	if len(lines) > 1000 {
		lines = lines[len(lines)-1000:]
	}
	return lines
}

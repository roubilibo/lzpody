package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type operationSession struct {
	action          string
	title           string
	lines           []string
	body            io.ReadCloser
	scanner         *bufio.Scanner
	cancel          context.CancelFunc
	done            bool
	cancelRequested bool
	err             error
}

type operationStreamStartedMsg struct {
	session *operationSession
	body    io.ReadCloser
	scanner *bufio.Scanner
	err     error
}

type operationProgressMsg struct {
	session *operationSession
	line    string
	done    bool
	err     error
}

type bubbleCapabilitiesMsg struct {
	capabilities PodmanCapabilities
	err          error
}

func (m Model) beginOperation(action, title string) (*operationSession, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	session := &operationSession{
		action: action,
		title:  title,
		lines:  []string{"Starting " + strings.ToLower(title) + "..."},
		cancel: cancel,
	}
	m.app.Operation = session
	m.app.Status = title + "..."
	return session, ctx
}

func (m Model) startOperationCmd(action string, values []string, session *operationSession, ctx context.Context) tea.Cmd {
	client := m.app.Client
	return func() tea.Msg {
		var body io.ReadCloser
		var err error
		switch action {
		case "build":
			parts := splitPrompt(promptValue(values, 0), 2)
			tag := ""
			if len(parts) > 1 {
				tag = parts[1]
			}
			body, err = client.buildImageStream(ctx, parts[0], tag)
		case "push":
			parts := splitPrompt(promptValue(values, 0), 2)
			body, err = client.pushImageStream(ctx, parts[0], parts[1])
		case "system_prune":
			parts := splitPrompt(promptValue(values, 0), 5)
			all := len(parts) > 1 && strings.EqualFold(parts[1], "all")
			volumes := len(parts) > 2 && strings.EqualFold(parts[2], "volumes")
			build := len(parts) > 3 && strings.EqualFold(parts[3], "build")
			filters := []string{}
			if len(parts) > 4 && parts[4] != "" {
				filters = strings.Split(parts[4], ",")
			}
			output, pruneErr := client.systemPruneContext(ctx, all, volumes, build, filters)
			return operationProgressMsg{session: session, line: output, done: true, err: pruneErr}
		case "system_check":
			parts := splitPrompt(promptValue(values, 0), 4)
			quick := len(parts) > 0 && (strings.EqualFold(parts[0], "yes") || strings.EqualFold(parts[0], "true"))
			repair := len(parts) > 1 && (strings.EqualFold(parts[1], "yes") || strings.EqualFold(parts[1], "true"))
			repairLossy := len(parts) > 2 && (strings.EqualFold(parts[2], "yes") || strings.EqualFold(parts[2], "true"))
			maxAge := ""
			if len(parts) > 3 {
				maxAge = parts[3]
			}
			output, checkErr := client.systemCheckContext(ctx, quick, repair, repairLossy, maxAge)
			return operationProgressMsg{session: session, line: output, done: true, err: checkErr}
		default:
			return operationProgressMsg{session: session, done: true, err: fmt.Errorf("unsupported progress operation: %s", action)}
		}
		if err != nil {
			return operationStreamStartedMsg{session: session, err: err}
		}
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		return operationStreamStartedMsg{session: session, body: body, scanner: scanner}
	}
}

func operationReadCmd(session *operationSession) tea.Cmd {
	return func() tea.Msg {
		if session.scanner == nil || session.body == nil {
			return operationProgressMsg{session: session, done: true}
		}
		for session.scanner.Scan() {
			line, terminal, err := operationProgressEvent(session.scanner.Text())
			if line != "" {
				if terminal {
					_ = session.body.Close()
				}
				return operationProgressMsg{session: session, line: line, done: terminal, err: err}
			}
			if terminal {
				_ = session.body.Close()
				return operationProgressMsg{session: session, done: true, err: err}
			}
		}
		err := session.scanner.Err()
		_ = session.body.Close()
		return operationProgressMsg{session: session, done: true, err: err}
	}
}

func operationProgressText(raw string) string {
	line, _, _ := operationProgressEvent(raw)
	return line
}

func operationProgressEvent(raw string) (string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, nil
	}
	var event map[string]any
	if json.Unmarshal([]byte(trimmed), &event) != nil {
		return trimmed, false, nil
	}
	if message := scalarText(event["error"]); message != "" {
		return "Error: " + message, true, &PodmanError{Message: "Operation failed: " + message}
	}
	if stream := strings.TrimSpace(scalarText(event["stream"])); stream != "" {
		return stream, false, nil
	}
	parts := []string{}
	if id := scalarText(event["id"]); id != "" {
		parts = append(parts, id)
	}
	if status := scalarText(event["status"]); status != "" {
		parts = append(parts, status)
	}
	if progress := scalarText(event["progress"]); progress != "" {
		parts = append(parts, progress)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " "), false, nil
	}
	if aux := event["aux"]; aux != nil {
		return valueText(aux), false, nil
	}
	return "", false, nil
}

func appendOperationLines(lines []string, text string) []string {
	for _, line := range splitLines(text, "") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > 1000 {
		lines = lines[len(lines)-1000:]
	}
	return lines
}

func (m Model) finishOperation(session *operationSession, err error) {
	if m.app.Operation != session {
		return
	}
	session.err = err
	session.done = true
	if session.cancelRequested || errors.Is(err, context.Canceled) {
		m.app.Status = session.title + " cancelled"
		return
	}
	if err != nil {
		m.app.Status = session.title + " failed: " + err.Error()
		return
	}
	m.app.Status = session.title + " complete"
}

func (m Model) capabilitiesCmd() tea.Cmd {
	client := m.app.Client
	return func() tea.Msg {
		capabilities, err := client.detectCapabilities()
		return bubbleCapabilitiesMsg{capabilities: capabilities, err: err}
	}
}

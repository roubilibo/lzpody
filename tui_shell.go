package main

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

type shellSession struct {
	command  *exec.Cmd
	terminal *os.File
	emulator vt10x.Terminal
	itemID   string
	name     string
}

type shellStartedMsg struct {
	session *shellSession
	err     error
}

type shellOutputMsg struct {
	session *shellSession
	output  string
}

type shellExitedMsg struct {
	session *shellSession
	err     error
}

func (m Model) startShellCmd() tea.Cmd {
	item := m.app.current()
	if item == nil {
		return nil
	}
	itemCopy := *item
	return func() tea.Msg {
		command := exec.Command("podman", "exec", "-it", itemCopy.Name, "sh")
		terminal, err := pty.Start(command)
		if err != nil {
			return shellStartedMsg{err: err}
		}
		cols, rows := shellDimensions(m.width, m.height)
		emulator := vt10x.New(vt10x.WithSize(cols, rows))
		return shellStartedMsg{session: &shellSession{command: command, terminal: terminal, emulator: emulator, itemID: itemCopy.ID, name: itemCopy.Name}}
	}
}

func shellReadCmd(session *shellSession) tea.Cmd {
	return func() tea.Msg {
		buffer := make([]byte, 4096)
		for {
			count, err := session.terminal.Read(buffer)
			if count > 0 {
				return shellOutputMsg{session: session, output: string(buffer[:count])}
			}
			if err != nil {
				waitErr := session.command.Wait()
				if waitErr != nil && !strings.Contains(waitErr.Error(), "signal: killed") {
					err = waitErr
				}
				return shellExitedMsg{session: session, err: err}
			}
		}
	}
}

func (m Model) resizeShell() {
	if m.app.Shell == nil {
		return
	}
	cols, rows := shellDimensions(m.width, m.height)
	_ = pty.Setsize(m.app.Shell.terminal, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	m.app.Shell.emulator.Resize(cols, rows)
}

func shellDimensions(width, height int) (int, int) {
	layout := layoutFor(width, height)
	return max(1, layout.rightWidth-4), max(1, layout.bottom-layout.top-2)
}

func (m Model) updateShell(message tea.KeyMsg) tea.Cmd {
	session := m.app.Shell
	if session == nil {
		return nil
	}
	if message.String() == "esc" {
		_ = session.terminal.Close()
		if session.command.Process != nil {
			_ = session.command.Process.Kill()
		}
		m.app.Shell = nil
		m.app.DetailMode = "summary"
		m.app.DetailLines = nil
		m.app.DetailRawLines = nil
		m.app.FocusMain = false
		m.app.Status = "Shell closed."
		return nil
	}
	input := shellKeyBytes(message)
	if len(input) == 0 {
		return nil
	}
	if _, err := session.terminal.Write(input); err != nil {
		m.app.Status = "Shell input failed: " + err.Error()
	}
	return nil
}

func shellKeyBytes(message tea.KeyMsg) []byte {
	if message.Type == tea.KeyRunes {
		return []byte(string(message.Runes))
	}
	switch message.String() {
	case " ", "space":
		return []byte(" ")
	case "enter":
		return []byte("\r")
	case "tab":
		return []byte("\t")
	case "backspace":
		return []byte("\x7f")
	case "delete":
		return []byte("\x1b[3~")
	case "up":
		return []byte("\x1b[A")
	case "down":
		return []byte("\x1b[B")
	case "right":
		return []byte("\x1b[C")
	case "left":
		return []byte("\x1b[D")
	case "home":
		return []byte("\x1b[H")
	case "end":
		return []byte("\x1b[F")
	case "ctrl+c":
		return []byte("\x03")
	case "ctrl+d":
		return []byte("\x04")
	case "ctrl+l":
		return []byte("\x0c")
	default:
		return nil
	}
}

func shellSnapshot(session *shellSession) []string {
	view := strings.TrimSuffix(session.emulator.String(), "\n")
	lines := strings.Split(view, "\n")
	cursor := session.emulator.Cursor()
	if session.emulator.CursorVisible() && cursor.Y >= 0 && cursor.Y < len(lines) {
		line := []rune(lines[cursor.Y])
		if cursor.X > len(line) {
			line = append(line, []rune(strings.Repeat(" ", cursor.X-len(line)))...)
		}
		line = append(line, 0)
		copy(line[cursor.X+1:], line[cursor.X:])
		line[cursor.X] = '▌'
		lines[cursor.Y] = string(line)
	}
	return lines
}

package main

import (
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) external(action string) tea.Cmd {
	item := m.app.current()
	if item == nil {
		return nil
	}
	var command *exec.Cmd
	command = exec.Command("podman", "attach", item.Name)
	itemID := item.ID
	return tea.ExecProcess(command, func(err error) tea.Msg {
		if _, exited := err.(*exec.ExitError); exited {
			err = nil
		}
		return bubbleExternalMsg{itemID: itemID, action: action, err: err}
	})
}
func runBubbleTUI(client *PodmanClient) error {
	app := NewApp(client)
	model := newBubbleModel(app)
	_, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

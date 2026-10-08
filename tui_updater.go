package main

import (
	"context"
	"fmt"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
)

type updateCheckMsg struct {
	release *updateRelease
	target  string
	err     error
}

type updateInstallMsg struct {
	tag string
	err error
}

func (m Model) checkForUpdatesCmd() tea.Cmd {
	if m.app.UpdateChecking || m.app.UpdateInProgress {
		return nil
	}
	m.app.UpdateChecking = true
	m.app.UpdateRelease = nil
	m.app.UpdateTarget = ""
	m.app.UpdateConfirm = false
	m.app.Status = "Checking for lzpody updates..."
	currentVersion := version
	return func() tea.Msg {
		target, err := updateTargetPath(currentVersion)
		if err != nil {
			return updateCheckMsg{err: err}
		}
		release, err := checkLatestRelease(context.Background(), currentVersion, runtime.GOOS, runtime.GOARCH, "", updateHTTPClient())
		return updateCheckMsg{release: release, target: target, err: err}
	}
}

func (m Model) beginSelfUpdate() tea.Cmd {
	if m.app.UpdateRelease == nil || m.app.UpdateTarget == "" || m.app.UpdateInProgress {
		return nil
	}
	release := *m.app.UpdateRelease
	target := m.app.UpdateTarget
	m.app.UpdateConfirm = false
	m.app.UpdateInProgress = true
	m.app.Status = "Downloading lzpody " + release.TagName + "..."
	return func() tea.Msg {
		err := installRelease(context.Background(), release, target, updateHTTPClient())
		return updateInstallMsg{tag: release.TagName, err: err}
	}
}

func (m Model) updateSelfConfirmation(message tea.KeyMsg) tea.Cmd {
	switch message.String() {
	case "y", "Y", "enter":
		return m.beginSelfUpdate()
	case "n", "N", "q", "esc", "ctrl+c":
		m.app.UpdateConfirm = false
		m.app.UpdateRelease = nil
		m.app.UpdateTarget = ""
		m.app.Status = "Update cancelled."
	}
	return nil
}

func applyUpdateCheck(app *App, message updateCheckMsg) {
	app.UpdateChecking = false
	if message.err != nil {
		app.UpdateRelease = nil
		app.UpdateTarget = ""
		app.Status = "Update check failed: " + message.err.Error()
		return
	}
	if message.release == nil {
		app.UpdateRelease = nil
		app.UpdateTarget = ""
		app.Status = fmt.Sprintf("lzpody %s is up to date.", version)
		return
	}
	app.UpdateRelease = message.release
	app.UpdateTarget = message.target
	app.UpdateConfirm = true
	app.Status = "Update available: " + message.release.TagName
}

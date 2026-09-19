package main

import (
	"strings"
)

func (m Model) View() string {
	width, height := m.width, m.height
	if width <= 0 || height <= 0 {
		width, height = terminalSize()
	}
	return strings.ReplaceAll(m.app.frame(width, height), "\r\n", "\n")
}

package main

import tea "github.com/charmbracelet/bubbletea"

type uiLayout struct {
	top, bottom        int
	leftWidth          int
	rightX, rightWidth int
	panelTops          [6]int
	panelHeights       [6]int
}

func layoutFor(width, height int) uiLayout {
	layout := uiLayout{top: 2, bottom: height - 3}
	layout.leftWidth = max(32, width/3)
	layout.rightX = layout.leftWidth + 2
	layout.rightWidth = width - layout.rightX - 1
	leftHeight := layout.bottom - layout.top + 1
	panelHeight, extraPanels := leftHeight/len(resourceModes), leftHeight%len(resourceModes)
	for index := range resourceModes {
		panelTop := layout.top + index*panelHeight
		panelH := panelHeight
		if index < extraPanels {
			panelH++
			panelTop += index
		} else {
			panelTop += extraPanels
		}
		layout.panelTops[index] = panelTop
		layout.panelHeights[index] = panelH
	}
	return layout
}

func menuBounds(app *App, width, height int) (left, top, boxWidth, boxHeight, visibleStart int) {
	entries := app.menuEntries()
	boxWidth = min(42, max(24, width-6))
	boxHeight = min(len(entries)+2, max(5, height-4))
	top = max(1, (height-boxHeight)/2)
	left = max(2, (width-boxWidth)/2)
	visible := max(1, boxHeight-2)
	visibleStart = max(0, min(app.MenuIndex-visible+1, len(entries)-visible))
	return
}

func (m Model) mouseUpdate(event tea.MouseMsg) tea.Cmd {
	if m.app.Prompt != nil || m.app.ContainerForm != nil || m.app.ConfirmAction != "" {
		return nil
	}
	if m.app.MenuOpen {
		return m.mouseMenu(event)
	}
	if event.Button == tea.MouseButtonWheelUp || event.Button == tea.MouseButtonWheelDown {
		return m.mouseWheel(event)
	}
	if event.Action == tea.MouseActionPress && event.Button == tea.MouseButtonRight {
		layout := layoutFor(m.width, m.height)
		if event.X >= 1 && event.X <= layout.leftWidth && event.Y >= layout.top && event.Y <= layout.bottom {
			return m.mouseContextPanel(layout, event.X, event.Y)
		}
		if event.X >= layout.rightX && event.X < layout.rightX+layout.rightWidth && event.Y >= layout.top+2 && event.Y < layout.bottom {
			m.app.FocusMain = false
			m.app.openMenu()
		}
		return nil
	}
	if event.Action != tea.MouseActionPress || event.Button != tea.MouseButtonLeft {
		return nil
	}
	layout := layoutFor(m.width, m.height)
	if event.X >= 1 && event.X <= layout.leftWidth && event.Y >= layout.top && event.Y <= layout.bottom {
		return m.mousePanel(layout, event.X, event.Y)
	}
	if event.X >= layout.rightX && event.X < layout.rightX+layout.rightWidth {
		if event.Y == layout.top+1 {
			return m.mouseTab(layout, event.X)
		}
		if event.Y >= layout.top+2 && event.Y < layout.bottom {
			m.app.FocusMain = true
		}
	}
	return nil
}

func (m Model) mouseMenu(event tea.MouseMsg) tea.Cmd {
	if event.Button == tea.MouseButtonWheelUp || event.Button == tea.MouseButtonWheelDown {
		delta := 1
		if event.Button == tea.MouseButtonWheelUp {
			delta = -1
		}
		m.app.moveMenu(delta)
		return nil
	}
	if event.Action != tea.MouseActionPress || event.Button != tea.MouseButtonLeft {
		return nil
	}
	left, top, boxWidth, boxHeight, visibleStart := menuBounds(m.app, m.width, m.height)
	if event.X < left || event.X >= left+boxWidth || event.Y < top || event.Y >= top+boxHeight {
		m.app.closeMenu()
		return nil
	}
	row := event.Y - top - 1
	visible := max(1, boxHeight-2)
	entries := m.app.menuEntries()
	if row >= 0 && row < visible && visibleStart+row < len(entries) {
		m.app.MenuIndex = visibleStart + row
		return m.executeMenuAction()
	}
	return nil
}

func (m Model) mouseWheel(event tea.MouseMsg) tea.Cmd {
	layout := layoutFor(m.width, m.height)
	delta := 3
	if event.Button == tea.MouseButtonWheelUp {
		delta = -3
	}
	if event.X >= layout.rightX && event.X < layout.rightX+layout.rightWidth && event.Y >= layout.top+2 && event.Y < layout.bottom {
		m.app.FocusMain = true
		m.app.scroll(delta)
		return nil
	}
	if event.X >= 1 && event.X <= layout.leftWidth && event.Y >= layout.top && event.Y <= layout.bottom {
		for index, panelTop := range layout.panelTops {
			if event.Y >= panelTop && event.Y < panelTop+layout.panelHeights[index] {
				if resourceModes[index] != m.app.Mode {
					m.app.Mode = resourceModes[index]
					m.app.FocusMain = false
					m.app.DetailMode = "summary"
				}
				m.app.move(delta / 3)
				return m.detailCmd()
			}
		}
	}
	return nil
}

func (m Model) mousePanel(layout uiLayout, x, y int) tea.Cmd {
	for index, panelTop := range layout.panelTops {
		panelHeight := layout.panelHeights[index]
		if y < panelTop || y >= panelTop+panelHeight {
			continue
		}
		mode := resourceModes[index]
		m.app.Mode = mode
		m.app.FocusMain = false
		items := m.app.Items[mode]
		visible := max(1, panelHeight-2)
		selected := m.app.Selected[mode]
		start := max(0, min(selected-visible+1, len(items)-visible))
		row := y - panelTop - 1
		if row >= 0 && row < visible && start+row < len(items) {
			m.app.Selected[mode] = start + row
			m.app.DetailMode = "summary"
			return m.detailCmd()
		}
		return nil
	}
	return nil
}

func (m Model) mouseContextPanel(layout uiLayout, x, y int) tea.Cmd {
	for index, panelTop := range layout.panelTops {
		panelHeight := layout.panelHeights[index]
		if y < panelTop || y >= panelTop+panelHeight {
			continue
		}
		mode := resourceModes[index]
		items := m.app.Items[mode]
		visible := max(1, panelHeight-2)
		selected := m.app.Selected[mode]
		start := max(0, min(selected-visible+1, len(items)-visible))
		row := y - panelTop - 1
		if row < 0 || row >= visible || start+row >= len(items) {
			return nil
		}
		m.app.Mode = mode
		m.app.Selected[mode] = start + row
		m.app.DetailMode = "summary"
		m.app.FocusMain = false
		m.app.openMenu()
		return m.detailCmd()
	}
	return nil
}

func (m Model) mouseTab(layout uiLayout, x int) tea.Cmd {
	position := layout.rightX + 2
	for _, tab := range m.app.detailTabs() {
		label := "[" + titleText(tab) + "]"
		if tab != m.app.DetailMode {
			label = titleText(tab)
		}
		end := position + len([]rune(label))
		if x >= position && x < end {
			m.app.DetailMode = tab
			m.app.FocusMain = true
			m.app.DetailScroll = 0
			if tab == "logs" {
				m.app.LogsFollow = true
			}
			return m.detailCmd()
		}
		position = end + 2
	}
	return nil
}

func titleText(value string) string {
	if value == "" {
		return value
	}
	return string([]rune{rune(value[0] - 'a' + 'A')}) + value[1:]
}

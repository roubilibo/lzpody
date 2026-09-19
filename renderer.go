package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type textRegion struct {
	start, end int
	style      lipgloss.Style
}

func addRegion(regions [][]textRegion, row, start, end int, style lipgloss.Style) {
	if row < 0 || row >= len(regions) || start >= end {
		return
	}
	regions[row] = append(regions[row], textRegion{start: start, end: end, style: style})
}

func addTextRegion(regions [][]textRegion, row, column, width int, text string, style lipgloss.Style) {
	if width <= 0 {
		return
	}
	runes := []rune(cleanText(text))
	if len(runes) > width {
		runes = runes[:width]
	}
	addRegion(regions, row, column, column+len(runes), style)
}

func styleLine(line string, base lipgloss.Style, regions []textRegion) string {
	runes := []rune(line)
	if len(runes) == 0 {
		return base.Render("")
	}
	styleIndexes := make([]int, len(runes))
	styles := []lipgloss.Style{base}
	for regionIndex, region := range regions {
		styles = append(styles, region.style)
		start := max(0, region.start)
		end := min(len(runes), region.end)
		for index := start; index < end; index++ {
			styleIndexes[index] = regionIndex + 1
		}
	}
	var output strings.Builder
	start := 0
	for start < len(runes) {
		styleIndex := styleIndexes[start]
		end := start + 1
		for end < len(runes) && styleIndexes[end] == styleIndex {
			end++
		}
		output.WriteString(styles[styleIndex].Render(string(runes[start:end])))
		start = end
	}
	return output.String()
}
func clip(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = cleanText(text)
	runes := []rune(text)
	if len(runes) > width {
		return string(runes[:width])
	}
	return text + strings.Repeat(" ", width-len(runes))
}

func cleanText(text string) string {
	var cleaned strings.Builder
	for _, character := range text {
		if character == '\t' || character >= 32 {
			cleaned.WriteRune(character)
		}
	}
	return cleaned.String()
}
func putLine(lines []string, row, column, width int, text string) {
	if row < 0 || row >= len(lines) || column >= len([]rune(lines[row])) || width <= 0 {
		return
	}
	prefix := []rune(lines[row])
	value := []rune(cleanText(text))
	if len(value) > width {
		value = value[:width]
	}
	for i, char := range value {
		index := column + i
		if index >= len(prefix) {
			break
		}
		prefix[index] = char
	}
	lines[row] = string(prefix)
}
func boxLine(width int, left, right rune) string {
	if width < 2 {
		return strings.Repeat("─", width)
	}
	return string(left) + strings.Repeat("─", width-2) + string(right)
}
func (a *App) frame(width, height int) string {
	uiTheme := loadUITheme()
	lines := make([]string, height)
	styles := make([]lipgloss.Style, height)
	regions := make([][]textRegion, height)
	for i := range lines {
		lines[i] = strings.Repeat(" ", width)
		styles[i] = uiTheme.Normal
	}
	if height < 22 || width < 70 {
		lines[0] = clip("Terminal too small. Minimum size is 70x22.", width)
		return frameText(lines, styles, regions)
	}
	headerTitle := " lzpody "
	headerSubtitle := "native Libpod · " + uiTheme.Name
	putLine(lines, 0, 0, width, headerTitle)
	putLine(lines, 0, 14, width-14, headerSubtitle)
	addTextRegion(regions, 0, 0, width, headerTitle, uiTheme.Title)
	addTextRegion(regions, 0, 14, width-14, headerSubtitle, uiTheme.Muted)
	refreshText := "[F5] refresh  [q] quit"
	refreshColumn := max(1, width-25)
	putLine(lines, 1, refreshColumn, width-refreshColumn, refreshText)
	addTextRegion(regions, 1, refreshColumn, width-refreshColumn, refreshText, uiTheme.Muted)
	if a.Filter != "" {
		filterText := "Filter: " + a.Filter
		putLine(lines, 1, 1, width/2, filterText)
		addTextRegion(regions, 1, 1, width/2, filterText, uiTheme.Muted)
	}
	top, bottom := 2, height-3
	leftWidth := max(32, width/3)
	rightX := leftWidth + 2
	rightWidth := width - rightX - 1
	leftHeight := bottom - top + 1
	panelHeight, extraPanels := leftHeight/len(resourceModes), leftHeight%len(resourceModes)
	for index, mode := range resourceModes {
		panelTop := top + index*panelHeight
		panelH := panelHeight
		if index < extraPanels {
			panelH++
			panelTop += index
		} else {
			panelTop += extraPanels
		}
		panelStyle := uiTheme.Border
		if mode == a.Mode && !a.FocusMain {
			panelStyle = uiTheme.Title
		}
		addRegion(regions, panelTop, 1, leftWidth+1, panelStyle)
		putLine(lines, panelTop, 1, leftWidth, boxLine(leftWidth, '┌', '┐'))
		panelTitle := fmt.Sprintf("[%d] %s (%d)", index+1, resourceLabels[mode], len(a.Items[mode]))
		putLine(lines, panelTop, 3, leftWidth-4, panelTitle)
		addTextRegion(regions, panelTop, 3, leftWidth-4, panelTitle, panelStyle)
		for r := 1; r < panelH-1; r++ {
			putLine(lines, panelTop+r, 1, leftWidth, "│")
			putLine(lines, panelTop+r, leftWidth, 1, "│")
			addRegion(regions, panelTop+r, 1, 2, panelStyle)
			addRegion(regions, panelTop+r, leftWidth, leftWidth+1, panelStyle)
		}
		putLine(lines, panelTop+panelH-1, 1, leftWidth, boxLine(leftWidth, '└', '┘'))
		addRegion(regions, panelTop+panelH-1, 1, leftWidth+1, panelStyle)
		visible := max(1, panelH-2)
		selected := a.Selected[mode]
		start := max(0, min(selected-visible+1, len(a.Items[mode])-visible))
		for row, item := range a.Items[mode][start:min(start+visible, len(a.Items[mode]))] {
			marker := "○"
			if item.Kind == "image" {
				marker = "◆"
			} else if item.Kind == "volume" || item.Kind == "network" {
				marker = "◇"
			} else if strings.EqualFold(item.State, "running") || strings.EqualFold(item.State, "running (healthy)") {
				marker = "●"
			}
			label := marker + " " + item.Name + "  " + item.State
			if item.Kind == "container" {
				label = marker + " " + item.Name + "  " + containerStateLabel(item) + fmt.Sprintf("  %5.2f%%", item.CPU)
			}
			if item.Kind == "image" {
				imageName := item.Name
				if slash := strings.LastIndex(imageName, "/"); slash >= 0 {
					imageName = imageName[slash+1:]
				}
				label = marker + " " + imageName + "  " + defaultText(item.Status, "unknown size")
			}
			if start+row == selected && mode == a.Mode && !a.FocusMain {
				label = "> " + label
			}
			labelWidth := leftWidth - 3
			putLine(lines, panelTop+1+row, 2, labelWidth, label)
			itemStyle := uiTheme.Stopped
			if strings.ToLower(item.State) == "running" {
				itemStyle = uiTheme.Running
			}
			if start+row == selected && mode == a.Mode && !a.FocusMain {
				itemStyle = uiTheme.Selected
			}
			addTextRegion(regions, panelTop+1+row, 2, labelWidth, label, itemStyle)
		}
		if len(a.Items[mode]) == 0 && panelH >= 4 {
			emptyText := "(empty)"
			putLine(lines, panelTop+1, 3, leftWidth-4, emptyText)
			addTextRegion(regions, panelTop+1, 3, leftWidth-4, emptyText, uiTheme.Muted)
		}
	}
	mainStyle := uiTheme.Border
	if a.FocusMain {
		mainStyle = uiTheme.Title
	}
	putLine(lines, top, rightX, rightWidth, boxLine(rightWidth, '┌', '┐'))
	addRegion(regions, top, rightX, rightX+rightWidth, mainStyle)
	for r := 1; r < bottom-top; r++ {
		putLine(lines, top+r, rightX, 1, "│")
		putLine(lines, top+r, rightX+rightWidth-1, 1, "│")
		addRegion(regions, top+r, rightX, rightX+1, mainStyle)
		addRegion(regions, top+r, rightX+rightWidth-1, rightX+rightWidth, mainStyle)
	}
	putLine(lines, bottom, rightX, rightWidth, boxLine(rightWidth, '└', '┘'))
	addRegion(regions, bottom, rightX, rightX+rightWidth, mainStyle)
	item := a.current()
	title := "Details"
	if item != nil {
		title = item.Name
	}
	position := ""
	if len(a.DetailLines) > 0 {
		first := a.DetailScroll + 1
		last := min(len(a.DetailLines), a.DetailScroll+max(1, bottom-top-2))
		position = fmt.Sprintf("  (%d-%d/%d)", first, last, len(a.DetailLines))
	}
	detailTitle := title + " [" + a.DetailMode + "]" + position
	putLine(lines, top, rightX+2, rightWidth-4, detailTitle)
	addTextRegion(regions, top, rightX+2, rightWidth-4, detailTitle, uiTheme.Title)
	tabs := []string{}
	for _, tab := range a.detailTabs() {
		label := strings.Title(tab)
		if tab == a.DetailMode {
			tabs = append(tabs, "["+label+"]")
		} else {
			tabs = append(tabs, label)
		}
	}
	tabText := strings.Join(tabs, "  ")
	putLine(lines, top+1, rightX+2, rightWidth-4, tabText)
	addTextRegion(regions, top+1, rightX+2, rightWidth-4, tabText, uiTheme.Key)
	detailRows := bottom - top - 2
	a.DetailViewRows = max(1, detailRows)
	start := max(0, min(a.DetailScroll, max(0, len(a.DetailLines)-detailRows)))
	for index, line := range a.DetailLines[start:min(start+detailRows, len(a.DetailLines))] {
		putLine(lines, top+2+index, rightX+2, rightWidth-4, line)
	}
	putLine(lines, height-2, 1, width-2, a.Status)
	statusLower := strings.ToLower(a.Status)
	statusStyle := uiTheme.Muted
	if strings.Contains(statusLower, "error") || strings.Contains(statusLower, "cannot") || strings.Contains(statusLower, "not found") || strings.Contains(statusLower, "invalid") || strings.Contains(statusLower, "no item") || strings.Contains(statusLower, "api 4") || strings.Contains(statusLower, "api 5") {
		statusStyle = uiTheme.Error
	}
	addTextRegion(regions, height-2, 1, width-2, a.Status, statusStyle)
	footer := "←/→ h/l panels  ↑/↓ j/k items  Tab  1-5 focus  Enter main  [/] tabs  x/? menu  / filter  q quit"
	if a.FocusMain {
		footer = "↑/↓ j/k scroll  PgUp/PgDn Ctrl-U/D  Home/End  [/] tabs  Esc panels  x/? menu  q quit"
	}
	if a.MenuOpen {
		footer = "↑/↓ j/k select  Enter/Space choose  Esc/q close menu"
	}
	putLine(lines, height-1, 1, width-2, footer)
	addTextRegion(regions, height-1, 1, width-2, footer, uiTheme.Key)
	if a.MenuOpen {
		entries := a.menuEntries()
		boxWidth := min(42, max(24, width-6))
		boxHeight := min(len(entries)+2, max(5, height-4))
		menuTop := max(1, (height-boxHeight)/2)
		menuLeft := max(2, (width-boxWidth)/2)
		menuStyle := uiTheme.Title
		putLine(lines, menuTop, menuLeft, boxWidth, boxLine(boxWidth, '┌', '┐'))
		addRegion(regions, menuTop, menuLeft, menuLeft+boxWidth, menuStyle)
		for row := 1; row < boxHeight-1; row++ {
			putLine(lines, menuTop+row, menuLeft, boxWidth, "│"+strings.Repeat(" ", boxWidth-2)+"│")
			addRegion(regions, menuTop+row, menuLeft, menuLeft+boxWidth, uiTheme.Normal)
		}
		putLine(lines, menuTop+boxHeight-1, menuLeft, boxWidth, boxLine(boxWidth, '└', '┘'))
		addRegion(regions, menuTop+boxHeight-1, menuLeft, menuLeft+boxWidth, menuStyle)
		putLine(lines, menuTop, menuLeft+2, boxWidth-4, "Actions")
		visible := max(1, boxHeight-2)
		start := max(0, min(a.MenuIndex-visible+1, len(entries)-visible))
		for index, entry := range entries[start:min(start+visible, len(entries))] {
			row := menuTop + 1 + index
			putLine(lines, row, menuLeft+2, boxWidth-4, entry[0])
			if start+index == a.MenuIndex {
				addRegion(regions, row, menuLeft+2, menuLeft+boxWidth-2, uiTheme.Selected)
			} else {
				addRegion(regions, row, menuLeft+2, menuLeft+boxWidth-2, uiTheme.Normal)
			}
		}
	}
	if a.FilterInput {
		filterText := "Filter: " + a.FilterDraft
		putLine(lines, height-2, 1, width-2, filterText)
		addTextRegion(regions, height-2, 1, width-2, filterText, uiTheme.Normal.Bold(true))
	}
	if a.ConfirmAction != "" {
		drawConfirmOverlay(lines, styles, regions, width, height, uiTheme, a)
	}
	return frameText(lines, styles, regions)
}

func drawConfirmOverlay(lines []string, styles []lipgloss.Style, regions [][]textRegion, width, height int, theme UITheme, app *App) {
	item := app.current()
	if item == nil {
		return
	}
	prompt := strings.Title(app.ConfirmAction) + " " + item.Name + "?"
	boxWidth := min(max(40, len([]rune(prompt))+8), max(40, width-6))
	boxHeight := min(7, max(5, height-2))
	top := max(1, (height-boxHeight)/2)
	left := max(2, (width-boxWidth)/2)
	putLine(lines, top, left, boxWidth, boxLine(boxWidth, '┌', '┐'))
	addRegion(regions, top, left, left+boxWidth, theme.Title)
	for row := top + 1; row < top+boxHeight-1; row++ {
		putLine(lines, row, left, boxWidth, " "+strings.Repeat(" ", boxWidth-2)+" ")
		addRegion(regions, row, left+1, left+boxWidth-1, theme.Selected)
	}
	putLine(lines, top+boxHeight-1, left, boxWidth, boxLine(boxWidth, '└', '┘'))
	addRegion(regions, top+boxHeight-1, left, left+boxWidth, theme.Title)
	title := "Confirm action"
	putLine(lines, top, left+2, boxWidth-4, title)
	addTextRegion(regions, top, left+2, boxWidth-4, title, theme.Title)
	messageLeft := left + max(2, (boxWidth-len([]rune(prompt)))/2)
	putLine(lines, top+2, messageLeft, boxWidth-(messageLeft-left), prompt)
	addTextRegion(regions, top+2, messageLeft, boxWidth-(messageLeft-left), prompt, theme.Selected.Bold(true))
	controls := "Y/Enter confirm   N/Esc cancel"
	controlsLeft := left + max(2, (boxWidth-len([]rune(controls)))/2)
	putLine(lines, top+4, controlsLeft, boxWidth-(controlsLeft-left), controls)
	addTextRegion(regions, top+4, controlsLeft, boxWidth-(controlsLeft-left), controls, theme.Key)
}

func frameText(lines []string, styles []lipgloss.Style, regions [][]textRegion) string {
	var frame strings.Builder
	for index, line := range lines {
		var base lipgloss.Style
		if index < len(styles) {
			base = styles[index]
		}
		var rowRegions []textRegion
		if index < len(regions) {
			rowRegions = regions[index]
		}
		frame.WriteString(styleLine(line, base, rowRegions))
		if index < len(lines)-1 {
			frame.WriteString("\r\n")
		}
	}
	return frame.String()
}

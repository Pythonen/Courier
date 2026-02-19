package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// handleHistoryKeys handles key input when the history pane is focused.
func (m *model) handleHistoryKeys(keyStr string) {
	switch keyStr {
	case "up", "k":
		if m.historyPos > 0 {
			m.historyPos--
		}
	case "down", "j":
		if m.historyPos < len(m.history)-1 {
			m.historyPos++
		}
	case "enter":
		if len(m.history) > 0 {
			item := m.history[m.historyPos]
			m.urlInput.SetValue(item.url)
			for i, method := range methods {
				if method == item.method {
					m.methodIdx = i
					break
				}
			}
		}
	}
}

// viewHistory renders the history sidebar.
func (m model) viewHistory(contentHeight int) string {
	border := blurredBorder
	if m.focus == paneHistory {
		border = focusedBorder
	}

	label := labelStyle.Render("History")

	var items []string
	visible := contentHeight - 4
	if visible < 1 {
		visible = 1
	}
	for i, item := range m.history {
		if i >= visible {
			break
		}

		urlStr := item.url
		maxURL := historyWidth - 12
		if maxURL < 5 {
			maxURL = 5
		}
		if len(urlStr) > maxURL {
			urlStr = urlStr[:maxURL-1] + "…"
		}

		var line string
		if m.focus == paneHistory && i == m.historyPos {
			line = lipgloss.NewStyle().
				Background(lipgloss.Color("57")).
				Foreground(lipgloss.Color("230")).
				Width(historyWidth - 2).
				Render(item.method + " " + urlStr)
		} else {
			line = historyMethodStyle.Render(item.method) + historyItemStyle.Render(urlStr)
		}
		items = append(items, line)
	}

	if len(items) == 0 {
		items = append(items, historyItemStyle.Render("No history yet"))
	}

	content := label + "\n" + strings.Join(items, "\n")
	return border.
		Width(historyWidth).
		Height(contentHeight).
		Render(content)
}

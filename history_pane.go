package main

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func (m *model) handleHistoryKeys(keyStr string) {
	switch keyStr {
	// NOTE: Let's see down the line if this immediate preview / populating panes has some perf issues
	case "up", "k":
		if m.historyPos > 0 {
			m.historyPos--
		}
		if len(m.history) > 0 {
			// TODO: should this be centralized somehow? We are mutating the model from all over the place.
			item := m.history[m.historyPos]
			// TODO: populate auth and params as well when the panes are implemented
			m.urlInput.SetValue(item.url)
			m.bodyInput.SetValue(item.requestComponents["body"])
			m.response = item.responseComponents["body"]
			m.responseHeaders = item.responseComponents["headers"]
			m.responseModel.SetContent(m.response)
			m.responseHeadersModel.SetContent(m.responseHeaders)
			for i, method := range methods {
				if method == item.method {
					m.methodIdx = i
					break
				}
			}
		}
	// NOTE: Let's see down the line if this immediate preview / populating the panes has some perf issues
	case "down", "j":
		if m.historyPos < len(m.history)-1 {
			m.historyPos++
		}
		if len(m.history) > 0 {
			item := m.history[m.historyPos]
			m.urlInput.SetValue(item.url)
			m.bodyInput.SetValue(item.requestComponents["body"])
			m.response = item.responseComponents["body"]
			m.responseHeaders = item.responseComponents["headers"]
			m.responseModel.SetContent(m.response)
			m.responseHeadersModel.SetContent(m.responseHeaders)
			for i, method := range methods {
				if method == item.method {
					m.methodIdx = i
					break
				}
			}
		}
	case "enter":
		if len(m.history) > 0 {
			m.focus = paneURL
		}
	}
}

func (m model) viewHistory(contentHeight int) string {
	border := blurredBorder
	if m.focus == paneHistory {
		border = focusedBorder
	}

	label := labelStyle.Render("History")

	// TODO: Let's group history items based on ??, such that we don't create duplicate history entries
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

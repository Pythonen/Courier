package main

import (
	"strings"
)

// responseTab tracks which tab is active in the response pane.
type responseTab int

const (
	responseTabBody responseTab = iota
	responseTabHeaders
)

// handleResponseKeys handles key input when the response pane is focused.
// Left/right arrows switch between Body and Headers tabs.
func (m *model) handleResponseKeys(keyStr string) {
	switch keyStr {
	case "left", "h":
		m.responseTab = responseTabBody
	case "right", "l":
		m.responseTab = responseTabHeaders
	}
}

// viewResponse renders the response pane with a tab bar (Body / Headers).
func (m model) viewResponse(mainWidth, height int) string {
	border := blurredBorder
	if m.focus == paneResponse {
		border = focusedBorder
	}

	// Tab bar
	bodyTab := inactiveTabStyle.Render("Body")
	headersTab := inactiveTabStyle.Render("Headers")
	if m.responseTab == responseTabBody {
		bodyTab = activeTabStyle.Render("Body")
	} else {
		headersTab = activeTabStyle.Render("Headers")
	}
	tabBar := bodyTab + " " + headersTab

	// Is this stupid to render the styles twice or okay?
	content := responseStyle.Render(m.response)
	if m.responseTab == responseTabHeaders {
		content = responseStyle.Render(m.responseHeaders)
	}

	// Truncate to fit
	lines := strings.Split(content, "\n")
	maxLines := height - 4 // room for tab bar + border
	if maxLines < 1 {
		maxLines = 1
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	text := strings.Join(lines, "\n")

	return border.
		Width(mainWidth).
		Height(height).
		Render(tabBar + "\n" + text)
}

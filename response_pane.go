package main

// responseTab tracks which tab is active in the response pane.
type responseTab int

const (
	responseTabBody responseTab = iota
	responseTabHeaders
	responseTabCount
)

// handleResponseKeys handles key input when the response pane is focused.
// Left/right arrows switch between Body and Headers tabs.
func (m *model) handleResponseKeys(keyStr string) {
	switch keyStr {
	case "left", "h":
		m.responseTab = (m.responseTab + 1) % responseTabCount
	case "right", "l":
		m.responseTab = (m.responseTab - 1) % responseTabCount
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

	m.response.Width = mainWidth - 2
	m.response.Height = height - 3
	m.responseHeaders.Width = mainWidth - 2
	m.responseHeaders.Height = height - 3
	content := m.response.View()
	content = responseStyle.Render(content)
	if m.responseTab == responseTabHeaders {
		content = m.responseHeaders.View()
		content = responseStyle.Render(content)
	}

	return border.
		Width(mainWidth).
		Height(height).
		Render(tabBar + "\n" + content)
}

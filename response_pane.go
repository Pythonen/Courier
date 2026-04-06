package main

// responseTab tracks which tab is active in the response pane.
type responseTab int

const (
	responseTabBody responseTab = iota
	responseTabHeaders
	responseTabCount
)

func (m *model) handleResponseKeys(keyStr string) {
	switch keyStr {
	case "left", "h":
		m.responseTab = (m.responseTab - 1 + responseTabCount) % responseTabCount
	case "right", "l":
		m.responseTab = (m.responseTab + 1) % responseTabCount
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

	m.responseModel.Width = mainWidth - 2 // TODO: These are no good, see https://leg100.github.io/en/posts/building-bubbletea-programs/#7-layout-arithmetic-is-error-prone
	m.responseModel.Height = height - 3
	m.responseHeadersModel.Width = mainWidth - 2
	m.responseHeadersModel.Height = height - 3
	content := m.responseModel.View()
	content = responseStyle.Render(content)
	if m.responseTab == responseTabHeaders {
		content = m.responseHeadersModel.View()
		content = responseStyle.Render(content)
	}

	return border.
		Width(mainWidth).
		Height(height).
		Render(tabBar + "\n" + content)
}

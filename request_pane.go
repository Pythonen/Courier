package main

type requestTab int

const (
	requestTabBody requestTab = iota
	requestTabParams
	requestTabAuth
	requestTabHeaders
	requestTabCount
)

func (m *model) handleRequestKeys(keyStr string) {
	switch keyStr {
	case "left", "h":
		m.requestTab = (m.requestTab - 1 + requestTabCount) % requestTabCount
	case "right", "l":
		m.requestTab = (m.requestTab + 1) % requestTabCount
	}
}

func (m model) viewRequest(mainWidth, height int) string {
	m.bodyInput.SetHeight(height - 2)

	border := blurredBorder
	if m.focus == paneRequest {
		border = focusedBorder
	}

	bodyTab := inactiveTabStyle.Render("Body")
	headersTab := inactiveTabStyle.Render("Headers")
	authTab := inactiveTabStyle.Render("Authorization")
	paramsTab := inactiveTabStyle.Render("Params")

	switch m.requestTab {
	case requestTabBody:
		bodyTab = activeTabStyle.Render("Body")
	case requestTabParams:
		paramsTab = activeTabStyle.Render("Params")
	case requestTabAuth:
		authTab = activeTabStyle.Render("Authorization")
	case requestTabHeaders:
		headersTab = activeTabStyle.Render("Headers")
	}
	tabBar := bodyTab + " " + paramsTab + " " + authTab + " " + headersTab

	return border.
		Width(mainWidth).
		Height(height).
		Render(tabBar + "\n" + m.bodyInput.View())
}

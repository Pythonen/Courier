package tui

import (
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
	"strings"
)

type requestTab int

const (
	requestTabBody requestTab = iota
	requestTabParams
	requestTabAuth
	requestTabHeaders
	requestTabCookies
	requestTabTests
	requestTabCount
)

func (m *model) handleRequestKeys(keyStr string) {
	switch keyStr {
	case "left":
		m.requestTab = (m.requestTab - 1 + requestTabCount) % requestTabCount
	case "right":
		m.requestTab = (m.requestTab + 1) % requestTabCount
	}
}

func (m model) viewRequest(mainWidth, height int) string {
	m.bodyInput.SetHeight(max(1, height-3))
	innerWidth := max(1, mainWidth-2)
	innerHeight := max(1, height-1)

	borderColor := lipgloss.Color("240")
	if m.focus == paneRequest {
		borderColor = lipgloss.Color("212")
	}

	// Use a border with no bottom so we can draw our own
	noBottomBorder := lipgloss.RoundedBorder()
	noBottomBorder.BottomLeft = "│"
	noBottomBorder.Bottom = " "
	noBottomBorder.BottomRight = "│"

	border := lipgloss.NewStyle().
		Border(noBottomBorder).
		BorderForeground(borderColor)

	bodyTab := zone.Mark("bodyTab", inactiveTabStyle.Render("Body"))
	headersTab := zone.Mark("headersTab", inactiveTabStyle.Render("Headers"))
	authTab := zone.Mark("authTab", inactiveTabStyle.Render("Auth"))
	paramsTab := zone.Mark("paramsTab", inactiveTabStyle.Render("Query"))
	cookiesTab := zone.Mark("cookiesTab", inactiveTabStyle.Render("Cookies"))
	testsTab := zone.Mark("testsTab", inactiveTabStyle.Render("Tests"))

	switch m.requestTab {
	case requestTabBody:
		bodyTab = activeTabStyle.Render("Body")
	case requestTabParams:
		paramsTab = activeTabStyle.Render("Query")
	case requestTabAuth:
		authTab = activeTabStyle.Render("Auth")
	case requestTabHeaders:
		headersTab = activeTabStyle.Render("Headers")
	case requestTabCookies:
		cookiesTab = activeTabStyle.Render("Cookies")
	case requestTabTests:
		testsTab = activeTabStyle.Render("Tests")
	}
	tabBar := bodyTab + paramsTab + authTab + headersTab + cookiesTab + testsTab
	tabBar = truncateLines(tabBar, innerWidth)

	// Conditional content based on active tab
	var content string
	switch m.requestTab {
	case requestTabHeaders:
		content = m.headersInput.View()
	case requestTabParams:
		content = m.paramsInput.View()
	case requestTabAuth:
		content = m.authInput.View()
	case requestTabBody:
		content = m.viewBody()
	case requestTabCookies:
		content = m.cookiesInput.View()
	case requestTabTests:
		content = m.testsInput.View()
	}
	if m.settingsOpen {
		tabBar = activeTabStyle.Render("Settings") + hintStyle.Render("  Request transport")
		content = m.settings.View()
	} else if m.environmentOpen {
		tabBar = activeTabStyle.Render("Environment: "+m.activeEnvironmentName()) + hintStyle.Render("  p:next n:new r:rename dd:delete")
		if m.environmentNameOpen {
			action := "Rename: "
			if m.environmentCreating {
				action = "New: "
			}
			tabBar = activeTabStyle.Render(action) + m.environmentNameInput.View()
		}
		content = m.variablesInput.View()
	}

	content = truncateLines(content, innerWidth)
	canvas := lipgloss.NewStyle().
		Width(innerWidth).
		MaxWidth(innerWidth).
		Height(innerHeight).
		MaxHeight(innerHeight).
		Render(tabBar + "\n" + content)
	box := zone.Mark("request", border.Render(canvas))

	// Build custom bottom border with mode indicator embedded
	bdrStyle := lipgloss.NewStyle().Foreground(borderColor)

	modeText := ""
	if m.focus == paneRequest {
		if m.inputMode == modeInsert {
			modeText = " INSERT "
		} else {
			modeText = " NORMAL "
		}
	}

	bottomInnerWidth := max(1, mainWidth-2)
	if modeText != "" {
		modeRendered := modeIndicatorStyle.Render(modeText)
		modeWidth := lipgloss.Width(modeRendered)
		leftDash := 2
		rightDash := bottomInnerWidth - leftDash - modeWidth
		if rightDash < 1 {
			rightDash = 1
		}
		bottomLine := bdrStyle.Render("╰"+strings.Repeat("─", leftDash)) +
			modeRendered +
			bdrStyle.Render(strings.Repeat("─", rightDash)+"╯")
		box += "\n" + bottomLine
	} else {
		bottomLine := bdrStyle.Render("╰" + strings.Repeat("─", bottomInnerWidth) + "╯")
		box += "\n" + bottomLine
	}

	return box
}

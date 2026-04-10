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
	m.bodyInput.SetHeight(height - 2)

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
	authTab := zone.Mark("authTab", inactiveTabStyle.Render("Authorization"))
	paramsTab := zone.Mark("paramsTab", inactiveTabStyle.Render("Params"))

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

	// Conditional content based on active tab
	var content string
	switch m.requestTab {
	case requestTabHeaders:
		content = m.headersInput.View()
	case requestTabBody:
		content = m.bodyInput.View()
	default:
		content = hintStyle.Render("  (not implemented)")
	}

	box := zone.Mark("request", border.
		Width(mainWidth).
		Height(height).
		Render(tabBar+"\n"+content))

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

	// +2 for the left/right border columns
	innerWidth := mainWidth + 2
	if modeText != "" {
		modeRendered := modeIndicatorStyle.Render(modeText)
		modeWidth := lipgloss.Width(modeRendered)
		leftDash := 2
		rightDash := innerWidth - leftDash - modeWidth - 2 // -2 for corners
		if rightDash < 1 {
			rightDash = 1
		}
		bottomLine := bdrStyle.Render("╰"+strings.Repeat("─", leftDash)) +
			modeRendered +
			bdrStyle.Render(strings.Repeat("─", rightDash)+"╯")
		box += "\n" + bottomLine
	} else {
		bottomLine := bdrStyle.Render("╰" + strings.Repeat("─", innerWidth-2) + "╯")
		box += "\n" + bottomLine
	}

	return box
}

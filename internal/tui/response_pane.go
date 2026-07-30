package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
)

// responseTab tracks which tab is active in the response pane.
type responseTab int

const (
	responseTabBody responseTab = iota
	responseTabHeaders
	responseTabTests
	responseTabCount
)

// handleResponseKeys reports whether the key was consumed by response navigation.
func (m *model) handleResponseKeys(keyStr string) bool {
	switch keyStr {
	case "left", "h":
		m.responseTab = (m.responseTab - 1 + responseTabCount) % responseTabCount
		m.resetActiveResponseXOffset()
		m.resetResponseSearchMatches()
		return true
	case "right", "l":
		m.responseTab = (m.responseTab + 1) % responseTabCount
		m.resetActiveResponseXOffset()
		m.resetResponseSearchMatches()
		return true
	case "H":
		m.activeResponseViewport().ScrollLeft(8)
		return true
	case "L":
		m.activeResponseViewport().ScrollRight(8)
		return true
	}
	return false
}

func (m *model) activeResponseViewport() *viewport.Model {
	switch m.responseTab {
	case responseTabHeaders:
		return &m.responseHeadersModel
	case responseTabTests:
		return &m.responseTestsModel
	default:
		return &m.responseModel
	}
}

func (m *model) resetActiveResponseXOffset() {
	m.activeResponseViewport().SetXOffset(0)
}

// viewResponse renders the response pane with a tab bar (Body / Headers).
func (m model) viewResponse(mainWidth, height int) string {
	border := blurredBorder
	if m.focus == paneResponse {
		border = focusedBorder
	}

	innerWidth := max(1, mainWidth-2)
	innerHeight := max(1, height-2)

	// Tab bar
	bodyTab := zone.Mark("responseTabBody", inactiveTabStyle.Render("Body"))
	headersTab := zone.Mark("responseTabHeaders", inactiveTabStyle.Render("Headers"))
	testsTab := zone.Mark("responseTabTests", inactiveTabStyle.Render("Tests"))
	switch m.responseTab {
	case responseTabBody:
		bodyTab = activeTabStyle.Render("Body")
	case responseTabHeaders:
		headersTab = activeTabStyle.Render("Headers")
	case responseTabTests:
		testsTab = activeTabStyle.Render("Tests")
	}
	tabBar := bodyTab + " " + headersTab + " " + testsTab
	if m.responseSearchOpen {
		tabBar += "  " + headerStyle.Render("Find: ") + m.responseSearchInput.View()
	} else if m.responseFilterOpen {
		tabBar += "  " + headerStyle.Render("Filter: ") + m.responseFilterInput.View()
	} else if m.responseSaveOpen {
		tabBar += "  " + headerStyle.Render("Save: ") + m.responseSaveInput.View()
	} else if m.responseMeta != "" || m.responseSearchStatus != "" || m.responseFilterStatus != "" {
		status := m.responseMeta
		if m.responseSearchStatus != "" {
			status = m.responseSearchStatus
		} else if m.responseFilterStatus != "" {
			status = m.responseFilterStatus
		}
		available := innerWidth - lipgloss.Width(tabBar) - 2
		if available > 0 {
			tabBar += "  " + hintStyle.Render(ansi.Truncate(status, available, "…"))
		}
	}
	// Zone markers and styled text can disagree about printable width. Clamp
	// the complete row so it can never wrap and consume response body height.
	tabBar = ansi.Truncate(tabBar, innerWidth, "")

	content := m.responseModel.View()
	switch m.responseTab {
	case responseTabHeaders:
		content = m.responseHeadersModel.View()
	case responseTabTests:
		content = m.responseTestsModel.View()
	}
	content = truncateLines(content, max(1, innerWidth-1))
	content = responseStyle.Render(content)

	// Render into an exact-size inner canvas first. Lip Gloss Height on the
	// bordered style is only a minimum; an overflowing child can otherwise
	// make the pane grow by one or more rows.
	canvas := lipgloss.NewStyle().
		Width(innerWidth).
		MaxWidth(innerWidth).
		Height(innerHeight).
		MaxHeight(innerHeight).
		Render(tabBar + "\n" + content)

	return zone.Mark("response", border.Render(canvas))
}

func innerPromptWidth(mainWidth int) int {
	return max(10, mainWidth-28)
}

func truncateLines(content string, width int) string {
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
	}
	return strings.Join(lines, "\n")
}

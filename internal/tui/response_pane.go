package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
)

// responseTab tracks which tab is active in the response pane.
type responseTab int

const (
	responseTabBody responseTab = iota
	responseTabHeaders
	responseTabCount
)

// handleResponseKeys reports whether the key was consumed as tab navigation.
func (m *model) handleResponseKeys(keyStr string) bool {
	switch keyStr {
	case "left", "h":
		m.responseTab = (m.responseTab - 1 + responseTabCount) % responseTabCount
		m.resetActiveResponseXOffset()
		return true
	case "right", "l":
		m.responseTab = (m.responseTab + 1) % responseTabCount
		m.resetActiveResponseXOffset()
		return true
	}
	return false
}

func (m *model) resetActiveResponseXOffset() {
	if m.responseTab == responseTabHeaders {
		m.responseHeadersModel.SetXOffset(0)
		return
	}
	m.responseModel.SetXOffset(0)
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
	if m.responseTab == responseTabBody {
		bodyTab = activeTabStyle.Render("Body")
	} else {
		headersTab = activeTabStyle.Render("Headers")
	}
	tabBar := bodyTab + " " + headersTab
	if m.responseMeta != "" {
		available := innerWidth - lipgloss.Width(tabBar) - 2
		if available > 0 {
			tabBar += "  " + hintStyle.Render(ansi.Truncate(m.responseMeta, available, "…"))
		}
	}
	// Zone markers and styled text can disagree about printable width. Clamp
	// the complete row so it can never wrap and consume response body height.
	tabBar = ansi.Truncate(tabBar, innerWidth, "")

	content := m.responseModel.View()
	if m.responseTab == responseTabHeaders {
		content = m.responseHeadersModel.View()
	}
	content = truncateResponseLines(content, max(1, innerWidth-1))
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

func truncateResponseLines(content string, width int) string {
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
	}
	return strings.Join(lines, "\n")
}

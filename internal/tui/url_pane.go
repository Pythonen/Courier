package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
)

// viewURL renders the URL bar: [METHOD] [url input field]
func (m model) viewURL(mainWidth int) string {
	methodText := ansi.Truncate(m.displayedMethod(), methodWidth-2, "…")
	if m.methodEditOpen {
		methodText = m.methodInput.View()
	}
	method := zone.Mark("method", methodStyle.Render(methodText))
	urlField := m.urlInput.View()
	urlBar := lipgloss.JoinHorizontal(lipgloss.Center, method, " ", urlField)

	border := blurredBorder
	if m.focus == paneURL {
		border = focusedBorder
	}

	return border.
		Width(mainWidth).
		Render(urlBar)
}

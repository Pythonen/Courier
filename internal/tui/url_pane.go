package tui

import (
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

// viewURL renders the URL bar: [METHOD] [url input field]
func (m model) viewURL(mainWidth int) string {
	method := zone.Mark("method", methodStyle.Render(methods[m.methodIdx]))
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

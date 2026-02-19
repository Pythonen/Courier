package main

// viewBody renders the request body textarea pane.
func (m model) viewBody(mainWidth, height int) string {
	m.bodyInput.SetHeight(height - 2)

	border := blurredBorder
	if m.focus == paneBody {
		border = focusedBorder
	}

	label := labelStyle.Render("Request Body")
	return border.
		Width(mainWidth).
		Height(height).
		Render(label + "\n" + m.bodyInput.View())
}

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type helpOverlayEntry struct {
	keys        string
	description string
}

type helpOverlaySection struct {
	title   string
	entries []helpOverlayEntry
}

var helpOverlaySections = []helpOverlaySection{
	{
		title: "Global navigation and actions",
		entries: []helpOverlayEntry{
			{keys: "? / F1 / Esc", description: "Open or close this keyboard guide. F1 works while editing text; ? is reserved for normal-mode panes."},
			{keys: "Tab / Shift-Tab", description: "Move to the next or previous pane. This is the tmux-safe fallback when pane-control chords are captured."},
			{keys: "Ctrl-H/J/K/L", description: "Move between panes by direction when the terminal and tmux pass these control keys through."},
			{keys: "Ctrl-S / Enter", description: "Send from normal mode with Ctrl-S; Enter sends while the URL pane is focused."},
			{keys: "Ctrl-X", description: "Cancel the active request or disconnect an active realtime session."},
			{keys: "Alt-K", description: "Connect or disconnect WebSocket, Socket.IO, and MQTT sessions."},
			{keys: "Ctrl-O / O", description: "Cycle the HTTP method or enter a custom method."},
			{keys: "Ctrl-W / Ctrl-Y", description: "Save the request or save the current response as an example."},
			{keys: "Ctrl-G / Ctrl-D", description: "Show the request as cURL or save the active response tab to a file."},
			{keys: "Ctrl-P", description: "Cycle the sidebar through History, Collections, Examples, and Cookies."},
			{keys: "Ctrl-T / Ctrl-E", description: "Open request settings or environment variables."},
			{keys: "Ctrl-C twice", description: "Confirm and quit Courier; Esc cancels the confirmation."},
		},
	},
	{
		title: "Request pane: normal and insert modes",
		entries: []helpOverlayEntry{
			{keys: "Left / Right", description: "Change the active request tab."},
			{keys: "i / Esc", description: "Enter insert mode for the selected field or return to normal mode."},
			{keys: "h j k l", description: "Move between key/value cells in table editors."},
			{keys: "o / dd", description: "Add a table row or delete the selected row."},
			{keys: "v", description: "Reveal or hide sensitive table and authentication values; leaving the pane hides them again."},
			{keys: "Body: m / f", description: "Cycle the body type or the raw-body format; j/k selects GraphQL fields."},
			{keys: "Auth: o / j k", description: "Cycle the authentication type or move between its fields."},
			{keys: "Auth: h l / Space", description: "Adjust the selected auth option. b, p, and a toggle options where shown."},
			{keys: "Auth: g", description: "Start the OAuth 2 Authorization Code login flow."},
		},
	},
	{
		title: "Sidebar",
		entries: []helpOverlayEntry{
			{keys: "j / k", description: "Change the selection without replacing the current request."},
			{keys: "Enter", description: "Load the selected history, collection, or example item."},
			{keys: "Dirty draft", description: "Loading asks before discarding edits: Enter/y discards, Esc/n keeps the draft."},
			{keys: "r / c / dd", description: "Rename, duplicate a collection request, or delete the selected item."},
			{keys: "v", description: "Reveal or hide stored cookie values in the Cookies sidebar."},
		},
	},
	{
		title: "Response pane",
		entries: []helpOverlayEntry{
			{keys: "j k / PgUp PgDn", description: "Scroll the active Body, Headers, or Tests response tab."},
			{keys: "h / l", description: "Change the active Body, Headers, or Tests response tab."},
			{keys: "H / L", description: "Scroll the active response tab horizontally."},
			{keys: "/ then n / N", description: "Find text, then move to the next or previous match."},
			{keys: "f / F", description: "Apply a JSONPath or XPath filter, or clear the active filter."},
			{keys: "Ctrl-D", description: "Save the raw body or the active Headers/Tests tab without overwriting an existing file."},
		},
	},
	{
		title: "Settings and environments",
		entries: []helpOverlayEntry{
			{keys: "Settings: p", description: "Change the settings page; j/k moves, Space toggles, and h/l adjusts values."},
			{keys: "Settings: i / Esc", description: "Edit the selected setting or return to normal mode."},
			{keys: "Settings: v", description: "Reveal or hide proxy and certificate credentials."},
			{keys: "Environment: p/n/r", description: "Select the next environment, create one, or rename the active environment."},
			{keys: "Environment: dd", description: "Delete the active environment after the second d."},
			{keys: "Environment table", description: "Use h/j/k/l, i, o, dd, and v just like other tables; values are masked by default."},
		},
	},
}

func (m model) helpShortcutAvailable() bool {
	return m.focus != paneURL &&
		!m.methodEditOpen &&
		!m.collectionRenameOpen &&
		!m.environmentNameOpen &&
		!m.responseSearchOpen &&
		!m.responseFilterOpen &&
		!m.responseSaveOpen
}

func (m *model) updateHelpOverlay(key string) {
	page := max(1, m.helpOverlayBodyHeight()-1)
	maximum := m.helpOverlayMaxOffset()

	switch key {
	case "?", "f1", "esc":
		m.helpOverlayOpen = false
		m.helpOverlayOffset = 0
	case "j", "down":
		m.helpOverlayOffset = min(maximum, m.helpOverlayOffset+1)
	case "k", "up":
		m.helpOverlayOffset = max(0, m.helpOverlayOffset-1)
	case "pgdown", "ctrl+f":
		m.helpOverlayOffset = min(maximum, m.helpOverlayOffset+page)
	case "pgup", "ctrl+b":
		m.helpOverlayOffset = max(0, m.helpOverlayOffset-page)
	case "home", "g":
		m.helpOverlayOffset = 0
	case "end", "G":
		m.helpOverlayOffset = maximum
	}
}

func (m model) viewHelpOverlay(content string) string {
	viewportWidth := max(1, m.width)
	viewportHeight := max(1, m.height)
	modal := m.helpOverlayModal()
	modalWidth, modalHeight := lipgloss.Size(modal)
	x := max(0, (viewportWidth-modalWidth)/2)
	y := max(0, (viewportHeight-modalHeight)/2)

	return lipgloss.NewCompositor(
		lipgloss.NewLayer(content),
		lipgloss.NewLayer(modal).X(x).Y(y).Z(2),
	).Render()
}

func (m model) helpOverlayModal() string {
	innerWidth := m.helpOverlayInnerWidth()
	bodyHeight := m.helpOverlayBodyHeight()
	lines := renderHelpOverlayLines(innerWidth)
	maximum := max(0, len(lines)-bodyHeight)
	offset := min(maximum, max(0, m.helpOverlayOffset))
	end := min(len(lines), offset+bodyHeight)

	body := ""
	if offset < end {
		body = strings.Join(lines[offset:end], "\n")
	}
	body = helpModalBodyStyle.
		Width(innerWidth).
		Height(bodyHeight).
		MaxHeight(bodyHeight).
		Render(body)

	position := "All shortcuts"
	if maximum > 0 {
		position = "j/k or PgUp/PgDn scroll"
		if offset > 0 {
			position = "↑ " + position
		}
		if offset < maximum {
			position += " ↓"
		}
	}

	parts := make([]string, 0, 3)
	showTitle, showFooter := m.helpOverlayChrome()
	if showTitle {
		titleText := ansi.Truncate("Courier keyboard help", innerWidth, "")
		parts = append(parts, helpModalTitleStyle.Width(innerWidth).MaxWidth(innerWidth).Render(titleText))
	}
	parts = append(parts, body)
	if showFooter {
		footerText := ansi.Truncate(position+"  •  ?/F1/Esc close", innerWidth, "")
		footer := helpModalHintStyle.
			Width(innerWidth).
			MaxWidth(innerWidth).
			Align(lipgloss.Center).
			Render(footerText)
		parts = append(parts, footer)
	}
	return m.helpOverlayContainerStyle().Render(strings.Join(parts, "\n"))
}

func (m model) helpOverlayInnerWidth() int {
	frameWidth := m.helpOverlayContainerStyle().GetHorizontalFrameSize()
	return max(1, min(94, m.width-frameWidth))
}

func (m model) helpOverlayBodyHeight() int {
	frameHeight := m.helpOverlayContainerStyle().GetVerticalFrameSize()
	availableHeight := max(1, m.height-frameHeight)
	showTitle, showFooter := m.helpOverlayChrome()
	if showTitle {
		availableHeight--
	}
	if showFooter {
		availableHeight--
	}
	return max(1, availableHeight)
}

func (m model) helpOverlayChrome() (showTitle, showFooter bool) {
	availableHeight := m.height - m.helpOverlayContainerStyle().GetVerticalFrameSize()
	return availableHeight >= 2, availableHeight >= 3
}

func (m model) helpOverlayContainerStyle() lipgloss.Style {
	style := helpModalStyle
	if m.width < 16 {
		style = style.Padding(0)
	} else if m.width < 28 {
		style = style.Padding(0, 1)
	}
	if m.height < 9 {
		style = style.PaddingTop(0).PaddingBottom(0)
	}
	if m.width < 3 || m.height < 5 {
		style = style.UnsetBorderStyle().Padding(0)
	}
	return style
}

func (m model) helpOverlayMaxOffset() int {
	return max(0, len(renderHelpOverlayLines(m.helpOverlayInnerWidth()))-m.helpOverlayBodyHeight())
}

func renderHelpOverlayLines(width int) []string {
	lines := make([]string, 0, 64)
	appendRendered := func(rendered string) {
		lines = append(lines, strings.Split(rendered, "\n")...)
	}

	for sectionIndex, section := range helpOverlaySections {
		if sectionIndex > 0 {
			lines = append(lines, helpModalBodyStyle.Width(width).Render(""))
		}
		appendRendered(helpModalSectionStyle.Width(width).MaxWidth(width).Render(section.title))
		for _, entry := range section.entries {
			if width < 38 {
				appendRendered(helpModalKeyStyle.Width(width).MaxWidth(width).Render(entry.keys))
				appendRendered(helpModalBodyStyle.Width(width).MaxWidth(width).Render(entry.description))
				continue
			}
			keyWidth := min(24, max(12, width/3))
			descriptionWidth := max(1, width-keyWidth-2)
			keyView := helpModalKeyStyle.Width(keyWidth).MaxWidth(keyWidth).Render(entry.keys)
			descriptionView := helpModalBodyStyle.Width(descriptionWidth).MaxWidth(descriptionWidth).Render(entry.description)
			row := lipgloss.JoinHorizontal(lipgloss.Top, keyView, helpModalBodyStyle.Render("  "), descriptionView)
			appendRendered(row)
		}
	}
	return lines
}

func helpOverlayDocumentation() string {
	var lines []string
	for _, section := range helpOverlaySections {
		lines = append(lines, section.title)
		for _, entry := range section.entries {
			lines = append(lines, entry.keys+" "+entry.description)
		}
	}
	return strings.Join(lines, "\n")
}

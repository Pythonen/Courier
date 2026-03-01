package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type headerRow struct {
	key   textinput.Model
	value textinput.Model
}

type headersTable struct {
	rows      []headerRow
	cursorRow int
	cursorCol int // 0 = key, 1 = value
	focused   bool
	width     int
	height    int
	viewport  viewport.Model
	pendingD  bool // waiting for second 'd' in dd
}

func newHeaderRow() headerRow {
	k := textinput.New()
	k.Prompt = ""
	k.Placeholder = "Header-Name"
	k.CharLimit = 256
	k.Blur()

	v := textinput.New()
	v.Prompt = ""
	v.Placeholder = "value"
	v.CharLimit = 2048
	v.Blur()

	return headerRow{key: k, value: v}
}

func newHeadersTable() headersTable {
	return headersTable{
		rows: []headerRow{newHeaderRow()},
	}
}

// Headers returns a map of non-empty key-value pairs.
func (h *headersTable) Headers() map[string]string {
	result := make(map[string]string)
	for _, row := range h.rows {
		k := strings.TrimSpace(row.key.Value())
		v := strings.TrimSpace(row.value.Value())
		if k != "" {
			result[k] = v
		}
	}
	return result
}

func (h *headersTable) Focus() {
	h.focused = true
}

func (h *headersTable) Blur() {
	h.focused = false
	h.blurAll()
}

func (h *headersTable) blurAll() {
	for i := range h.rows {
		h.rows[i].key.Blur()
		h.rows[i].value.Blur()
	}
}

// FocusCurrent focuses the text input at the current cursor position.
func (h *headersTable) FocusCurrent() tea.Cmd {
	h.blurAll()
	if h.cursorRow >= len(h.rows) {
		return nil
	}
	if h.cursorCol == 0 {
		return h.rows[h.cursorRow].key.Focus()
	}
	return h.rows[h.cursorRow].value.Focus()
}

// UpdateInsert forwards a message to the focused input in insert mode.
func (h *headersTable) UpdateInsert(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if h.cursorRow >= len(h.rows) {
		return nil
	}
	if h.cursorCol == 0 {
		h.rows[h.cursorRow].key, cmd = h.rows[h.cursorRow].key.Update(msg)
	} else {
		h.rows[h.cursorRow].value, cmd = h.rows[h.cursorRow].value.Update(msg)
	}
	return cmd
}

// UpdateNormal handles navigation keys in normal mode.
func (h *headersTable) UpdateNormal(keyStr string) {
	switch keyStr {
	case "h":
		h.cursorCol = 0
		h.pendingD = false
	case "l":
		h.cursorCol = 1
		h.pendingD = false
	case "j":
		if h.cursorRow < len(h.rows)-1 {
			h.cursorRow++
		}
		h.pendingD = false
	case "k":
		if h.cursorRow > 0 {
			h.cursorRow--
		}
		h.pendingD = false
	case "o":
		h.pendingD = false
		newRow := newHeaderRow()
		keyCol, valCol := h.colWidths()
		newRow.key.Width = keyCol
		newRow.value.Width = valCol
		pos := h.cursorRow + 1
		h.rows = append(h.rows, headerRow{})
		copy(h.rows[pos+1:], h.rows[pos:])
		h.rows[pos] = newRow
		h.cursorRow = pos
	case "d":
		if h.pendingD {
			// dd: delete current row
			if len(h.rows) > 1 {
				h.rows = append(h.rows[:h.cursorRow], h.rows[h.cursorRow+1:]...)
				if h.cursorRow >= len(h.rows) {
					h.cursorRow = len(h.rows) - 1
				}
			}
			h.pendingD = false
		} else {
			h.pendingD = true
		}
	default:
		h.pendingD = false
	}
}

func (h *headersTable) SetWidth(w int) {
	h.width = w
	h.viewport.Width = w
	keyCol, valCol := h.colWidths()
	for i := range h.rows {
		h.rows[i].key.Width = keyCol
		h.rows[i].value.Width = valCol
	}
}

func (h *headersTable) SetHeight(height int) {
	h.height = height
	// Reserve 1 line for the hint bar
	vpHeight := height - 1
	if vpHeight < 1 {
		vpHeight = 1
	}
	h.viewport.Height = vpHeight
}

// colWidths returns the inner widths for key and value columns.
func (h *headersTable) colWidths() (int, int) {
	// Layout: │ <key> │ <value> │  — 5 chars of chrome (3 borders + 2 spaces)
	inner := h.width - 5
	if inner < 20 {
		inner = 20
	}
	keyCol := inner / 3
	valCol := inner - keyCol
	return keyCol, valCol
}

var (
	tableBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	headerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	cellStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	activeCellStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	rowCursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	hintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true)
)

func (h *headersTable) View() string {
	keyCol, valCol := h.colWidths()
	bdr := tableBorderStyle.Render

	var b strings.Builder

	// Top border
	b.WriteString(bdr("┌" + strings.Repeat("─", keyCol+2) + "┬" + strings.Repeat("─", valCol+2) + "┐"))
	b.WriteString("\n")

	// Column headers
	keyHeader := headerStyle.Width(keyCol).Render("Header")
	valHeader := headerStyle.Width(valCol).Render("Value")
	b.WriteString(bdr("│") + " " + keyHeader + " " + bdr("│") + " " + valHeader + " " + bdr("│"))
	b.WriteString("\n")

	// Header separator
	b.WriteString(bdr("├" + strings.Repeat("─", keyCol+2) + "┼" + strings.Repeat("─", valCol+2) + "┤"))
	b.WriteString("\n")

	// Data rows
	for i, row := range h.rows {
		isActiveRow := h.focused && i == h.cursorRow

		var keyView, valView string

		if isActiveRow && h.cursorCol == 0 {
			keyView = truncOrPad(row.key.View(), keyCol)
		} else {
			v := row.key.Value()
			if v == "" {
				v = cellStyle.Faint(true).Render(row.key.Placeholder)
			} else {
				v = cellStyle.Render(v)
			}
			keyView = truncOrPad(v, keyCol)
		}

		if isActiveRow && h.cursorCol == 1 {
			valView = truncOrPad(row.value.View(), valCol)
		} else {
			v := row.value.Value()
			if v == "" {
				v = cellStyle.Faint(true).Render(row.value.Placeholder)
			} else {
				v = cellStyle.Render(v)
			}
			valView = truncOrPad(v, valCol)
		}

		// Row cursor indicator
		rowIndicator := bdr("│")
		if isActiveRow {
			if h.cursorCol == 0 {
				keyView = activeCellStyle.Render(">") + keyView
				keyView = truncOrPad(keyView, keyCol)
			} else {
				valView = activeCellStyle.Render(">") + valView
				valView = truncOrPad(valView, valCol)
			}
			rowIndicator = rowCursorStyle.Render("│")
		}

		fmt.Fprintf(&b, "%s %s %s %s %s",
			rowIndicator, keyView, bdr("│"), valView, bdr("│"))
		b.WriteString("\n")

		// Row separator (between data rows, not after the last one)
		if i < len(h.rows)-1 {
			b.WriteString(bdr("├" + strings.Repeat("─", keyCol+2) + "┼" + strings.Repeat("─", valCol+2) + "┤"))
			b.WriteString("\n")
		}
	}

	// Bottom border
	b.WriteString(bdr("└" + strings.Repeat("─", keyCol+2) + "┴" + strings.Repeat("─", valCol+2) + "┘"))

	// Render table through viewport for scrolling
	tableContent := b.String()
	h.viewport.SetContent(tableContent)

	// Auto-scroll to keep cursor row visible.
	// Each row takes 2 lines (content + separator), except the last (1 line).
	// Header takes 3 lines (top border, header, separator).
	cursorLine := 3 + h.cursorRow*2
	if cursorLine >= h.viewport.YOffset+h.viewport.Height {
		h.viewport.SetYOffset(cursorLine - h.viewport.Height + 1)
	} else if cursorLine < h.viewport.YOffset {
		h.viewport.SetYOffset(cursorLine)
	}

	return h.viewport.View() + "\n" + hintStyle.Render(" hjkl:move  i:edit  o:add  dd:delete")
}

// truncOrPad ensures s renders to exactly width visible characters.
func truncOrPad(s string, width int) string {
	w := lipgloss.Width(s)
	if w > width {
		// Rough truncation — take prefix and hope for the best with ANSI
		runes := []rune(s)
		for len(runes) > 0 && lipgloss.Width(string(runes)) > width {
			runes = runes[:len(runes)-1]
		}
		return string(runes)
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

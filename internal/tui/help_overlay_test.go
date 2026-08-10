package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestHelpOverlayOpensClosesAndBlocksOtherInput(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(model)
	originalFocus := m.focus
	originalMethod := m.methodIdx

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	m = updated.(model)
	if !m.helpOverlayOpen {
		t.Fatal("F1 did not open the help overlay")
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "Courier keyboard help") || !strings.Contains(view, "Global navigation and actions") {
		t.Fatalf("open help overlay was not rendered:\n%s", view)
	}

	updated, command := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	m = updated.(model)
	if command != nil || m.methodIdx != originalMethod || m.focus != originalFocus {
		t.Fatal("help overlay allowed a global action to change the underlying model")
	}
	updated, command = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(model)
	if command != nil || m.quitConfirmOpen || !m.helpOverlayOpen {
		t.Fatal("help overlay did not block the quit shortcut")
	}
	updated, _ = m.Update(tea.MouseReleaseMsg{X: 35, Y: 1, Button: tea.MouseLeft})
	m = updated.(model)
	if m.methodIdx != originalMethod || m.focus != originalFocus || !m.helpOverlayOpen {
		t.Fatal("help overlay did not block mouse input")
	}

	updated, _ = m.Update(tea.KeyReleaseMsg{Code: tea.KeyF1})
	m = updated.(model)
	if !m.helpOverlayOpen {
		t.Fatal("F1 key release closed the help overlay")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(model)
	if m.helpOverlayOpen {
		t.Fatal("Esc did not close the help overlay")
	}

	m.setFocus(paneResponse)
	updated, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	m = updated.(model)
	if m.helpOverlayOpen {
		t.Fatal("F1 did not close the help overlay opened with ?")
	}
}

func TestHelpOverlayScrollsToAllSections(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	m = updated.(model)

	maximum := m.helpOverlayMaxOffset()
	if maximum == 0 {
		t.Fatal("short viewport unexpectedly fit the complete help guide")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = updated.(model)
	if m.helpOverlayOffset == 0 {
		t.Fatal("PgDown did not scroll the help overlay")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = updated.(model)
	if m.helpOverlayOffset != maximum {
		t.Fatalf("End offset = %d, want %d", m.helpOverlayOffset, maximum)
	}
	if view := stripANSI(m.View().Content); !strings.Contains(view, "Environment table") {
		t.Fatalf("last help section was not visible after End:\n%s", view)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m = updated.(model)
	if m.helpOverlayOffset != 0 {
		t.Fatalf("Home offset = %d, want 0", m.helpOverlayOffset)
	}
}

func TestHelpOverlayDocumentsCoreWorkflows(t *testing.T) {
	documentation := helpOverlayDocumentation()
	for _, expected := range []string{
		"Tab / Shift-Tab",
		"tmux-safe fallback",
		"Ctrl-H/J/K/L",
		"Alt-K",
		"Request pane: normal and insert modes",
		"Reveal or hide sensitive",
		"Dirty draft",
		"Enter/y discards, Esc/n keeps",
		"Response pane",
		"JSONPath or XPath",
		"Settings and environments",
		"Environment table",
	} {
		if !strings.Contains(documentation, expected) {
			t.Errorf("help documentation does not contain %q", expected)
		}
	}
}

func TestAltKRemainsTheConnectShortcut(t *testing.T) {
	m := NewModel()
	altK := tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt}
	if !key.Matches(altK, m.keymap.connect) {
		t.Fatal("Alt-K no longer matches the realtime connect shortcut")
	}
	if key.Matches(altK, m.keymap.movePane) {
		t.Fatal("Alt-K ambiguously matches pane movement")
	}
}

func TestTabAndShiftTabRemainTmuxSafePaneFallbacks(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(model)
	if m.focus != paneRequest {
		t.Fatalf("Tab moved focus to %d, want request pane", m.focus)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(model)
	if m.focus != paneURL {
		t.Fatalf("Shift-Tab moved focus to %d, want URL pane", m.focus)
	}
}

func TestCompactFooterAdvertisesHelpAndPaneFallbacks(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	view := stripANSI(m.View().Content)
	for _, expected := range []string{"?/f1 all keys", "ctrl+hjkl move pane", "tab/shift+tab cycle pane"} {
		if !strings.Contains(view, expected) {
			t.Errorf("compact footer does not contain %q:\n%s", expected, view)
		}
	}
}

func TestQuestionMarkRemainsEditableInInsertMode(t *testing.T) {
	m := NewModel()
	m.setFocus(paneRequest)
	m.requestTab = requestTabHeaders
	m.syncRequestTabFocus()
	m.inputMode = modeInsert
	_ = m.headersInput.FocusCurrent()

	updated, _ := m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = updated.(model)
	if m.helpOverlayOpen {
		t.Fatal("? opened help while a request field was in insert mode")
	}
	if got := m.headersInput.rows[0].key.Value(); got != "?" {
		t.Fatalf("inserted header key = %q, want ?", got)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	m = updated.(model)
	if !m.helpOverlayOpen {
		t.Fatal("F1 did not open help from request insert mode")
	}
}

func TestQuestionMarkRemainsEditableInURL(t *testing.T) {
	m := NewModel()
	m.urlInput.SetValue("https://example.test/search")
	m.urlInput.CursorEnd()

	updated, _ := m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = updated.(model)
	if m.helpOverlayOpen {
		t.Fatal("? opened help while the URL field was focused")
	}
	if got := m.urlInput.Value(); got != "https://example.test/search?" {
		t.Fatalf("URL after ? = %q", got)
	}
}

func TestHelpOverlayFitsNarrowViewport(t *testing.T) {
	for _, size := range []struct{ width, height int }{
		{width: 24, height: 8},
		{width: 12, height: 5},
		{width: 2, height: 2},
	} {
		m := NewModel()
		m.width = size.width
		m.height = size.height
		modal := m.helpOverlayModal()
		width, height := lipgloss.Size(modal)
		if width > size.width || height > size.height {
			t.Errorf("modal at %dx%d rendered as %dx%d", size.width, size.height, width, height)
		}
	}
}

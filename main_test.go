package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	uuid "github.com/google/uuid"
)

func ctrlKey(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestTea_FocusAndMethodCycle(t *testing.T) {
	tm := teatest.NewTestModel(t, newModel(), teatest.WithInitialTermSize(120, 40))

	tm.Send(ctrlKey(tea.KeyCtrlO))
	tm.Send(ctrlKey(tea.KeyCtrlO))
	tm.Send(ctrlKey(tea.KeyTab))
	tm.Send(ctrlKey(tea.KeyTab))
	tm.Send(ctrlKey(tea.KeyShiftTab))
	tm.Send(ctrlKey(tea.KeyCtrlC))

	final := tm.FinalModel(t).(model)

	if got := methods[final.methodIdx]; got != "PUT" {
		t.Fatalf("method = %q, want PUT", got)
	}
	if final.focus != paneRequest {
		t.Fatalf("focus = %v, want paneRequest", final.focus)
	}
	if final.inputMode != modeNormal {
		t.Fatalf("inputMode = %v, want modeNormal", final.inputMode)
	}
}

func TestTea_RequestPaneModesAndTabs(t *testing.T) {
	tm := teatest.NewTestModel(t, newModel(), teatest.WithInitialTermSize(120, 40))

	tm.Send(ctrlKey(tea.KeyTab))
	tm.Send(runeKey('i'))
	tm.Send(ctrlKey(tea.KeyEsc))
	tm.Send(ctrlKey(tea.KeyRight))
	tm.Send(ctrlKey(tea.KeyRight))
	tm.Send(ctrlKey(tea.KeyRight))
	tm.Send(runeKey('i'))
	tm.Send(ctrlKey(tea.KeyEsc))
	tm.Send(ctrlKey(tea.KeyCtrlC))

	final := tm.FinalModel(t).(model)

	if final.focus != paneRequest {
		t.Fatalf("focus = %v, want paneRequest", final.focus)
	}
	if final.requestTab != requestTabHeaders {
		t.Fatalf("requestTab = %v, want requestTabHeaders", final.requestTab)
	}
	if final.inputMode != modeNormal {
		t.Fatalf("inputMode = %v, want modeNormal", final.inputMode)
	}
}

func TestTea_HistoryNavigationPopulatesFields(t *testing.T) {
	item0 := historyItem{
		method: "GET",
		url:    "https://example.com/0",
		requestComponents: map[string]string{
			"body": "zero",
		},
		responseComponents: map[string]string{
			"body":    "resp-zero",
			"headers": "x-zero: 1",
		},
		requestId: uuid.New(),
	}
	item1 := historyItem{
		method: "POST",
		url:    "https://example.com/1",
		requestComponents: map[string]string{
			"body": "one",
		},
		responseComponents: map[string]string{
			"body":    "resp-one",
			"headers": "x-one: 1",
		},
		requestId: uuid.New(),
	}
	item2 := historyItem{
		method: "DELETE",
		url:    "https://example.com/2",
		requestComponents: map[string]string{
			"body": "two",
		},
		responseComponents: map[string]string{
			"body":    "resp-two",
			"headers": "x-two: 1",
		},
		requestId: uuid.New(),
	}

	m := newModel()
	m.history = []historyItem{item0, item1, item2}
	m.setFocus(paneHistory)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	tm.Send(ctrlKey(tea.KeyDown))
	tm.Send(ctrlKey(tea.KeyDown))
	tm.Send(ctrlKey(tea.KeyUp))
	tm.Send(ctrlKey(tea.KeyEnter))
	tm.Send(ctrlKey(tea.KeyCtrlC))

	final := tm.FinalModel(t).(model)

	if final.historyPos != 1 {
		t.Fatalf("historyPos = %d, want 1", final.historyPos)
	}
	if final.focus != paneURL {
		t.Fatalf("focus = %v, want paneURL after enter", final.focus)
	}
	if got := final.urlInput.Value(); got != item1.url {
		t.Fatalf("url = %q, want %q", got, item1.url)
	}
	if got := final.bodyInput.Value(); got != item1.requestComponents["body"] {
		t.Fatalf("body = %q, want %q", got, item1.requestComponents["body"])
	}
	if got := methods[final.methodIdx]; got != item1.method {
		t.Fatalf("method = %q, want %q", got, item1.method)
	}
	if final.response != item1.responseComponents["body"] {
		t.Fatalf("response body = %q, want %q", final.response, item1.responseComponents["body"])
	}
	if final.responseHeaders != item1.responseComponents["headers"] {
		t.Fatalf("response headers = %q, want %q", final.responseHeaders, item1.responseComponents["headers"])
	}
}

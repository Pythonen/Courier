package tui

import (
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uuid "github.com/google/uuid"
	zone "github.com/lrstanley/bubblezone/v2"
)

var testZoneOnce sync.Once

func initTestZones() {
	testZoneOnce.Do(func() { zone.NewGlobal() })
}

func TestEnterSendsAndSnapshotsRequest(t *testing.T) {
	m := NewModel()
	m.urlInput.SetValue("https://example.test/items")
	m.bodyInput.SetValue(`{"name":"courier"}`)
	m.headersInput.SetEntries([]headerEntry{
		{key: "X-Trace", value: "one"},
		{key: "X-Trace", value: "two"},
	})

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)

	if cmd == nil {
		t.Fatal("Enter did not produce a request command")
	}
	if len(got.history) != 1 {
		t.Fatalf("history length = %d, want 1", len(got.history))
	}
	item := got.history[0]
	if item.url != "https://example.test/items" || item.requestBody != `{"name":"courier"}` {
		t.Fatalf("request snapshot = %#v", item)
	}
	if len(item.requestHeaders) != 2 || item.requestHeaders[1].value != "two" {
		t.Fatalf("header snapshot = %#v", item.requestHeaders)
	}
	if item.requestID != got.requestId {
		t.Fatal("history request ID does not match the in-flight request")
	}
}

func TestResponseUpdatesMatchingHistoryOnly(t *testing.T) {
	oldID := uuid.New()
	currentID := uuid.New()
	m := NewModel()
	m.requestId = currentID
	m.history = []historyItem{
		{requestID: currentID, url: "https://example.test/current"},
		{requestID: oldID, url: "https://example.test/old"},
	}

	updated, _ := m.Update(responseMsg{
		requestID:    oldID,
		responseBody: "old response",
		responseMeta: "200 OK",
	})
	m = updated.(model)

	if m.history[1].responseBody != "old response" {
		t.Fatal("older response was not stored in its history item")
	}
	if m.response != "" {
		t.Fatalf("older response replaced current display: %q", m.response)
	}

	updated, _ = m.Update(responseMsg{
		requestID:       currentID,
		responseBody:    "current response",
		responseHeaders: "X-Test: yes",
		responseMeta:    "201 Created",
	})
	m = updated.(model)

	if m.response != "current response" || m.responseMeta != "201 Created" {
		t.Fatalf("current response was not displayed: body=%q meta=%q", m.response, m.responseMeta)
	}
	if m.history[0].responseHeaders != "X-Test: yes" {
		t.Fatal("current response headers were not stored in history")
	}
}

func TestResponseTabNavigationDoesNotScrollContent(t *testing.T) {
	m := NewModel()
	m.focus = paneResponse
	m.responseModel.SetWidth(20)
	m.responseModel.SetHeight(2)
	m.responseHeadersModel.SetWidth(20)
	m.responseHeadersModel.SetHeight(2)
	m.responseModel.SetContent("abcdefghijklmnopqrstuvwxyz")
	m.responseHeadersModel.SetContent("Header-Name: header-value")
	m.responseModel.SetXOffset(5)
	m.responseHeadersModel.SetXOffset(5)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(model)
	if m.responseTab != responseTabHeaders {
		t.Fatalf("response tab = %d, want headers", m.responseTab)
	}
	if m.responseHeadersModel.XOffset() != 0 {
		t.Fatalf("headers horizontal offset = %d, want 0", m.responseHeadersModel.XOffset())
	}
	if got := m.responseHeadersModel.View(); !strings.HasPrefix(got, "Header-Name") {
		t.Fatalf("headers are clipped after tab switch: %q", got)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updated.(model)
	if m.responseTab != responseTabBody {
		t.Fatalf("response tab = %d, want body", m.responseTab)
	}
	if m.responseModel.XOffset() != 0 {
		t.Fatalf("body horizontal offset = %d, want 0", m.responseModel.XOffset())
	}
}

func TestResponsePaneKeepsHeightWhileScrolling(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(model)
	nestedHTML := strings.Repeat("<div>", 30) + "content" + strings.Repeat("</div>", 30)
	m.responseModel.SetContent(highlight(prettyHTML(nestedHTML), "text/html"))
	m.responseMeta = strings.Repeat("long-response-metadata ", 20)

	mainWidth, _, _, responseHeight := layoutDimensions(m.width, m.height)
	wantViewportHeight := responseHeight - 4
	if m.responseModel.Height() != wantViewportHeight {
		t.Fatalf("persisted viewport height = %d, want %d", m.responseModel.Height(), wantViewportHeight)
	}
	m.focus = paneURL
	beforeFocus := lipgloss.Height(m.viewResponse(mainWidth, responseHeight))
	beforeFocusFull := lipgloss.Height(m.View().Content)
	m.setFocus(paneResponse)
	afterFocus := lipgloss.Height(m.viewResponse(mainWidth, responseHeight))
	afterFocusFull := lipgloss.Height(m.View().Content)
	if afterFocus != beforeFocus {
		t.Fatalf("response pane height changed on focus: before=%d after=%d", beforeFocus, afterFocus)
	}
	if afterFocusFull != beforeFocusFull {
		t.Fatalf("full view height changed on response focus: before=%d after=%d", beforeFocusFull, afterFocusFull)
	}

	before := afterFocus
	if before != responseHeight {
		t.Fatalf("response pane does not honor allocated height: got %d want %d", before, responseHeight)
	}
	beforeFull := lipgloss.Height(m.View().Content)

	for range 10 {
		updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = updated.(model)
	}
	if m.responseModel.YOffset() == 0 {
		t.Fatal("long response did not scroll")
	}
	after := lipgloss.Height(m.viewResponse(mainWidth, responseHeight))
	afterFull := lipgloss.Height(m.View().Content)
	if after != before {
		t.Fatalf("response pane height changed after scrolling: before=%d after=%d", before, after)
	}
	if m.responseModel.Height() != wantViewportHeight {
		t.Fatalf("viewport height changed after scrolling: got %d want %d", m.responseModel.Height(), wantViewportHeight)
	}
	if afterFull != beforeFull {
		t.Fatalf("full view height changed after scrolling: before=%d after=%d", beforeFull, afterFull)
	}
	if afterFull > m.height {
		t.Fatalf("full view overflows terminal: view=%d terminal=%d", afterFull, m.height)
	}
}

func TestMainColumnBottomAlignsWithHistory(t *testing.T) {
	initTestZones()
	for _, size := range []tea.WindowSizeMsg{
		{Width: 80, Height: 24},
		{Width: 160, Height: 50},
		{Width: 320, Height: 80},
	} {
		m := NewModel()
		updated, _ := m.Update(size)
		m = updated.(model)

		mainWidth, contentHeight, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)
		rightColumn := lipgloss.JoinVertical(
			lipgloss.Left,
			m.viewURL(mainWidth),
			m.viewRequest(mainWidth, bodyHeight),
			m.viewResponse(mainWidth, responseHeight),
		)
		history := m.viewHistory(contentHeight)

		if got, want := lipgloss.Height(rightColumn), lipgloss.Height(history); got != want {
			t.Fatalf("column bottoms do not align at %dx%d: main=%d history=%d", size.Width, size.Height, got, want)
		}
	}
}

func TestHistoryRestoresHeadersAndScrollsSelection(t *testing.T) {
	initTestZones()
	m := NewModel()
	for i := range 5 {
		m.history = append(m.history, historyItem{
			method:         "POST",
			url:            "item-" + string(rune('a'+i)),
			requestBody:    string(rune('A' + i)),
			requestHeaders: []headerEntry{{key: "X-Item", value: string(rune('0' + i))}},
		})
	}
	m.historyPos = 3
	m.handleHistoryKeys("down")

	if m.urlInput.Value() != "item-e" || m.bodyInput.Value() != "E" {
		t.Fatalf("restored URL/body = %q/%q", m.urlInput.Value(), m.bodyInput.Value())
	}
	entries := m.headersInput.Entries()
	if len(entries) != 1 || entries[0].value != "4" {
		t.Fatalf("restored headers = %#v", entries)
	}

	view := stripANSI(m.viewHistory(6)) // two visible history rows
	if !strings.Contains(view, "item-e") {
		t.Fatalf("selected history item is off-screen:\n%s", view)
	}
}

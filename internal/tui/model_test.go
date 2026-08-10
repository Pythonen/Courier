package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	uuid "github.com/google/uuid"
	zone "github.com/lrstanley/bubblezone/v2"
)

var testZoneOnce sync.Once

func initTestZones() {
	testZoneOnce.Do(func() { zone.NewGlobal() })
}

func TestCtrlCRequiresConfirmationBeforeQuitting(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(model)
	cancelled := false
	m.cancelRequest = func() { cancelled = true }
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

	updated, cmd := m.Update(ctrlC)
	m = updated.(model)
	if cmd == nil {
		t.Fatal("first Ctrl-C did not schedule the confirmation timeout")
	}
	if !m.quitConfirmOpen {
		t.Fatal("first Ctrl-C did not open quit confirmation")
	}
	if cancelled {
		t.Fatal("first Ctrl-C cancelled active work")
	}
	const quitPrompt = "Press Ctrl-C again to close the application."
	view := m.View().Content
	if !strings.Contains(view, quitPrompt) {
		t.Fatal("quit confirmation message was not rendered")
	}
	promptColumn := -1
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if byteIndex := strings.Index(line, quitPrompt); byteIndex >= 0 {
			promptColumn = ansi.StringWidth(line[:byteIndex])
			break
		}
	}
	centerDelta := 2*promptColumn + len(quitPrompt) - m.width
	if promptColumn < 0 || centerDelta < -1 || centerDelta > 1 {
		t.Fatalf("quit confirmation prompt starts at column %d in a %d-column viewport", promptColumn, m.width)
	}

	updated, cmd = m.Update(tea.KeyReleaseMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(model)
	if cmd != nil || !m.quitConfirmOpen || cancelled {
		t.Fatal("Ctrl-C key release triggered the confirmed quit action")
	}

	updated, cmd = m.Update(ctrlC)
	m = updated.(model)
	if m.quitConfirmOpen {
		t.Fatal("second Ctrl-C left quit confirmation open")
	}
	if !cancelled {
		t.Fatal("second Ctrl-C did not cancel active work")
	}
	if cmd == nil {
		t.Fatal("second Ctrl-C did not return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("second Ctrl-C command returned %T, want tea.QuitMsg", cmd())
	}
}

func TestQuitConfirmationBlocksInputAndEscapeCancels(t *testing.T) {
	m := NewModel()
	originalFocus := m.focus
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

	updated, _ := m.Update(ctrlC)
	m = updated.(model)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(model)
	if cmd != nil || !m.quitConfirmOpen || m.focus != originalFocus {
		t.Fatal("quit confirmation did not block unrelated input")
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("Escape returned a command while cancelling quit")
	}
	if m.quitConfirmOpen {
		t.Fatal("Escape did not close quit confirmation")
	}

	updated, cmd = m.Update(ctrlC)
	m = updated.(model)
	if cmd == nil || !m.quitConfirmOpen {
		t.Fatal("Ctrl-C after cancellation did not start a fresh confirmation")
	}
}

func TestQuitConfirmationExpiresWithoutAffectingApplication(t *testing.T) {
	m := NewModel()
	originalFocus := m.focus
	cancelled := false
	m.cancelRequest = func() { cancelled = true }
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

	updated, _ := m.Update(ctrlC)
	m = updated.(model)
	firstID := m.quitConfirmID

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(model)
	updated, _ = m.Update(ctrlC)
	m = updated.(model)
	secondID := m.quitConfirmID
	if firstID == secondID {
		t.Fatal("new quit confirmation reused the previous timeout ID")
	}

	updated, cmd := m.Update(quitConfirmationExpiredMsg{id: firstID})
	m = updated.(model)
	if cmd != nil || !m.quitConfirmOpen || m.quitConfirmID != secondID {
		t.Fatal("stale timeout closed the newer quit confirmation")
	}

	updated, cmd = m.Update(quitConfirmationExpiredMsg{id: secondID})
	m = updated.(model)
	if cmd != nil || m.quitConfirmOpen || m.quitConfirmID != uuid.Nil {
		t.Fatal("current timeout did not close the quit confirmation")
	}
	if cancelled || m.focus != originalFocus {
		t.Fatal("quit confirmation timeout affected underlying application state")
	}
}

func TestCtrlHJKLMovesBetweenAdjacentPanes(t *testing.T) {
	tests := []struct {
		name  string
		start pane
		key   rune
		want  pane
	}{
		{name: "left from URL", start: paneURL, key: 'h', want: paneHistory},
		{name: "left from request", start: paneRequest, key: 'h', want: paneHistory},
		{name: "left from response", start: paneResponse, key: 'h', want: paneHistory},
		{name: "left edge", start: paneHistory, key: 'h', want: paneHistory},
		{name: "down from URL", start: paneURL, key: 'j', want: paneRequest},
		{name: "down from request", start: paneRequest, key: 'j', want: paneResponse},
		{name: "down edge from response", start: paneResponse, key: 'j', want: paneResponse},
		{name: "down edge from history", start: paneHistory, key: 'j', want: paneHistory},
		{name: "up edge from URL", start: paneURL, key: 'k', want: paneURL},
		{name: "up from request", start: paneRequest, key: 'k', want: paneURL},
		{name: "up from response", start: paneResponse, key: 'k', want: paneRequest},
		{name: "up edge from history", start: paneHistory, key: 'k', want: paneHistory},
		{name: "right edge from URL", start: paneURL, key: 'l', want: paneURL},
		{name: "right edge from request", start: paneRequest, key: 'l', want: paneRequest},
		{name: "right edge from response", start: paneResponse, key: 'l', want: paneResponse},
		{name: "right from history", start: paneHistory, key: 'l', want: paneRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel()
			m.setFocus(tt.start)

			updated, _ := m.Update(tea.KeyPressMsg{Code: tt.key, Mod: tea.ModCtrl})
			m = updated.(model)

			if m.focus != tt.want {
				t.Fatalf("focus = %d, want %d", m.focus, tt.want)
			}
			if got := m.urlInput.Focused(); got != (tt.want == paneURL) {
				t.Fatalf("URL focus = %v, want %v", got, tt.want == paneURL)
			}
			if m.inputMode != modeNormal {
				t.Fatalf("input mode = %d, want normal", m.inputMode)
			}
		})
	}
}

func TestDirectionalPaneNavigationDoesNotInterceptRequestInsertMode(t *testing.T) {
	m := NewModel()
	m.requestTab = requestTabBody
	m.bodyInput.SetValue("ab")
	m.bodyInput.CursorEnd()
	m.setFocus(paneRequest)
	m.inputMode = modeInsert
	_ = m.focusBodyInput()

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	m = updated.(model)

	if m.focus != paneRequest || m.inputMode != modeInsert {
		t.Fatalf("insert-mode navigation changed focus or mode: focus=%d mode=%d", m.focus, m.inputMode)
	}
	if got := m.bodyInput.Value(); got != "a" {
		t.Fatalf("Ctrl-H did not reach the active editor: body = %q, want %q", got, "a")
	}
}

func TestTabPaneNavigationStillCycles(t *testing.T) {
	m := NewModel()
	want := []pane{paneRequest, paneResponse, paneHistory, paneURL}
	for _, wantFocus := range want {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m = updated.(model)
		if m.focus != wantFocus {
			t.Fatalf("Tab focus = %d, want %d", m.focus, wantFocus)
		}
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(model)
	if m.focus != paneHistory {
		t.Fatalf("Shift-Tab focus = %d, want history", m.focus)
	}
}

func TestQuitConfirmationModalHasUniformBackground(t *testing.T) {
	modal := quitConfirmationModal(120)
	width, height := lipgloss.Size(modal)
	canvas := lipgloss.NewCanvas(width, height)
	canvas.Compose(lipgloss.NewLayer(modal))

	for y := 1; y < height-1; y++ {
		for x := 1; x < width-1; x++ {
			if cell := canvas.CellAt(x, y); cell == nil || cell.Style.Bg == nil {
				t.Fatalf("modal background missing at cell (%d, %d)", x, y)
			}
		}
	}
}

func TestEnterSendsAndSnapshotsRequest(t *testing.T) {
	m := NewModel()
	m.urlInput.SetValue("https://example.test/items")
	m.bodyInput.SetValue(`{"name":"courier"}`)
	m.headersInput.SetEntries([]headerEntry{
		{key: "X-Trace", value: "one"},
		{key: "X-Trace", value: "two"},
	})
	m.paramsInput.SetEntries([]headerEntry{{key: "page", value: "2"}})
	m.authInput.SetConfig(authConfig{typeID: authBearer, bearerToken: "snapshot-token"})
	m.cookiesInput.SetEntries([]headerEntry{{key: "session", value: "snapshot-cookie"}})
	m.testsInput.SetEntries([]headerEntry{{key: "status", value: "201"}})

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
	if item.requestBodyConfig.mode != bodyRaw || item.requestBodyConfig.rawType != rawJSON || item.requestBodyConfig.raw != `{"name":"courier"}` {
		t.Fatalf("body configuration snapshot = %#v", item.requestBodyConfig)
	}
	if len(item.requestHeaders) != 2 || item.requestHeaders[1].value != "two" {
		t.Fatalf("header snapshot = %#v", item.requestHeaders)
	}
	if len(item.requestParams) != 1 || item.requestParams[0].value != "2" {
		t.Fatalf("parameter snapshot = %#v", item.requestParams)
	}
	if item.requestAuth.typeID != authBearer || item.requestAuth.bearerToken != "snapshot-token" {
		t.Fatalf("auth snapshot = %#v", item.requestAuth)
	}
	if len(item.requestCookies) != 1 || item.requestCookies[0].value != "snapshot-cookie" {
		t.Fatalf("cookie snapshot = %#v", item.requestCookies)
	}
	if len(item.requestTests) != 1 || item.requestTests[0].key != "status" || item.requestTests[0].value != "201" {
		t.Fatalf("test snapshot = %#v", item.requestTests)
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

func TestLoadingSavedRequestInvalidatesInFlightHTTPResponse(t *testing.T) {
	activeID := uuid.New()
	requestContext, cancel := context.WithCancel(context.Background())
	m := NewModel()
	m.requestId = activeID
	m.requestContext = requestContext
	m.cancelRequest = cancel
	m.history = []historyItem{{requestID: activeID, url: "https://example.test/slow"}}

	m.applySavedRequest(savedRequest{method: "GET", url: "https://example.test/next"})
	if requestContext.Err() != context.Canceled {
		t.Fatal("loading a saved request did not cancel the in-flight request")
	}
	if m.requestId == activeID || m.cancelRequest != nil || m.requestContext != nil {
		t.Fatalf("active request state was not invalidated: id=%s cancel=%v context=%v", m.requestId, m.cancelRequest != nil, m.requestContext != nil)
	}

	updated, _ := m.Update(responseMsg{requestID: activeID, responseBody: "late response", responseMeta: "200 OK"})
	m = updated.(model)
	if m.response != "" {
		t.Fatalf("late response replaced the selected request display: %q", m.response)
	}
	if m.history[0].responseBody != "late response" {
		t.Fatal("late response was not retained in its original history entry")
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

func TestResponseHorizontalScrollingUsesUppercaseHL(t *testing.T) {
	m := NewModel()
	m.focus = paneResponse
	m.responseModel.SetWidth(20)
	m.responseModel.SetHeight(2)
	m.responseModel.SetContent(strings.Repeat("0123456789", 8))

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'L', Text: "L", Mod: tea.ModShift})
	m = updated.(model)
	if m.responseModel.XOffset() == 0 {
		t.Fatal("L did not scroll the response horizontally")
	}
	if m.responseTab != responseTabBody {
		t.Fatalf("horizontal scrolling changed tab to %d", m.responseTab)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'H', Text: "H", Mod: tea.ModShift})
	m = updated.(model)
	if m.responseModel.XOffset() != 0 {
		t.Fatalf("H did not scroll back to the left: offset=%d", m.responseModel.XOffset())
	}

	m.responseTab = responseTabHeaders
	m.responseHeadersModel.SetWidth(20)
	m.responseHeadersModel.SetHeight(2)
	m.responseHeadersModel.SetContent("X-Long: " + strings.Repeat("value", 20))
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'L', Text: "L", Mod: tea.ModShift})
	m = updated.(model)
	if m.responseHeadersModel.XOffset() == 0 || m.responseModel.XOffset() != 0 {
		t.Fatalf("header horizontal scroll = header %d body %d", m.responseHeadersModel.XOffset(), m.responseModel.XOffset())
	}
}

func TestCustomMethodEditorAndBuiltInCycle(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'O', Text: "O", Mod: tea.ModShift})
	m = updated.(model)
	if !m.methodEditOpen {
		t.Fatal("O did not open the custom method editor")
	}
	m.methodInput.SetValue("REPORT-V2")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if m.methodEditOpen || m.displayedMethod() != "REPORT-V2" {
		t.Fatalf("custom method editor result = open %v method %q", m.methodEditOpen, m.displayedMethod())
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	m = updated.(model)
	if m.customMethod != "" || m.displayedMethod() != "POST" {
		t.Fatalf("built-in method cycle after custom method = %q", m.displayedMethod())
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
	var sizes []tea.WindowSizeMsg
	for _, width := range []int{80, 120, 160, 320} {
		for height := 16; height <= 80; height++ {
			sizes = append(sizes, tea.WindowSizeMsg{Width: width, Height: height})
		}
	}
	for _, size := range sizes {
		m := NewModel()
		updated, _ := m.Update(size)
		m = updated.(model)

		mainWidth, contentHeight, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)
		history := m.viewHistory(contentHeight)
		for tab := requestTabBody; tab < requestTabCount; tab++ {
			m.requestTab = tab
			m.setFocus(paneRequest)
			rightColumn := lipgloss.JoinVertical(
				lipgloss.Left,
				m.viewURL(mainWidth),
				m.viewRequest(mainWidth, bodyHeight),
				m.viewResponse(mainWidth, responseHeight),
			)

			if got, want := lipgloss.Height(rightColumn), lipgloss.Height(history); got != want {
				t.Fatalf("column bottoms do not align at %dx%d on tab %d: main=%d history=%d", size.Width, size.Height, tab, got, want)
			}
			layout := lipgloss.JoinHorizontal(lipgloss.Top, history, rightColumn)
			if got := lipgloss.Width(layout); got >= size.Width {
				t.Fatalf("layout reaches terminal wrap column at %dx%d on tab %d: layout=%d", size.Width, size.Height, tab, got)
			}
			rightLines := strings.Split(stripANSI(rightColumn), "\n")
			historyLines := strings.Split(stripANSI(history), "\n")
			if !strings.Contains(rightLines[len(rightLines)-1], "╯") || !strings.Contains(historyLines[len(historyLines)-1], "╯") {
				t.Fatalf("bottom border is not on final row at %dx%d on tab %d", size.Width, size.Height, tab)
			}

			renderedLines := strings.Split(stripANSI(m.View().Content), "\n")
			alignedBottom := false
			for _, line := range renderedLines {
				if strings.HasPrefix(line, "╰") && strings.Count(line, "╰") >= 2 {
					alignedBottom = true
					break
				}
			}
			if !alignedBottom {
				t.Fatalf("rendered pane bottoms do not align at %dx%d on tab %d:\n%s", size.Width, size.Height, tab, stripANSI(m.View().Content))
			}
		}
	}
}

func TestGraphQLBodyEditorNavigationKeepsRequestHeight(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	m.bodyMode = bodyGraphQL
	m.requestTab = requestTabBody
	m.setFocus(paneRequest)
	mainWidth, _, bodyHeight, _ := layoutDimensions(m.width, m.height)
	wantHeight := lipgloss.Height(m.viewRequest(mainWidth, bodyHeight))
	if !strings.Contains(stripANSI(m.viewRequest(mainWidth, bodyHeight)), "Operation:") || !strings.Contains(stripANSI(m.viewRequest(mainWidth, bodyHeight)), "Variables (JSON)") {
		t.Fatalf("GraphQL fields are not visible:\n%s", stripANSI(m.viewRequest(mainWidth, bodyHeight)))
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = updated.(model)
	if m.graphqlField != 1 {
		t.Fatalf("GraphQL field after j = %d", m.graphqlField)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m = updated.(model)
	if m.inputMode != modeInsert || !m.graphqlVariablesInput.Focused() {
		t.Fatal("GraphQL variables editor did not enter insert mode")
	}
	if got := lipgloss.Height(m.viewRequest(mainWidth, bodyHeight)); got != wantHeight {
		t.Fatalf("GraphQL insert mode changed request height: got %d want %d", got, wantHeight)
	}
}

func TestSettingsScreenKeepsLayoutAndUpdatesTransportOptions(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = updated.(model)

	if !m.settingsOpen || m.focus != paneRequest {
		t.Fatalf("settings did not open in request pane: open=%v focus=%d", m.settingsOpen, m.focus)
	}
	mainWidth, contentHeight, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)
	requestView := stripANSI(m.viewRequest(mainWidth, bodyHeight))
	if !strings.Contains(requestView, "Settings") || !strings.Contains(requestView, "Follow redirects") {
		t.Fatalf("settings view missing controls:\n%s", requestView)
	}
	rightColumn := lipgloss.JoinVertical(lipgloss.Left, m.viewURL(mainWidth), m.viewRequest(mainWidth, bodyHeight), m.viewResponse(mainWidth, responseHeight))
	if got, want := lipgloss.Height(rightColumn), lipgloss.Height(m.viewHistory(contentHeight)); got != want {
		t.Fatalf("settings changed column height: main=%d history=%d", got, want)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = updated.(model)
	if m.settings.config.followRedirects {
		t.Fatal("space did not toggle redirect setting")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(model)
	before := m.settings.config.timeout
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(model)
	if m.settings.config.timeout != before+time.Second {
		t.Fatalf("timeout = %s, want %s", m.settings.config.timeout, before+time.Second)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	m = updated.(model)
	tlsView := stripANSI(m.viewRequest(mainWidth, bodyHeight))
	if !strings.Contains(tlsView, "TLS & certificates") || !strings.Contains(tlsView, "CA bundle") || !strings.Contains(tlsView, "Client cert") {
		t.Fatalf("TLS settings view missing controls:\n%s", tlsView)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = updated.(model)
	if !m.settings.config.skipTLSVerify {
		t.Fatal("space did not toggle TLS verification setting")
	}
}

func TestEnvironmentScreenKeepsLayout(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = updated.(model)

	if !m.environmentOpen || m.settingsOpen || m.focus != paneRequest {
		t.Fatalf("environment did not open correctly: environment=%v settings=%v focus=%d", m.environmentOpen, m.settingsOpen, m.focus)
	}
	mainWidth, contentHeight, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)
	requestView := stripANSI(m.viewRequest(mainWidth, bodyHeight))
	if !strings.Contains(requestView, "Environment") || !strings.Contains(requestView, "Variable") {
		t.Fatalf("environment view missing controls:\n%s", requestView)
	}
	rightColumn := lipgloss.JoinVertical(lipgloss.Left, m.viewURL(mainWidth), m.viewRequest(mainWidth, bodyHeight), m.viewResponse(mainWidth, responseHeight))
	if got, want := lipgloss.Height(rightColumn), lipgloss.Height(m.viewHistory(contentHeight)); got != want {
		t.Fatalf("environment changed column height: main=%d history=%d", got, want)
	}
}

func TestEnvironmentProfilesCanBeCreatedRenamedSwitchedAndDeleted(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(model)
	m.environmentNameInput.SetValue("Staging")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if len(m.environments) != 2 || m.activeEnvironmentName() != "Staging" {
		t.Fatalf("created environments = %#v", m.environments)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = updated.(model)
	m.environmentNameInput.SetValue("QA")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if m.activeEnvironmentName() != "QA" {
		t.Fatalf("renamed environment = %q", m.activeEnvironmentName())
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	m = updated.(model)
	if m.activeEnvironmentName() != defaultEnvironmentName {
		t.Fatalf("cycled environment = %q", m.activeEnvironmentName())
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(model)
	if len(m.environments) != 1 || m.activeEnvironmentName() != "QA" {
		t.Fatalf("environments after delete = %#v", m.environments)
	}
}

func TestAWSSignatureV4FieldsFitRequestPane(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	m.requestTab = requestTabAuth
	m.authInput.SetConfig(authConfig{typeID: authAWSSignatureV4})
	m.setFocus(paneRequest)
	mainWidth, _, bodyHeight, _ := layoutDimensions(m.width, m.height)
	view := stripANSI(m.viewRequest(mainWidth, bodyHeight))
	for _, field := range []string{"Access ID", "Secret", "Region", "Service", "Session"} {
		if !strings.Contains(view, field) {
			t.Fatalf("AWS auth field %q is clipped:\n%s", field, view)
		}
	}
}

func TestParamsTableFitsInsideRequestPane(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	m.requestTab = requestTabParams
	m.setFocus(paneRequest)

	mainWidth, _, bodyHeight, _ := layoutDimensions(m.width, m.height)
	lines := strings.Split(stripANSI(m.viewRequest(mainWidth, bodyHeight)), "\n")
	if len(lines) < 3 || !strings.Contains(lines[2], "┐") {
		t.Fatalf("parameter table right edge is clipped:\n%s", strings.Join(lines, "\n"))
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
			requestParams:  []headerEntry{{key: "item", value: string(rune('0' + i))}},
			requestAuth:    authConfig{typeID: authBearer, bearerToken: "token-" + string(rune('0'+i))},
			requestCookies: []headerEntry{{key: "cookie-item", value: string(rune('0' + i))}},
			requestBodyConfig: bodyConfig{
				mode: bodyFormURLEncoded,
				form: []headerEntry{{key: "body-item", value: string(rune('0' + i))}},
			},
		})
	}
	m.historyPos = 3
	m.handleHistoryKeys("down")
	if m.urlInput.Value() != "" {
		t.Fatalf("history navigation replaced the draft URL with %q", m.urlInput.Value())
	}
	m.handleHistoryKeys("enter")

	if m.urlInput.Value() != "item-e" {
		t.Fatalf("restored URL = %q", m.urlInput.Value())
	}
	entries := m.headersInput.Entries()
	if len(entries) != 1 || entries[0].value != "4" {
		t.Fatalf("restored headers = %#v", entries)
	}
	params := m.paramsInput.Entries()
	if len(params) != 1 || params[0].value != "4" {
		t.Fatalf("restored params = %#v", params)
	}
	auth := m.authInput.Config()
	if auth.typeID != authBearer || auth.bearerToken != "token-4" {
		t.Fatalf("restored auth = %#v", auth)
	}
	cookies := m.cookiesInput.Entries()
	if len(cookies) != 1 || cookies[0].value != "4" {
		t.Fatalf("restored cookies = %#v", cookies)
	}
	if m.bodyMode != bodyFormURLEncoded {
		t.Fatalf("restored body mode = %d, want form", m.bodyMode)
	}
	form := m.formInput.Entries()
	if len(form) != 1 || form[0].value != "4" {
		t.Fatalf("restored form body = %#v", form)
	}

	view := stripANSI(m.viewHistory(6)) // two visible history rows
	if !strings.Contains(view, "item-e") {
		t.Fatalf("selected history item is off-screen:\n%s", view)
	}
}

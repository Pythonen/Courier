package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestSaveLoadRenameAndDeleteResponseExample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.savedRequests = []savedRequest{{name: "Users / Get", method: "GET", url: "https://example.test/users/1", body: bodyConfig{mode: bodyNone}}}
	m.activeSavedIndex = 0
	m.responseStatusCode = 200
	m.response = `{"id":1}`
	m.responseRaw = `{"id":1}`
	m.responseRawAvailable = true
	m.responseHeaders = "Content-Type: application/json\nX-Request-Id: req-1"
	m.responseMeta = "200 OK • 12ms • 8 B"

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	m = updated.(model)
	m.responseMeta = "200 OK • 10ms • 8 B"
	if err := m.saveCurrentResponseExample(); err != nil {
		t.Fatal(err)
	}
	if got := m.savedRequests[0].examples; len(got) != 2 || got[0].name != "200 OK" || got[1].name != "200 OK 2" {
		t.Fatalf("saved examples = %#v", got)
	}
	m.urlInput.SetValue("https://example.test/users/2")
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	m = updated.(model)
	if len(m.savedRequests[0].examples) != 2 {
		t.Fatalf("request update discarded examples: %#v", m.savedRequests[0])
	}

	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if len(loaded.savedRequests) != 1 || len(loaded.savedRequests[0].examples) != 2 || loaded.savedRequests[0].examples[0].responseRaw != `{"id":1}` {
		t.Fatalf("loaded examples = %#v", loaded.savedRequests)
	}
	loaded.sidebarMode = sidebarExamples
	loaded.examplePos = 0
	loaded.handleHistoryKeys("enter")
	if loaded.responseStatusCode != 200 || loaded.responseRaw != `{"id":1}` || loaded.activeSavedIndex != 0 {
		t.Fatalf("loaded example response = status %d raw %q active %d", loaded.responseStatusCode, loaded.responseRaw, loaded.activeSavedIndex)
	}
	loaded.handleHistoryKeys("r")
	loaded.collectionRenameInput.SetValue("Happy path")
	updated, _ = loaded.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	loaded = updated.(model)
	if loaded.savedRequests[0].examples[0].name != "Happy path" {
		t.Fatalf("renamed example = %#v", loaded.savedRequests[0].examples)
	}
	loaded.handleHistoryKeys("d")
	loaded.handleHistoryKeys("d")
	if len(loaded.savedRequests[0].examples) != 1 {
		t.Fatalf("examples after deletion = %#v", loaded.savedRequests[0].examples)
	}
}

func TestExamplesSidebarKeepsMainColumnAlignment(t *testing.T) {
	initTestZones()
	m := NewModel()
	m.savedRequests = []savedRequest{{
		name: "Users / Get", method: "GET", url: "https://example.test/users/1", body: bodyConfig{mode: bodyNone},
		examples: []savedExample{{name: "Success", statusCode: 200, responseBody: "ok", responseRaw: "ok", responseRawAvailable: true}},
	}}
	m.sidebarMode = sidebarExamples
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(model)
	mainWidth, contentHeight, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)
	history := m.viewHistory(contentHeight)
	if !strings.Contains(stripANSI(history), "Examples") || !strings.Contains(stripANSI(history), "Users / Get") {
		t.Fatalf("examples sidebar = %q", stripANSI(history))
	}
	rightColumn := lipgloss.JoinVertical(lipgloss.Left, m.viewURL(mainWidth), m.viewRequest(mainWidth, bodyHeight), m.viewResponse(mainWidth, responseHeight))
	if got, want := lipgloss.Height(rightColumn), lipgloss.Height(history); got != want {
		t.Fatalf("example sidebar alignment: main=%d sidebar=%d", got, want)
	}
}

func TestSaveExampleRequiresSavedRequestAndResponse(t *testing.T) {
	m := NewModel()
	if err := m.saveCurrentResponseExample(); err == nil || !strings.Contains(err.Error(), "collection request") {
		t.Fatalf("unsaved request error = %v", err)
	}
	m.savedRequests = []savedRequest{{name: "Request", method: "GET", url: "https://example.test"}}
	m.activeSavedIndex = 0
	if err := m.saveCurrentResponseExample(); err == nil || !strings.Contains(err.Error(), "send the request") {
		t.Fatalf("empty response error = %v", err)
	}
}

func TestResponseHeaderEntryRoundTrip(t *testing.T) {
	formatted := "Content-Type: application/json\nSet-Cookie: a=1\nSet-Cookie: b=2"
	if got := formattedResponseHeaders(responseHeaderEntries(formatted)); got != formatted {
		t.Fatalf("formatted headers = %q", got)
	}
}

func TestResponseHeaderControlsRoundTripThroughExamplesAndExports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	value := "before\tmiddle\u202eafter"
	rawHeaders := "X-Test: " + value + "\n"

	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.savedRequests = []savedRequest{{name: "Headers", method: "GET", url: "https://example.test/headers", body: bodyConfig{mode: bodyNone}}}
	m.activeSavedIndex = 0
	m.responseStatusCode = 200
	m.response = "ok"
	m.responseRaw = "ok"
	m.responseRawAvailable = true
	m.responseMeta = "200 OK"
	m.setResponseHeaders(rawHeaders)
	if err := m.saveCurrentResponseExample(); err != nil {
		t.Fatal(err)
	}
	if got := m.savedRequests[0].examples[0].responseHeaders; got != rawHeaders {
		t.Fatalf("saved example headers = %q, want %q", got, rawHeaders)
	}

	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if got := loaded.savedRequests[0].examples[0].responseHeaders; got != rawHeaders {
		t.Fatalf("persisted example headers = %q, want %q", got, rawHeaders)
	}
	loaded.applySavedExample(savedExampleRef{requestIndex: 0, exampleIndex: 0})
	if loaded.responseHeaders != rawHeaders {
		t.Fatalf("loaded example changed raw headers: %q", loaded.responseHeaders)
	}
	display := loaded.responseHeadersModel.GetContent()
	if strings.Contains(display, "\t") || strings.Contains(display, "\u202e") || !strings.Contains(display, `\t`) || !strings.Contains(display, `\u202e`) {
		t.Fatalf("loaded example header viewport is unsafe: %q", display)
	}

	entries := responseHeaderEntries(rawHeaders)
	if len(entries) != 1 || entries[0].value != value {
		t.Fatalf("Postman header entries = %#v", entries)
	}
	postmanItems := exportPostmanNodes([]*postmanExportNode{{name: "Headers", request: &loaded.savedRequests[0]}})
	if len(postmanItems) != 1 || len(postmanItems[0].Response) != 1 || len(postmanItems[0].Response[0].Header) != 1 || postmanItems[0].Response[0].Header[0]["value"] != value {
		t.Fatalf("Postman exported response headers = %#v", postmanItems)
	}
	harHeaders := parseHARHeaderText(rawHeaders)
	if len(harHeaders) != 1 || harHeaders[0].Value != value {
		t.Fatalf("HAR header entries = %#v", harHeaders)
	}
	harEntry, err := exportHAREntry(loaded.savedRequests[0], loaded.savedRequests[0].examples[0])
	if err != nil || len(harEntry.Response.Headers) != 1 || harEntry.Response.Headers[0].Value != value {
		t.Fatalf("HAR exported response headers = %#v, err = %v", harEntry.Response.Headers, err)
	}
	mockHeaders, err := parseMockResponseHeaders(rawHeaders)
	if err != nil || mockHeaders.Get("X-Test") != value {
		t.Fatalf("mock headers = %#v, err = %v", mockHeaders, err)
	}
}

func TestFailedExampleSavePreservesResponseAndRollsBackAppend(t *testing.T) {
	regularFile := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(regularFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.workspacePath = filepath.Join(regularFile, "workspace.json")
	m.savedRequests = []savedRequest{{name: "Request", method: "GET", url: "https://example.test"}}
	m.activeSavedIndex = 0
	m.response = "original response"
	m.responseMeta = "200 OK"
	m.responseStatusCode = 200
	m.responseModel.SetContent(m.response)

	err := m.saveCurrentResponseExample()
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("save example error = %v", err)
	}
	if len(m.savedRequests[0].examples) != 0 {
		t.Fatalf("failed save retained appended example: %#v", m.savedRequests[0].examples)
	}
	if m.response != "original response" || m.responseModel.GetContent() != "original response" || m.responseMeta != "200 OK" {
		t.Fatalf("failed save clobbered response: body=%q viewport=%q metadata=%q", m.response, m.responseModel.GetContent(), m.responseMeta)
	}
	if !strings.Contains(m.workspaceSaveStatus, "Workspace save failed") {
		t.Fatalf("failed save status = %q", m.workspaceSaveStatus)
	}
}

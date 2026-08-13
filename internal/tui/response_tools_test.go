package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestResponseSearchNavigatesMatches(t *testing.T) {
	m := NewModel()
	m.response = "zero\nneedle one\ntwo\nthree\nneedle four\nfive\nneedle six"
	m.responseModel.SetContent(m.response)
	m.responseModel.SetWidth(40)
	m.responseModel.SetHeight(2)
	m.responseSearchInput.SetValue("needle")
	m.executeResponseSearch()

	if len(m.responseSearchMatches) != 3 || m.responseSearchPos != 0 || m.responseModel.YOffset() != 0 {
		t.Fatalf("initial search = matches %#v pos %d offset %d", m.responseSearchMatches, m.responseSearchPos, m.responseModel.YOffset())
	}
	m.navigateResponseMatch(1)
	if m.responseSearchPos != 1 || m.responseModel.YOffset() != 3 {
		t.Fatalf("next match = pos %d offset %d", m.responseSearchPos, m.responseModel.YOffset())
	}
	m.navigateResponseMatch(-1)
	if m.responseSearchPos != 0 || !strings.Contains(m.responseSearchStatus, "1/3") {
		t.Fatalf("previous match = pos %d status %q", m.responseSearchPos, m.responseSearchStatus)
	}
}

func TestResponseSearchUsesActiveHeadersTab(t *testing.T) {
	m := NewModel()
	m.response = "body only"
	m.responseHeaders = "Content-Type: text/plain\nX-Trace: match"
	m.responseHeadersModel.SetContent(m.responseHeaders)
	m.responseHeadersModel.SetHeight(1)
	m.responseTab = responseTabHeaders
	m.responseSearchInput.SetValue("match")
	m.executeResponseSearch()
	if len(m.responseSearchMatches) != 1 || m.responseHeadersModel.YOffset() != 1 {
		t.Fatalf("header search = matches %#v offset %d", m.responseSearchMatches, m.responseHeadersModel.YOffset())
	}
}

func TestResponseSearchKeyboardFlow(t *testing.T) {
	m := NewModel()
	m.focus = paneResponse
	m.response = "first\nfind me\nlast"
	m.responseModel.SetContent(m.response)
	m.responseModel.SetHeight(1)
	updated, _ := m.Update(tea.KeyPressMsg{Code: '/'})
	m = updated.(model)
	if !m.responseSearchOpen || !m.responseSearchInput.Focused() {
		t.Fatal("slash did not open the response search")
	}
	m.responseSearchInput.SetValue("find")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if m.responseSearchOpen || len(m.responseSearchMatches) != 1 || m.responseModel.YOffset() != 1 {
		t.Fatalf("submitted search = open %v matches %#v offset %d", m.responseSearchOpen, m.responseSearchMatches, m.responseModel.YOffset())
	}
}

func TestFilterResponseBodySupportsJSONPathPredicatesAndRecursion(t *testing.T) {
	body := []byte(`{"items":[{"name":"Ada","active":true},{"name":"Linus","active":false}],"nested":{"name":"Grace"}}`)
	filtered, contentType, count, err := filterResponseBody(body, "application/json", `$.items[?(@.active==true)].name`)
	if err != nil {
		t.Fatal(err)
	}
	if filtered != `"Ada"` || contentType != "application/json" || count != 1 {
		t.Fatalf("JSONPath predicate = %q %q %d", filtered, contentType, count)
	}
	filtered, _, count, err = filterResponseBody(body, "application/json", `$..name`)
	if err != nil || count != 3 || !strings.Contains(filtered, `"Grace"`) {
		t.Fatalf("recursive JSONPath = %q count %d err %v", filtered, count, err)
	}
}

func TestFilterResponseBodySupportsXMLAndHTMLXPath(t *testing.T) {
	xml := []byte(`<root><item active="true"><name>Ada</name></item><item><name>Linus</name></item></root>`)
	filtered, contentType, count, err := filterResponseBody(xml, "application/xml", `//item[@active='true']/name`)
	if err != nil || count != 1 || contentType != "application/xml" || !strings.Contains(filtered, "<name>Ada</name>") {
		t.Fatalf("XML XPath = %q %q count %d err %v", filtered, contentType, count, err)
	}
	html := []byte(`<html><body><ul><li>Ada</li><li class="selected">Grace</li></ul></body></html>`)
	filtered, contentType, count, err = filterResponseBody(html, "text/html", `//li[@class='selected']`)
	if err != nil || count != 1 || contentType != "text/html" || !strings.Contains(filtered, "Grace") {
		t.Fatalf("HTML XPath = %q %q count %d err %v", filtered, contentType, count, err)
	}
}

func TestResponseFilterIsDisplayOnlySearchableAndClearable(t *testing.T) {
	m := NewModel()
	m.responseRaw = `{"items":[{"name":"Ada"},{"name":"Grace"}]}`
	m.responseRawAvailable = true
	m.response = formatResponseBody([]byte(m.responseRaw), "application/json")
	m.responseHeaders = "Content-Type: application/json"
	m.responseModel.SetContent(m.response)
	m.responseModel.SetWidth(60)
	m.responseModel.SetHeight(10)
	m.responseFilterInput.SetValue(`$.items[1]`)
	m.executeResponseFilter()
	if !m.responseFilterActive || !strings.Contains(m.responseFilterContent, "Grace") || strings.Contains(m.responseFilterContent, "Ada") {
		t.Fatalf("active response filter = active %v content %q status %q", m.responseFilterActive, m.responseFilterContent, m.responseFilterStatus)
	}
	if m.activeResponseExportContent() != m.responseRaw {
		t.Fatalf("filter changed exported response = %q", m.activeResponseExportContent())
	}
	m.responseSearchInput.SetValue("Grace")
	m.executeResponseSearch()
	if len(m.responseSearchMatches) != 1 {
		t.Fatalf("search within filter = %#v", m.responseSearchMatches)
	}
	m.clearResponseFilter()
	if m.responseFilterActive || !strings.Contains(ansi.Strip(m.responseModel.View()), "Ada") {
		t.Fatalf("cleared response filter = active %v view %q", m.responseFilterActive, ansi.Strip(m.responseModel.View()))
	}
}

func TestResponseFilterReportsInvalidAndEmptyResults(t *testing.T) {
	if _, _, _, err := filterResponseBody([]byte(`{"ok":true}`), "application/json", `$[?(`); err == nil || !strings.Contains(err.Error(), "invalid JSONPath") {
		t.Fatalf("invalid JSONPath error = %v", err)
	}
	if _, _, _, err := filterResponseBody([]byte(`<root/>`), "application/xml", `//missing`); err == nil || !strings.Contains(err.Error(), "no results") {
		t.Fatalf("empty XPath error = %v", err)
	}
}

func TestResponseFilterKeyboardFlow(t *testing.T) {
	m := NewModel()
	m.focus = paneResponse
	m.responseRaw = `{"items":[1,2,3]}`
	m.responseRawAvailable = true
	m.response = formatResponseBody([]byte(m.responseRaw), "application/json")
	m.responseModel.SetContent(m.response)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = updated.(model)
	if !m.responseFilterOpen || !m.responseFilterInput.Focused() || m.responseTab != responseTabBody {
		t.Fatalf("filter shortcut = open %v focused %v tab %d", m.responseFilterOpen, m.responseFilterInput.Focused(), m.responseTab)
	}
	m.responseFilterInput.SetValue(`$.items[1:]`)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if !m.responseFilterActive || !strings.Contains(m.responseFilterContent, "3") {
		t.Fatalf("submitted filter = active %v content %q status %q", m.responseFilterActive, m.responseFilterContent, m.responseFilterStatus)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'F', Text: "F", Mod: tea.ModShift})
	m = updated.(model)
	if m.responseFilterActive {
		t.Fatal("F did not clear the response filter")
	}
}

func TestSaveActiveResponseCreatesPlainOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "response.json")
	m := NewModel()
	m.response = "\x1b[31m{\"ok\": true}\x1b[0m"
	written, err := m.saveActiveResponse(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"ok": true}` || written != len(data) {
		t.Fatalf("saved response = %q (%d bytes)", data, written)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("response mode = %o", info.Mode().Perm())
	}
	if _, err := m.saveActiveResponse(path); err == nil {
		t.Fatal("existing response export was overwritten")
	}
}

func TestSaveActiveResponseUsesOriginalPayloadNotFormattedDisplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "response.html")
	m := NewModel()
	m.response = "<html>\n  <body>pretty display</body>\n</html>"
	m.responseRaw = "<html><body>original</body></html>"
	m.responseRawAvailable = true
	if _, err := m.saveActiveResponse(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != m.responseRaw {
		t.Fatalf("saved payload = %q, want original %q", data, m.responseRaw)
	}
}

func TestSaveActiveResponseUsesSelectedTabAndVariables(t *testing.T) {
	dir := t.TempDir()
	m := NewModel()
	m.variablesInput.SetEntries([]headerEntry{{key: "outputDir", value: dir}})
	m.response = "body"
	m.responseHeaders = "X-Test: yes\n"
	m.responseTab = responseTabHeaders
	if _, err := m.saveActiveResponse("{{outputDir}}/headers.txt"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "headers.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != m.responseHeaders {
		t.Fatalf("saved headers = %q", data)
	}
}

func TestResponseExportSurvivesWorkspaceSaveFailureOnFocusChange(t *testing.T) {
	initTestZones()
	dir := t.TempDir()
	regularFile := filepath.Join(dir, "regular-file")
	if err := os.WriteFile(regularFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.workspacePath = filepath.Join(regularFile, "workspace.json")
	m.settingsOpen = true
	m.response = "original non-raw response"
	m.responseMeta = "200 OK"
	m.responseModel.SetContent(m.response)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = updated.(model)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(model)
	mainWidth, _, _, responseHeight := layoutDimensions(m.width, m.height)
	m.responseSaveInput.SetValue(strings.Repeat("very/long/export/path/", 12))
	m.responseSaveInput.CursorEnd()
	if !m.responseSaveOpen || m.response != "original non-raw response" || m.responseModel.GetContent() != "original non-raw response" {
		t.Fatalf("response export prompt lost content: open=%v response=%q viewport=%q", m.responseSaveOpen, m.response, m.responseModel.GetContent())
	}
	if m.responseMeta != "200 OK" || !strings.Contains(m.workspaceSaveStatus, "Workspace save failed") {
		t.Fatalf("response metadata/status = %q / %q", m.responseMeta, m.workspaceSaveStatus)
	}
	if rendered := ansi.Strip(m.viewResponse(mainWidth, responseHeight)); !strings.Contains(rendered, "Workspace save failed") {
		t.Fatalf("response export prompt hid workspace failure: %q", rendered)
	}

	path := filepath.Join(dir, "response.txt")
	m.responseSaveInput.SetValue(path)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original non-raw response" {
		t.Fatalf("exported response = %q, want original content", data)
	}
	if rendered := ansi.Strip(m.viewResponse(mainWidth, responseHeight)); !strings.Contains(rendered, "Workspace save failed") {
		t.Fatalf("response export success hid workspace failure: %q", rendered)
	}
}

func TestCurlGenerationSurvivesWorkspaceSaveFailureOnFocusChange(t *testing.T) {
	initTestZones()
	regularFile := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(regularFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.workspacePath = filepath.Join(regularFile, "workspace.json")
	m.settingsOpen = true
	m.urlInput.SetValue("https://example.com/generated")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated.(model)
	if !strings.Contains(m.response, "curl") || !strings.Contains(m.response, "https://example.com/generated") || strings.Contains(m.response, "workspace changed on disk") {
		t.Fatalf("generated cURL was replaced by workspace failure: %q", m.response)
	}
	if m.responseMeta != "cURL command generated" || !strings.Contains(m.workspaceSaveStatus, "Workspace save failed") {
		t.Fatalf("cURL metadata/status = %q / %q", m.responseMeta, m.workspaceSaveStatus)
	}
	if rendered := ansi.Strip(m.viewResponse(120, 12)); !strings.Contains(rendered, "Workspace save failed") {
		t.Fatalf("generated cURL hid workspace failure: %q", rendered)
	}
}

func TestReceivedResponseSurvivesWorkspaceSaveFailure(t *testing.T) {
	initTestZones()
	regularFile := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(regularFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.workspacePath = filepath.Join(regularFile, "workspace.json")
	m.history = []historyItem{{requestID: m.requestId}}
	updated, _ := m.Update(responseMsg{
		requestID:       m.requestId,
		responseBody:    "received response",
		responseMeta:    "200 OK",
		variableUpdates: []headerEntry{{key: "token", value: "fresh"}},
	})
	m = updated.(model)
	if m.response != "received response" || m.responseModel.GetContent() != "received response" || m.responseMeta != "200 OK" {
		t.Fatalf("workspace autosave clobbered response: body=%q viewport=%q metadata=%q", m.response, m.responseModel.GetContent(), m.responseMeta)
	}
	if len(m.history) != 1 || m.history[0].responseBody != "received response" {
		t.Fatalf("workspace autosave lost in-memory history response: %#v", m.history)
	}
	if !strings.Contains(m.workspaceSaveStatus, "Workspace save failed") {
		t.Fatalf("workspace autosave failure status = %q", m.workspaceSaveStatus)
	}
	if rendered := ansi.Strip(m.viewResponse(120, 12)); !strings.Contains(rendered, "Workspace save failed") {
		t.Fatalf("received response hid workspace failure: %q", rendered)
	}
}

func TestWorkspaceFailureStatusSanitizesControlPath(t *testing.T) {
	initTestZones()
	dir := t.TempDir()
	maliciousName := "bad\n\x1b]52;c;payload\x07\u202e"
	regularFile := filepath.Join(dir, maliciousName)
	if err := os.WriteFile(regularFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.workspacePath = filepath.Join(regularFile, "workspace.json")
	m.response = "original response"
	m.responseMeta = "200 OK"
	m.responseModel.SetContent(m.response)
	if result := m.saveWorkspaceWithStatus(); result != workspaceSaveFailed {
		t.Fatalf("workspace save result = %d, want failed", result)
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 500, Height: 24})
	m = updated.(model)
	mainWidth, _, _, responseHeight := layoutDimensions(m.width, m.height)
	rendered := m.viewResponse(mainWidth, responseHeight)
	plain := ansi.Strip(rendered)
	if strings.Contains(rendered, "\x1b]52") || strings.Contains(rendered, "\x07") || strings.Contains(plain, "bad\n") {
		t.Fatalf("workspace status contains live terminal controls: %q", plain)
	}
	for _, visible := range []string{`bad\n`, `\x1b]52;c;payload`, `\x07`, `\u202e`} {
		if !strings.Contains(plain, visible) {
			t.Fatalf("workspace status does not expose escaped %q: %q", visible, plain)
		}
	}
	if m.response != "original response" || m.responseMeta != "200 OK" || m.responseModel.GetContent() != "original response" {
		t.Fatalf("workspace failure clobbered response: body=%q meta=%q viewport=%q", m.response, m.responseMeta, m.responseModel.GetContent())
	}

	m.workspacePath = filepath.Join(dir, "workspace.json")
	if result := m.saveWorkspaceWithStatus(); !result.succeeded() || m.workspaceSaveStatus != "" {
		t.Fatalf("successful retry result/status = %d / %q", result, m.workspaceSaveStatus)
	}
}

func TestResponsePromptsKeepPaneHeight(t *testing.T) {
	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(model)
	m.response = strings.Repeat("line\n", 40)
	m.responseModel.SetContent(m.response)
	mainWidth, _, _, responseHeight := layoutDimensions(m.width, m.height)
	want := lipgloss.Height(m.viewResponse(mainWidth, responseHeight))

	m.responseSearchOpen = true
	if got := lipgloss.Height(m.viewResponse(mainWidth, responseHeight)); got != want {
		t.Fatalf("search changed response height: got %d want %d", got, want)
	}
	m.responseSearchOpen = false
	m.responseSaveOpen = true
	if got := lipgloss.Height(m.viewResponse(mainWidth, responseHeight)); got != want {
		t.Fatalf("save changed response height: got %d want %d", got, want)
	}
	m.responseSaveInput.SetValue(strings.Repeat("long/path/", 20))
	m.responseSaveInput.CursorEnd()
	m.workspaceSaveStatus = "Workspace save failed\nnext line"
	rendered := ansi.Strip(m.viewResponse(mainWidth, responseHeight))
	if got := lipgloss.Height(m.viewResponse(mainWidth, responseHeight)); got != want {
		t.Fatalf("workspace status changed response height: got %d want %d", got, want)
	}
	if !strings.Contains(rendered, `Workspace save failed\n`) || strings.Contains(rendered, "Workspace save failed\nnext line") {
		t.Fatalf("workspace status was hidden or injected a row: %q", rendered)
	}
}

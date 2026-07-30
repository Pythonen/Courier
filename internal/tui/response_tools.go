package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/antchfx/xmlquery"
	"github.com/charmbracelet/x/ansi"
	"github.com/ohler55/ojg/jp"
	"golang.org/x/net/html"
)

func (m *model) activeResponseSearchContent() string {
	if m.responseTab == responseTabHeaders {
		return m.responseHeaders
	}
	if m.responseTab == responseTabTests {
		return m.responseTests
	}
	if m.responseFilterActive {
		return m.responseFilterContent
	}
	return ansi.Strip(m.response)
}

func (m *model) executeResponseFilter() {
	expression := strings.TrimSpace(m.responseFilterInput.Value())
	if expression == "" {
		m.clearResponseFilter()
		return
	}
	raw := m.responseRaw
	if !m.responseRawAvailable {
		raw = ansi.Strip(m.response)
	}
	if strings.TrimSpace(raw) == "" {
		m.responseFilterStatus = "Filter: response body is empty"
		return
	}
	filtered, resultType, count, err := filterResponseBody([]byte(raw), responseContentType(m.responseHeaders), expression)
	if err != nil {
		m.responseFilterStatus = "Filter error: " + err.Error()
		return
	}
	m.responseFilterActive = true
	m.responseFilterContent = filtered
	m.responseFilterStatus = fmt.Sprintf("Filter %q: %d result(s) • F clears", expression, count)
	m.responseModel.SetContent(formatResponseBody([]byte(filtered), resultType))
	m.responseModel.GotoTop()
	m.resetResponseSearchMatches()
}

func (m *model) clearResponseFilter() {
	if m.responseFilterActive {
		m.responseModel.SetContent(m.response)
		m.responseModel.GotoTop()
	}
	m.responseFilterActive = false
	m.responseFilterContent = ""
	m.responseFilterStatus = ""
	m.responseFilterOpen = false
	m.responseFilterInput.Blur()
	m.resetResponseSearchMatches()
}

func filterResponseBody(body []byte, contentType, expression string) (string, string, int, error) {
	var jsonValue any
	if json.Unmarshal(body, &jsonValue) == nil {
		path, err := jp.ParseString(expression)
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid JSONPath: %w", err)
		}
		matches := path.Get(jsonValue)
		if len(matches) == 0 {
			return "", "", 0, fmt.Errorf("JSONPath returned no results")
		}
		var result any = matches
		if len(matches) == 1 {
			result = matches[0]
		}
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", "", 0, fmt.Errorf("encode JSONPath results: %w", err)
		}
		return string(encoded), "application/json", len(matches), nil
	}

	if strings.Contains(strings.ToLower(contentType), "html") {
		return filterHTMLXPath(body, expression)
	}
	document, xmlErr := xmlquery.Parse(strings.NewReader(string(body)))
	if xmlErr == nil {
		nodes, err := xmlquery.QueryAll(document, expression)
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid XPath: %w", err)
		}
		if len(nodes) == 0 {
			return "", "", 0, fmt.Errorf("XPath returned no results")
		}
		parts := make([]string, 0, len(nodes))
		for _, node := range nodes {
			parts = append(parts, node.OutputXML(true))
		}
		return strings.Join(parts, "\n"), "application/xml", len(nodes), nil
	}
	lowerBody := strings.ToLower(string(body))
	if strings.Contains(lowerBody, "<html") || strings.Contains(lowerBody, "<!doctype html") {
		return filterHTMLXPath(body, expression)
	}
	return "", "", 0, fmt.Errorf("response is not valid JSON or XML: %w", xmlErr)
}

func filterHTMLXPath(body []byte, expression string) (string, string, int, error) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", "", 0, fmt.Errorf("response is not valid JSON, XML, or HTML: %w", err)
	}
	nodes, err := htmlquery.QueryAll(document, expression)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid XPath: %w", err)
	}
	if len(nodes) == 0 {
		return "", "", 0, fmt.Errorf("XPath returned no results")
	}
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		parts = append(parts, htmlquery.OutputHTML(node, true))
	}
	return strings.Join(parts, "\n"), "text/html", len(nodes), nil
}

func responseContentType(headers string) string {
	for _, line := range strings.Split(headers, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (m *model) activeResponseExportContent() string {
	if m.responseTab == responseTabHeaders {
		return m.responseHeaders
	}
	if m.responseTab == responseTabTests {
		return m.responseTests
	}
	if m.responseRawAvailable {
		return m.responseRaw
	}
	return ansi.Strip(m.response)
}

func (m *model) executeResponseSearch() {
	query := strings.TrimSpace(m.responseSearchInput.Value())
	m.responseSearchMatches = nil
	m.responseSearchPos = 0
	if query == "" {
		m.responseSearchStatus = ""
		return
	}
	lowerQuery := strings.ToLower(query)
	for lineNumber, line := range strings.Split(m.activeResponseSearchContent(), "\n") {
		count := strings.Count(strings.ToLower(line), lowerQuery)
		for range count {
			m.responseSearchMatches = append(m.responseSearchMatches, lineNumber)
		}
	}
	if len(m.responseSearchMatches) == 0 {
		m.responseSearchStatus = fmt.Sprintf("Find %q: no matches", query)
		return
	}
	m.gotoResponseMatch()
}

func (m *model) navigateResponseMatch(delta int) {
	if len(m.responseSearchMatches) == 0 {
		m.executeResponseSearch()
		return
	}
	m.responseSearchPos = (m.responseSearchPos + delta + len(m.responseSearchMatches)) % len(m.responseSearchMatches)
	m.gotoResponseMatch()
}

func (m *model) gotoResponseMatch() {
	line := m.responseSearchMatches[m.responseSearchPos]
	viewport := &m.responseModel
	switch m.responseTab {
	case responseTabHeaders:
		viewport = &m.responseHeadersModel
	case responseTabTests:
		viewport = &m.responseTestsModel
	}
	viewport.SetYOffset(max(0, line-viewport.Height()/2))
	m.responseSearchStatus = fmt.Sprintf("Find %q: %d/%d", m.responseSearchInput.Value(), m.responseSearchPos+1, len(m.responseSearchMatches))
}

func (m *model) resetResponseSearchMatches() {
	m.responseSearchMatches = nil
	m.responseSearchPos = 0
	m.responseSearchStatus = ""
}

func (m *model) saveActiveResponse(path string) (int, error) {
	path = strings.TrimSpace(newVariableResolver(m.variablesInput.Entries()).Resolve(path))
	if path == "" {
		return 0, fmt.Errorf("response export path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return 0, fmt.Errorf("locate home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	content := m.activeResponseExportContent()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return 0, fmt.Errorf("response export target already exists: %s", path)
		}
		return 0, fmt.Errorf("create response export: %w", err)
	}
	written, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil {
		return written, fmt.Errorf("write response export: %w", writeErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("close response export: %w", closeErr)
	}
	return written, nil
}

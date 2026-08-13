package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestEvaluateAssertions(t *testing.T) {
	response := assertionResponse{
		status:   http.StatusCreated,
		headers:  http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"abc"}},
		body:     []byte(`{"user":{"name":"Ada","roles":["admin","writer"]},"active":true}`),
		duration: 45 * time.Millisecond,
		size:     72,
	}
	assertions := []headerEntry{
		{key: "status", value: "200, 201"},
		{key: "status.class", value: "2xx"},
		{key: "header.Content-Type", value: "application/json"},
		{key: "header.X-Request-ID", value: "*"},
		{key: "body.contains", value: `"active":true`},
		{key: "body.matches", value: `"name"\s*:\s*"Ada"`},
		{key: "json.user.name", value: "Ada"},
		{key: "json.user.roles[1]", value: "writer"},
		{key: "json.active", value: "true"},
		{key: "time.lt", value: "100ms"},
		{key: "size.lt", value: "1KiB"},
	}
	results := evaluateAssertions(assertions, response)
	if len(results) != len(assertions) {
		t.Fatalf("result count = %d", len(results))
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("assertion %q failed: actual=%q error=%q", result.Expression, result.Actual, result.Error)
		}
	}
}

func TestEvaluateAssertionFailuresAreDescriptive(t *testing.T) {
	response := assertionResponse{status: 404, headers: http.Header{}, body: []byte(`{"items":[]}`), duration: time.Second, size: 12}
	tests := []headerEntry{
		{key: "status", value: "200"},
		{key: "header.X-Missing", value: "*"},
		{key: "body.matches", value: "["},
		{key: "json.items[2]", value: "value"},
		{key: "time.lt", value: "fast"},
		{key: "size.lt", value: "tiny"},
		{key: "unknown", value: "value"},
	}
	results := evaluateAssertions(tests, response)
	for i, result := range results {
		if result.Passed {
			t.Errorf("failure assertion %d passed: %#v", i, result)
		}
	}
	if results[2].Error == "" || results[3].Error == "" || results[4].Error == "" || results[5].Error == "" || results[6].Error == "" {
		t.Fatalf("invalid assertions lack errors: %#v", results)
	}
	formatted := formatAssertionResults(results)
	if !strings.Contains(formatted, "0/7 assertions passed") || !strings.Contains(formatted, "✗ status") {
		t.Fatalf("formatted assertions = %q", formatted)
	}
}

func TestResponseVariableExtractions(t *testing.T) {
	response := assertionResponse{
		status:  http.StatusCreated,
		headers: http.Header{"X-Request-Id": {"req-123"}},
		body:    []byte(`{"auth":{"token":"secret"},"count":3,"message":"id=42"}`),
	}
	rules := []headerEntry{
		{key: "set.token", value: "json.auth.token"},
		{key: "set.requestId", value: "header.X-Request-ID"},
		{key: "set.count", value: "json.count"},
		{key: "set.id", value: `body.matches:id=(\d+)`},
		{key: "set.statusCode", value: "status"},
	}
	results := evaluateAssertions(rules, response)
	updates := successfulVariableUpdates(rules, results)
	values := map[string]string{}
	for _, update := range updates {
		values[update.key] = update.value
	}
	if len(updates) != 5 || values["token"] != "secret" || values["requestId"] != "req-123" || values["count"] != "3" || values["id"] != "42" || values["statusCode"] != "201" {
		t.Fatalf("extraction updates = %#v; results=%#v", updates, results)
	}
}

func TestFailedResponseExtractionIsDescriptive(t *testing.T) {
	rules := []headerEntry{{key: "set.token", value: "json.missing"}, {key: "set.id", value: "body.matches:["}}
	results := evaluateAssertions(rules, assertionResponse{body: []byte(`{"ok":true}`)})
	if results[0].Passed || results[0].Error == "" || results[1].Passed || !strings.Contains(results[1].Error, "regular expression") {
		t.Fatalf("failed extractions = %#v", results)
	}
	if updates := successfulVariableUpdates(rules, results); len(updates) != 0 {
		t.Fatalf("failed extractions produced updates: %#v", updates)
	}
}

func TestTruncatedResponseSkipsBodyAndUnknownSizeAssertions(t *testing.T) {
	response := assertionResponse{
		status:           http.StatusOK,
		headers:          http.Header{"X-Response-Kind": {"large"}},
		body:             []byte(`{"token":"partial"}`),
		bodyTruncated:    true,
		duration:         25 * time.Millisecond,
		size:             maxAssertionResponseBody + 1,
		sizeIsLowerBound: true,
	}
	rules := []headerEntry{
		{key: "status", value: "200"},
		{key: "header.X-Response-Kind", value: "large"},
		{key: "time.lt", value: "1s"},
		{key: "size.lt", value: "100MiB"},
		{key: "set.statusCode", value: "status"},
		{key: "set.kind", value: "header.X-Response-Kind"},
		{key: "body.contains", value: "partial"},
		{key: "body.matches", value: "partial"},
		{key: "json.token", value: "partial"},
		{key: "set.body", value: "body"},
		{key: "set.token", value: "json.token"},
		{key: "set.match", value: "body.matches:(partial)"},
	}

	results := evaluateAssertions(rules, response)
	for _, index := range []int{0, 1, 2, 4, 5} {
		result := results[index]
		if !result.Passed {
			t.Errorf("metadata assertion %d failed: %#v", index, result)
		}
	}
	if result := results[3]; result.Passed || !strings.Contains(result.Error, "total response size is unknown") || !strings.HasPrefix(result.Actual, ">=") {
		t.Errorf("size assertion was evaluated against a lower bound: %#v", result)
	}
	for index, result := range results[6:] {
		if result.Passed || !strings.Contains(result.Error, "64.0 MiB assertion limit") {
			t.Errorf("body assertion %d was evaluated against truncated data: %#v", index, result)
		}
	}

	updates := successfulVariableUpdates(rules, results)
	if len(updates) != 2 || updates[0] != (headerEntry{key: "statusCode", value: "200"}) || updates[1] != (headerEntry{key: "kind", value: "large"}) {
		t.Fatalf("metadata extraction updates = %#v", updates)
	}
}

func TestDoRequestEvaluatesAndDisplaysAssertions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer server.Close()
	m := NewModel()
	m.responseTestsModel.SetWidth(80)
	m.responseTestsModel.SetHeight(10)
	m.urlInput.SetValue(server.URL)
	m.testsInput.SetEntries([]headerEntry{{key: "status", value: "201"}, {key: "json.id", value: "42"}})
	response := m.DoRequest()().(responseMsg)
	if len(response.assertionResults) != 2 || !response.assertionResults[0].Passed || !response.assertionResults[1].Passed {
		t.Fatalf("assertion response = %#v", response.assertionResults)
	}
	m.requestId = response.requestID
	updated, _ := m.Update(response)
	m = updated.(model)
	if !strings.Contains(m.responseTests, "2/2 assertions passed") || !strings.Contains(m.responseTestsModel.View(), "✓ status") {
		t.Fatalf("displayed tests = %q", m.responseTests)
	}
}

func TestDoRequestAppliesExtractedVariableToActiveEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"fresh-token"}`))
	}))
	defer server.Close()
	m := NewModel()
	m.urlInput.SetValue(server.URL)
	m.variablesInput.SetEntries([]headerEntry{{key: "token", value: "old-token"}})
	m.testsInput.SetEntries([]headerEntry{{key: "set.token", value: "json.token"}})
	response := m.DoRequest()().(responseMsg)
	if len(response.variableUpdates) != 1 || response.variableUpdates[0].value != "fresh-token" {
		t.Fatalf("response variable updates = %#v", response.variableUpdates)
	}
	m.requestId = response.requestID
	updated, _ := m.Update(response)
	m = updated.(model)
	if got := m.variablesInput.Entries()[0].value; got != "fresh-token" {
		t.Fatalf("active environment token = %q", got)
	}
}

func TestResponseTestsTabNavigation(t *testing.T) {
	m := NewModel()
	m.focus = paneResponse
	m.responseTests = "test results"
	m.responseTestsModel.SetContent(m.responseTests)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updated.(model)
	if m.responseTab != responseTabTests {
		t.Fatalf("left wrap tab = %d, want tests", m.responseTab)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(model)
	if m.responseTab != responseTabBody {
		t.Fatalf("right wrap tab = %d, want body", m.responseTab)
	}
}

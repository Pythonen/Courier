package tui

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunCollectionUsesEnvironmentCookiesOrderAndIterations(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "runner-token", Path: "/"})
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("logged in"))
		case "/profile":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "runner-token" || r.Header.Get("X-Run") != "yes" {
				http.Error(w, "missing runner state", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("profile"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := NewModel()
	m.variablesInput.SetEntries([]headerEntry{{key: "baseUrl", value: server.URL}, {key: "runHeader", value: "yes"}})
	m.savedRequests = []savedRequest{
		{name: "Auth / Login", method: "POST", url: "{{baseUrl}}/login", body: bodyConfig{mode: bodyNone}},
		{name: "Auth / Profile", method: "GET", url: "{{baseUrl}}/profile", headers: []headerEntry{{key: "X-Run", value: "{{runHeader}}"}}, body: bodyConfig{mode: bodyNone}},
	}

	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "Auth", Iterations: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 4 || report.Passed != 4 || report.Failed != 0 || calls.Load() != 4 {
		t.Fatalf("report = %#v calls=%d", report, calls.Load())
	}
	if report.Results[0].Name != "Auth / Login" || report.Results[1].Name != "Auth / Profile" || report.Results[2].Iteration != 2 {
		t.Fatalf("result ordering = %#v", report.Results)
	}
	if report.Results[0].Status != http.StatusCreated || report.Results[0].Bytes != len("logged in") || report.Results[1].URL != server.URL+"/profile" {
		t.Fatalf("structured result = %#v", report.Results)
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatalf("report is not JSON serializable: %v", err)
	}
}

func TestRunCollectionChainsExtractedVariables(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"runner-secret"}`))
		case "/profile":
			if r.Header.Get("Authorization") != "Bearer runner-secret" {
				http.Error(w, "missing extracted token", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := NewModel()
	m.variablesInput.SetEntries([]headerEntry{{key: "baseUrl", value: server.URL}})
	m.savedRequests = []savedRequest{
		{name: "Auth / Login", method: "POST", url: "{{baseUrl}}/login", body: bodyConfig{mode: bodyNone}, tests: []headerEntry{{key: "set.token", value: "json.access_token"}}},
		{name: "Auth / Profile", method: "GET", url: "{{baseUrl}}/profile", auth: authConfig{typeID: authBearer, bearerToken: "{{token}}"}, body: bodyConfig{mode: bodyNone}},
	}
	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "Auth", Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 2 || report.Failed != 0 {
		t.Fatalf("chained report = %#v", report)
	}
	values := map[string]string{}
	for _, entry := range m.variablesInput.Entries() {
		values[entry.key] = entry.value
	}
	if values["token"] != "runner-secret" {
		t.Fatalf("runner environment = %#v", values)
	}
}

func TestRunCollectionConsumesServerSentEvents(t *testing.T) {
	streamBody := "data: one\n\ndata: two\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{"data: one\n\n", "data: two\n\n"} {
			_, _ = w.Write([]byte(event))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	m := NewModel()
	m.savedRequests = []savedRequest{{
		name: "Events", method: "GET", url: server.URL, headers: []headerEntry{{key: "Accept", value: "text/event-stream"}}, body: bodyConfig{mode: bodyNone},
		tests: []headerEntry{{key: "status", value: "200"}, {key: "body.contains", value: "data: two"}},
	}}
	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Passed != 1 || report.Results[0].Bytes != len(streamBody) || len(report.Results[0].Assertions) != 2 {
		t.Fatalf("SSE runner report = %#v", report)
	}
}

func TestRunCollectionReportsInteractiveWebSocketRequest(t *testing.T) {
	m := NewModel()
	m.variablesInput.SetEntries([]headerEntry{{key: "socket", value: "ws://example.test/socket"}})
	m.savedRequests = []savedRequest{{name: "Live socket", method: "GET", url: "{{socket}}", body: bodyConfig{mode: bodyRaw, rawType: rawText, raw: "hello"}}}
	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Failed != 1 || report.Results[0].Method != "WS" || !strings.Contains(report.Results[0].Error, "interactive WebSocket") {
		t.Fatalf("WebSocket runner report = %#v", report)
	}
}

func TestRunCollectionUsesIterationDataAndRestoresEnvironment(t *testing.T) {
	var mutex sync.Mutex
	var values []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		values = append(values, request.URL.Query().Get("item")+":"+request.URL.Query().Get("base"))
		mutex.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	m := NewModel()
	m.variablesInput.SetEntries([]headerEntry{{key: "baseUrl", value: server.URL}, {key: "base", value: "workspace"}, {key: "item", value: "original"}})
	m.savedRequests = []savedRequest{{
		name: "Data request", method: "GET", url: "{{baseUrl}}?item={{item}}&base={{base}}", body: bodyConfig{mode: bodyNone},
	}}
	report, err := m.RunCollection(context.Background(), RunOptions{
		Selector: "all", Iterations: 2, Data: []map[string]string{{"item": "red"}, {"item": "blue"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 4 || report.Passed != 4 {
		t.Fatalf("data-driven report = %#v", report)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if got := strings.Join(values, ","); got != "red:workspace,blue:workspace,red:workspace,blue:workspace" {
		t.Fatalf("iteration values = %q", got)
	}
	entries := m.variablesInput.Entries()
	if len(entries) != 3 || entries[2].value != "original" {
		t.Fatalf("base environment was not restored: %#v", entries)
	}
}

func TestLoadRunDataJSONAndCSV(t *testing.T) {
	tempDir := t.TempDir()
	jsonPath := filepath.Join(tempDir, "iterations.json")
	if err := os.WriteFile(jsonPath, []byte(`[{"name":"Ada","count":2,"enabled":true,"metadata":{"role":"admin"},"empty":null}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	jsonRows, err := LoadRunData(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(jsonRows) != 1 || jsonRows[0]["name"] != "Ada" || jsonRows[0]["count"] != "2" || jsonRows[0]["enabled"] != "true" || jsonRows[0]["metadata"] != `{"role":"admin"}` || jsonRows[0]["empty"] != "" {
		t.Fatalf("JSON runner data = %#v", jsonRows)
	}

	csvPath := filepath.Join(tempDir, "iterations.csv")
	if err := os.WriteFile(csvPath, []byte("\ufeffname,count\nAda,2\nGrace,3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	csvRows, err := LoadRunData(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(csvRows) != 2 || csvRows[0]["name"] != "Ada" || csvRows[1]["count"] != "3" {
		t.Fatalf("CSV runner data = %#v", csvRows)
	}

	invalidPath := filepath.Join(tempDir, "invalid.csv")
	if err := os.WriteFile(invalidPath, []byte("name,name\nAda,Lovelace\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRunData(invalidPath); err == nil || !strings.Contains(err.Error(), "duplicate column") {
		t.Fatalf("duplicate CSV headings error = %v", err)
	}
}

func TestRunCollectionRecordsHTTPAndTransportFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	m := NewModel()
	m.savedRequests = []savedRequest{
		{name: "HTTP failure", method: "GET", url: server.URL, body: bodyConfig{mode: bodyNone}},
		{name: "Invalid URL", method: "GET", url: "://bad", body: bodyConfig{mode: bodyNone}},
	}
	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 2 || report.Passed != 0 || report.Failed != 2 {
		t.Fatalf("report = %#v", report)
	}
	if report.Results[0].Status != http.StatusBadGateway || report.Results[0].Error != "" {
		t.Fatalf("HTTP result = %#v", report.Results[0])
	}
	if report.Results[1].Status != 0 || !strings.Contains(report.Results[1].Error, "Error parsing URL") {
		t.Fatalf("transport result = %#v", report.Results[1])
	}
	formatted := FormatRunReport(report)
	if !strings.Contains(formatted, "FAIL") || !strings.Contains(formatted, "502") || !strings.Contains(formatted, "ERROR") || !strings.Contains(formatted, "0 passed, 2 failed") {
		t.Fatalf("formatted report = %q", formatted)
	}
}

func TestRunCollectionFailsSuccessfulHTTPResponseOnAssertion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2}`))
	}))
	defer server.Close()
	m := NewModel()
	m.savedRequests = []savedRequest{{
		name: "Assertion failure", method: "GET", url: server.URL, body: bodyConfig{mode: bodyNone},
		tests: []headerEntry{{key: "status", value: "200"}, {key: "json.count", value: "3"}},
	}}
	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 0 || report.Failed != 1 || report.Results[0].Status != 200 || report.Results[0].AssertionFailures != 1 || len(report.Results[0].Assertions) != 2 {
		t.Fatalf("assertion report = %#v", report)
	}
	formatted := FormatRunReport(report)
	if !strings.Contains(formatted, "✓ status = 200") || !strings.Contains(formatted, "✗ json.count = 3 — got 2") {
		t.Fatalf("formatted assertion report = %q", formatted)
	}
}

func TestRunCollectionSelectionAndValidation(t *testing.T) {
	m := NewModel()
	m.savedRequests = []savedRequest{
		{name: "One", method: "GET", url: "https://example.test", body: bodyConfig{mode: bodyNone}},
		{name: "Folder / Two", method: "GET", url: "https://example.test", body: bodyConfig{mode: bodyNone}},
	}
	selected, err := m.selectSavedRequests("2")
	if err != nil || len(selected) != 1 || selected[0].name != "Folder / Two" {
		t.Fatalf("index selection = %#v, %v", selected, err)
	}
	selected, err = m.selectSavedRequests("Folder")
	if err != nil || len(selected) != 1 || selected[0].name != "Folder / Two" {
		t.Fatalf("folder selection = %#v, %v", selected, err)
	}
	if _, err := m.selectSavedRequests("missing"); err == nil {
		t.Fatal("missing selector was accepted")
	}
	if _, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 0}); err == nil {
		t.Fatal("zero iterations were accepted")
	}
	if _, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 1, Delay: -time.Second}); err == nil {
		t.Fatal("negative delay was accepted")
	}
}

func TestRunCollectionCancellationDuringDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := NewModel()
	m.savedRequests = []savedRequest{
		{name: "One", method: "GET", url: server.URL, body: bodyConfig{mode: bodyNone}},
		{name: "Two", method: "GET", url: server.URL, body: bodyConfig{mode: bodyNone}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	report, err := m.RunCollection(ctx, RunOptions{Selector: "all", Iterations: 1, Delay: time.Second})
	if err == nil || report.Total != 1 || report.Passed != 1 {
		t.Fatalf("cancelled report = %#v err=%v", report, err)
	}
}

func TestRunCollectionBailsAfterFirstFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "failure", http.StatusInternalServerError)
	}))
	defer server.Close()
	m := NewModel()
	m.savedRequests = []savedRequest{
		{name: "One", method: "GET", url: server.URL, body: bodyConfig{mode: bodyNone}},
		{name: "Two", method: "GET", url: server.URL, body: bodyConfig{mode: bodyNone}},
	}
	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 3, Bail: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Failed != 1 || report.Passed != 0 || calls.Load() != 1 {
		t.Fatalf("fail-fast report = %#v calls=%d", report, calls.Load())
	}
}

func TestFormatRunReportJUnit(t *testing.T) {
	report := RunReport{
		Total: 3, Passed: 1, Failed: 2, DurationMS: 1250,
		Results: []RunResult{
			{Iteration: 1, Name: "Passing <request>", Method: "GET", Status: 200, DurationMS: 125, Passed: true},
			{Iteration: 1, Name: "Assertion request", Method: "GET", Status: 200, DurationMS: 250, AssertionFailures: 1, Assertions: []AssertionResult{{Expression: "json.count", Expected: "3", Actual: "2"}}},
			{Iteration: 1, Name: "Transport request", Method: "GET", DurationMS: 10, Error: "dial: connection refused"},
		},
	}
	encoded, err := FormatRunReportJUnit(report)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Suite    struct {
			Cases []struct {
				Name    string `xml:"name,attr"`
				Failure *struct {
					Body string `xml:",chardata"`
				} `xml:"failure"`
				Error *struct {
					Body string `xml:",chardata"`
				} `xml:"error"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal([]byte(encoded), &document); err != nil {
		t.Fatalf("JUnit XML is invalid: %v\n%s", err, encoded)
	}
	if document.Tests != 3 || document.Failures != 1 || document.Errors != 1 || len(document.Suite.Cases) != 3 {
		t.Fatalf("JUnit report = %#v\n%s", document, encoded)
	}
	if document.Suite.Cases[0].Name != "Passing <request>" || !strings.Contains(document.Suite.Cases[1].Failure.Body, "json.count expected 3, got 2") || !strings.Contains(document.Suite.Cases[2].Error.Body, "connection refused") {
		t.Fatalf("JUnit cases = %#v\n%s", document.Suite.Cases, encoded)
	}
}

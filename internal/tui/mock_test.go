package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMockHandlerMatchesMethodPathQueryAndExampleHeaders(t *testing.T) {
	m := NewModel()
	m.savedRequests = []savedRequest{
		{
			name: "Users / JSON", method: "GET", url: "https://api.example.test/users/{{id}}", params: []headerEntry{{key: "format", value: "json"}},
			examples: []savedExample{
				{name: "Found", statusCode: 200, responseRaw: `{"id":"{{id}}","format":"json"}`, responseRawAvailable: true, responseHeaders: "Content-Type: application/json\nX-User: {{id}}\n"},
				{name: "Missing", statusCode: 404, responseRaw: `{"error":"missing {{id}}"}`, responseRawAvailable: true, responseHeaders: "Content-Type: application/json\n"},
			},
		},
		{
			name: "Users / XML", method: "GET", url: "https://api.example.test/users/{{id}}", params: []headerEntry{{key: "format", value: "xml"}},
			examples: []savedExample{{name: "XML", statusCode: 200, responseRaw: `<user/>`, responseRawAvailable: true}},
		},
	}
	handler, err := m.MockHandler("Users")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := http.Get(server.URL + "/users/42?format=json") //nolint:noctx // Test client request.
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || string(body) != `{"id":"42","format":"json"}` || response.Header.Get("X-User") != "42" || response.Header.Get("X-Courier-Mock-Example") != "Found" {
		t.Fatalf("default mock response = status %d headers %#v body %q", response.StatusCode, response.Header, body)
	}

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/users/99?format=json", nil)
	request.Header.Set("X-Mock-Response-Code", "404")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 404 || !strings.Contains(string(body), "missing 99") || response.Header.Get("X-Courier-Mock-Example") != "Missing" {
		t.Fatalf("selected mock response = status %d headers %#v body %q", response.StatusCode, response.Header, body)
	}
}

func TestMockHandlerOptionalHeaderAndBodyMatching(t *testing.T) {
	m := NewModel()
	m.savedRequests = []savedRequest{{
		name: "Create", method: "POST", url: "https://api.example.test/items",
		headers: []headerEntry{{key: "X-Mode", value: "full"}}, body: bodyConfig{mode: bodyRaw, rawType: rawJSON, raw: `{"name":"Ada"}`},
		examples: []savedExample{{name: "Created", statusCode: 201, responseRaw: `{"ok":true}`, responseRawAvailable: true}},
	}}
	handler, err := m.MockHandler("all")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://mock/items", strings.NewReader(`{"name":"Ada"}`))
	request.Header.Set("X-Mode", "full")
	request.Header.Set("X-Mock-Match-Request-Headers", "X-Mode")
	request.Header.Set("X-Mock-Match-Request-Body", "true")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != 201 || recorder.Body.String() != `{"ok":true}` {
		t.Fatalf("matched mock = %d %q", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://mock/items", strings.NewReader(`{"name":"Grace"}`))
	request.Header.Set("X-Mode", "full")
	request.Header.Set("X-Mock-Match-Request-Headers", "X-Mode")
	request.Header.Set("X-Mock-Match-Request-Body", "true")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("mismatched body status = %d", recorder.Code)
	}
}

func TestMockHandlerValidationAndBadSelectionHeaders(t *testing.T) {
	m := NewModel()
	m.savedRequests = []savedRequest{{name: "No examples", method: "GET", url: "https://example.test"}}
	if _, err := m.MockHandler("all"); err == nil || !strings.Contains(err.Error(), "no saved response examples") {
		t.Fatalf("empty mock handler error = %v", err)
	}
	m.savedRequests[0].examples = []savedExample{{name: "OK", statusCode: 200, responseRawAvailable: true}}
	handler, err := m.MockHandler("all")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://mock/", nil)
	request.Header.Set("X-Mock-Response-Code", "invalid")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid response-code selection status = %d", recorder.Code)
	}
}

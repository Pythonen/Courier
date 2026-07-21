package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func methodIndex(t *testing.T, method string) int {
	t.Helper()
	for i, m := range methods {
		if m == method {
			return i
		}
	}
	t.Fatalf("method %q not found in methods", method)
	return -1
}

func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(s, "")
}

func TestDoRequest_Integration(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath, gotBody string
	var gotHeaders []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body failed: %v", err)
		}

		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeaders = r.Header.Values("X-Test")
		gotBody = string(body)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Server", "teatest")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"name":"courier"}`))
	}))
	defer srv.Close()

	m := NewModel()
	m.methodIdx = methodIndex(t, "POST")
	m.urlInput.SetValue(srv.URL + "/api")
	m.bodyInput.SetValue(`{"from":"test"}`)
	m.headersInput.SetEntries([]headerEntry{
		{key: "X-Test", value: "abc123"},
		{key: "X-Test", value: "second"},
	})

	msg := m.DoRequest()()
	resp, ok := msg.(responseMsg)
	if !ok {
		t.Fatalf("message type = %T, want responseMsg", msg)
	}

	if gotMethod != "POST" {
		t.Fatalf("server saw method %q, want POST", gotMethod)
	}
	if gotPath != "/api" {
		t.Fatalf("server saw path %q, want /api", gotPath)
	}
	if strings.Join(gotHeaders, ",") != "abc123,second" {
		t.Fatalf("server saw X-Test values %q", gotHeaders)
	}
	if gotBody != `{"from":"test"}` {
		t.Fatalf("server saw body %q, want JSON payload", gotBody)
	}

	body := stripANSI(resp.responseBody)
	if !strings.Contains(body, `"ok": true`) {
		t.Fatalf("response body missing pretty JSON field, got: %q", body)
	}
	if !strings.Contains(body, `"name": "courier"`) {
		t.Fatalf("response body missing name field, got: %q", body)
	}

	if !strings.Contains(resp.responseHeaders, "Content-Type: application/json") {
		t.Fatalf("response headers missing content type, got: %q", resp.responseHeaders)
	}
	if !strings.Contains(resp.responseHeaders, "X-Server: teatest") {
		t.Fatalf("response headers missing server marker, got: %q", resp.responseHeaders)
	}
	if !strings.Contains(resp.responseMeta, "201 Created") || !strings.Contains(resp.responseMeta, "B") {
		t.Fatalf("response metadata missing status or size, got: %q", resp.responseMeta)
	}
}

func TestDoRequest_Timeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("too late"))
	}))
	defer srv.Close()

	m := NewModel()
	m.client.Timeout = 10 * time.Millisecond
	m.urlInput.SetValue(srv.URL)

	resp := m.DoRequest()().(responseMsg)
	if !strings.Contains(resp.responseBody, "Client.Timeout") {
		t.Fatalf("timeout error not surfaced, got: %q", resp.responseBody)
	}
	if !strings.Contains(resp.responseMeta, "Request failed") {
		t.Fatalf("timeout metadata missing failure state, got: %q", resp.responseMeta)
	}
}

func TestDoRequest_ResponseBodyLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBody+1)))
	}))
	defer srv.Close()

	m := NewModel()
	m.urlInput.SetValue(srv.URL)

	resp := m.DoRequest()().(responseMsg)
	if !strings.Contains(resp.responseBody, "exceeds the 10.0 MiB display limit") {
		t.Fatalf("oversized response was not rejected, got: %q", resp.responseBody)
	}
}

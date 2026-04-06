package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
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

	var gotMethod, gotPath, gotHeader, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body failed: %v", err)
		}

		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-Test")
		gotBody = string(body)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Server", "teatest")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"name":"courier"}`))
	}))
	defer srv.Close()

	m := newModel()
	m.methodIdx = methodIndex(t, "POST")
	m.urlInput.SetValue(srv.URL + "/api")
	m.bodyInput.SetValue(`{"from":"test"}`)
	m.headersInput.rows[0].key.SetValue("X-Test")
	m.headersInput.rows[0].value.SetValue("abc123")

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
	if gotHeader != "abc123" {
		t.Fatalf("server saw X-Test %q, want abc123", gotHeader)
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
}

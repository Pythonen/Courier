package tui

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func startUnixHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("filesystem Unix sockets are not available on Windows")
	}
	directory, err := os.MkdirTemp("/tmp", "courier-uds-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "courier.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		_ = listener.Close()
	})
	return path
}

func TestUnixSocketHTTPRequestUsesPostmanURLSyntax(t *testing.T) {
	var gotPath, gotQuery, gotHost string
	socket := startUnixHTTPServer(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		gotQuery = request.URL.Query().Get("source")
		gotHost = request.Host
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("X-Socket", "yes")
		_, _ = response.Write([]byte(`{"transport":"unix"}`))
	}))

	m := NewModel()
	m.urlInput.SetValue("http://unix:" + socket + ":/v1/status")
	m.paramsInput.SetEntries([]headerEntry{{key: "source", value: "courier"}})
	m.headersInput.SetEntries([]headerEntry{{key: "Host", value: "daemon.local"}})
	m.testsInput.SetEntries([]headerEntry{{key: "status", value: "200"}, {key: "json.transport", value: "unix"}})
	response := m.DoRequest()().(responseMsg)

	if response.statusCode != http.StatusOK || gotPath != "/v1/status" || gotQuery != "courier" || gotHost != "daemon.local" {
		t.Fatalf("Unix socket request = response %#v path=%q query=%q host=%q", response, gotPath, gotQuery, gotHost)
	}
	if !strings.HasPrefix(response.finalURL, "http://unix:"+socket+":/v1/status") || strings.Contains(response.finalURL, "localhost") {
		t.Fatalf("Unix socket final URL = %q", response.finalURL)
	}
	for _, result := range response.assertionResults {
		if !result.Passed {
			t.Fatalf("Unix socket assertion = %#v", result)
		}
	}
}

func TestCollectionRunnerExecutesUnixSocketRequests(t *testing.T) {
	socket := startUnixHTTPServer(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	m := NewModel()
	m.savedRequests = []savedRequest{{
		name: "Daemon / Ping", method: "GET", url: "unix:" + socket + ":/ping",
		body: bodyConfig{mode: bodyNone}, tests: []headerEntry{{key: "status", value: "204"}},
	}}
	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Passed != 1 || report.Results[0].Status != http.StatusNoContent || !strings.HasPrefix(report.Results[0].URL, "http://unix:") {
		t.Fatalf("Unix socket runner report = %#v", report)
	}
}

func TestParseUnixSocketURL(t *testing.T) {
	target, matched, err := parseUnixSocketURL("unix:/tmp/courier.sock:/hello?name=world")
	if err != nil || !matched || target.socketPath != "/tmp/courier.sock" || target.requestURL != "http://localhost/hello?name=world" {
		t.Fatalf("parsed Unix socket target = %#v matched=%v err=%v", target, matched, err)
	}
	if target, matched, err = parseUnixSocketURL("https://example.test/path"); err != nil || matched || target != nil {
		t.Fatalf("ordinary URL parsed as Unix socket = %#v matched=%v err=%v", target, matched, err)
	}
	if _, matched, err = parseUnixSocketURL("unix:relative.sock:/hello"); !matched || err == nil {
		t.Fatalf("relative Unix socket = matched=%v err=%v", matched, err)
	}
}

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportHARRequestsAndResponses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.har")
	data := `{"log":{"version":"1.2","entries":[{"request":{"method":"POST","url":"https://api.example/users?debug=true","headers":[{"name":"Content-Type","value":"application/json"}],"queryString":[{"name":"debug","value":"true"}],"cookies":[{"name":"session","value":"abc"}],"postData":{"mimeType":"application/json","text":"{\"name\":\"Ada\"}"}},"response":{"status":201,"statusText":"Created","headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","text":"eyJpZCI6NDJ9","encoding":"base64"}}}]}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	count, err := m.ImportHAR(path)
	if err != nil || count != 1 || len(m.savedRequests) != 1 {
		t.Fatalf("import = count %d requests %d err %v", count, len(m.savedRequests), err)
	}
	request := m.savedRequests[0]
	if request.method != "POST" || request.url != "https://api.example/users" || len(request.params) != 1 || request.body.mode != bodyRaw || request.body.rawType != rawJSON || !strings.Contains(request.body.raw, "Ada") {
		t.Fatalf("request = %#v", request)
	}
	if len(request.cookies) != 1 || len(request.examples) != 1 || request.examples[0].statusCode != 201 || request.examples[0].responseRaw != `{"id":42}` {
		t.Fatalf("cookies/examples = %#v %#v", request.cookies, request.examples)
	}
}

func TestImportHARDoesNotMutateOnInvalidEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.har")
	if err := os.WriteFile(path, []byte(`{"log":{"entries":[{"request":{"method":"GET","url":"not a URL"}}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.savedRequests = []savedRequest{{name: "existing"}}
	if _, err := m.ImportHAR(path); err == nil {
		t.Fatal("expected invalid HAR error")
	}
	if len(m.savedRequests) != 1 || m.savedRequests[0].name != "existing" {
		t.Fatalf("failed import mutated collection: %#v", m.savedRequests)
	}
}

func TestHARRoundTripPreservesCourierRequestAndExamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.har")
	original := savedRequest{
		name: "Users / Create", method: "POST", url: "https://api.example/users?fixed=1",
		headers: []headerEntry{{key: "X-Trace", value: "abc"}}, params: []headerEntry{{key: "debug", value: "true"}}, cookies: []headerEntry{{key: "session", value: "cookie"}},
		auth:  authConfig{typeID: authOAuth2RefreshToken, oauthTokenURL: "https://id.example/token", oauthClientID: "client", oauthRefreshToken: "refresh"},
		body:  bodyConfig{mode: bodyGraphQL, graphqlQuery: "mutation { create }", graphqlVariables: `{"id":1}`, graphqlOperationName: "Create"},
		tests: []headerEntry{{key: "status", value: "201"}},
		examples: []savedExample{
			{name: "Created", statusCode: 201, responseRaw: `{"id":1}`, responseRawAvailable: true, responseHeaders: "Content-Type: application/json", responseMeta: "201 Created"},
			{name: "Rejected", statusCode: 400, responseRaw: `{"error":"bad"}`, responseRawAvailable: true, responseHeaders: "Content-Type: application/json", responseMeta: "400 Bad Request"},
		},
	}
	m := NewModel()
	m.savedRequests = []savedRequest{original}
	if err := m.ExportHAR(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("HAR permissions = %v (%v)", info.Mode().Perm(), err)
	}
	if err := m.ExportHAR(path); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error = %v", err)
	}
	imported := NewModel()
	count, err := imported.ImportHAR(path)
	if err != nil || count != 1 || len(imported.savedRequests) != 1 {
		t.Fatalf("round trip import = %d %#v %v", count, imported.savedRequests, err)
	}
	got := imported.savedRequests[0]
	if got.name != original.name || got.method != original.method || got.url != original.url || got.auth != original.auth || got.body.mode != original.body.mode || got.body.graphqlQuery != original.body.graphqlQuery || got.body.graphqlVariables != original.body.graphqlVariables || got.body.graphqlOperationName != original.body.graphqlOperationName || len(got.params) != 1 || len(got.tests) != 1 || len(got.examples) != 2 {
		t.Fatalf("round trip request = %#v", got)
	}
	if got.examples[0].name != "Created" || got.examples[1].name != "Rejected" || got.examples[1].responseRaw != `{"error":"bad"}` {
		t.Fatalf("round trip examples = %#v", got.examples)
	}
}

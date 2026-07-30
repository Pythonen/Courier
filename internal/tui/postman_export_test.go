package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExportPostmanCollectionRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "courier.postman_collection.json")
	m := NewModel()
	m.workspacePath = filepath.Join(tempDir, "API Workspace.json")
	m.savedRequests = []savedRequest{
		{name: "Examples / Raw", method: "POST", url: "https://example.test/raw", headers: []headerEntry{{key: "X-Test", value: "yes"}}, params: []headerEntry{{key: "expand", value: "full value"}}, cookies: []headerEntry{{key: "session", value: "{{token}}"}}, auth: authConfig{typeID: authBearer, bearerToken: "{{token}}"}, body: bodyConfig{mode: bodyRaw, rawType: rawJSON, raw: `{"ok":true}`}, tests: []headerEntry{{key: "status", value: "201"}, {key: "json.user.id", value: "42"}, {key: "set.token", value: "json.access_token"}}},
		{name: "Examples / Form", method: "POST", url: "https://example.test/form", auth: authConfig{typeID: authBasic, username: "user", password: "pass"}, body: bodyConfig{mode: bodyFormURLEncoded, form: []headerEntry{{key: "name", value: "Ada"}}}},
		{name: "Examples / Multipart", method: "POST", url: "https://example.test/upload", auth: authConfig{typeID: authDigest, username: "digest", password: "secret"}, body: bodyConfig{mode: bodyMultipart, multipart: []headerEntry{{key: "file", value: "@fixture.txt"}, {key: "literal", value: "@@starts-with-at"}}}},
		{name: "Examples / Binary", method: "PUT", url: "https://example.test/binary", auth: authConfig{typeID: authAPIKey, apiKeyName: "api_key", apiKeyValue: "secret", apiKeyLocation: apiKeyQuery}, body: bodyConfig{mode: bodyBinary, binaryPath: "archive.zip"}},
		{name: "Examples / GraphQL", method: "POST", url: "https://example.test/graphql", auth: authConfig{typeID: authAWSSignatureV4, awsAccessKey: "{{awsAccess}}", awsSecretKey: "{{awsSecret}}", awsRegion: "us-east-1", awsService: "execute-api", awsSessionToken: "{{awsToken}}"}, body: bodyConfig{mode: bodyGraphQL, graphqlQuery: "query Viewer { viewer { id } }", graphqlVariables: `{"enabled":true}`, graphqlOperationName: "Viewer"}},
		{name: "Examples / OAuth", method: "GET", url: "https://example.test/oauth", auth: authConfig{typeID: authOAuth2ClientCredentials, oauthTokenURL: "{{issuer}}/token", oauthClientID: "client", oauthClientSecret: "secret", oauthScope: "read write"}, body: bodyConfig{mode: bodyNone, rawType: rawJSON}},
	}
	m.savedRequests[0].examples = []savedExample{{name: "Created user", statusCode: 201, responseBody: formatResponseBody([]byte(`{"id":42}`), "text/plain"), responseRaw: `{"id":42}`, responseRawAvailable: true, responseHeaders: "Content-Type: text/plain\nX-Request-Id: req-42", responseMeta: "201 Created"}}
	if err := m.ExportPostmanCollection(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("collection export mode = %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exported postmanExportCollection
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Info.Name != "API Workspace" || exported.Info.Schema != postmanCollectionSchema || len(exported.Item) != 1 || exported.Item[0].Name != "Examples" || len(exported.Item[0].Item) != len(m.savedRequests) {
		t.Fatalf("exported collection structure = %#v", exported)
	}
	if len(exported.Item[0].Item[0].Event) != 1 || !strings.Contains(strings.Join(exported.Item[0].Item[0].Event[0].Script.Exec, "\n"), "pm.response.code") || !strings.Contains(strings.Join(exported.Item[0].Item[0].Event[0].Script.Exec, "\n"), "pm.response.json()") || !strings.Contains(strings.Join(exported.Item[0].Item[0].Event[0].Script.Exec, "\n"), "pm.environment.set") {
		t.Fatalf("exported Postman tests = %#v", exported.Item[0].Item[0].Event)
	}
	if responses := exported.Item[0].Item[0].Response; len(responses) != 1 || responses[0].Name != "Created user" || responses[0].Code != 201 || responses[0].Body != `{"id":42}` || len(responses[0].Header) != 2 {
		t.Fatalf("exported examples = %#v", responses)
	}

	imported := NewModel()
	count, err := imported.ImportPostmanCollection(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != len(m.savedRequests) {
		t.Fatalf("round-trip request count = %d", count)
	}
	for index := range m.savedRequests {
		want, got := m.savedRequests[index], imported.savedRequests[index]
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round-trip request %d:\n got  %#v\n want %#v", index, got, want)
		}
	}
	if err := m.ExportPostmanCollection(path); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("collection overwrite error = %v", err)
	}
}

func TestExportPostmanEnvironmentRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "courier.postman_environment.json")
	m := NewModel()
	m.variablesInput.SetEntries([]headerEntry{{key: "baseUrl", value: "https://api.example.test"}, {key: "token", value: "secret"}})
	if err := m.ExportPostmanEnvironment(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("environment export mode = %o", info.Mode().Perm())
	}
	imported := NewModel()
	count, err := imported.ImportPostmanEnvironment(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || !reflect.DeepEqual(imported.variablesInput.Entries(), m.variablesInput.Entries()) {
		t.Fatalf("round-trip environment = %#v", imported.variablesInput.Entries())
	}
	if err := m.ExportPostmanEnvironment(path); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("environment overwrite error = %v", err)
	}
}

func TestExportPostmanAssertions(t *testing.T) {
	scripts := exportPostmanTests([]headerEntry{
		{key: "status", value: "200, 201"}, {key: "status.class", value: "2xx"},
		{key: "header.Content-Type", value: "*"}, {key: "body.contains", value: "ok"},
		{key: "body.matches", value: "^ok"}, {key: "json.items[0].id", value: "42"},
		{key: "time.lt", value: "500ms"}, {key: "size.lt", value: "2KiB"},
	})
	joined := strings.Join(scripts, "\n")
	for _, expected := range []string{"pm.response.code", "response.headers.has", "response.text()", "new RegExp", `["items"][0]["id"]`, "responseTime", "responseSize"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("exported assertions missing %q:\n%s", expected, joined)
		}
	}
}

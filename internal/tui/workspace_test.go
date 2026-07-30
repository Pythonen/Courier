package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uuid "github.com/google/uuid"
)

func TestWorkspaceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.variablesInput.SetEntries([]headerEntry{{key: "baseUrl", value: "https://api.example.test"}, {key: "token", value: "secret"}})
	m.settings.SetConfig(requestSettings{
		followRedirects: false,
		httpVersion:     httpVersion2,
		skipTLSVerify:   true,
		timeout:         17 * time.Second,
		proxyURL:        "socks5h://proxy.example.test:1080",
		proxyBypass:     "localhost,.internal.example",
		caCertPath:      "{{certDir}}/root-ca.pem",
		clientCertPath:  "{{certDir}}/client.pem",
		clientKeyPath:   "{{certDir}}/client-key.pem",
	})
	m.methodIdx = methodIndex(t, "PATCH")
	m.urlInput.SetValue("{{baseUrl}}/users/1")
	m.headersInput.SetEntries([]headerEntry{{key: "X-Test", value: "yes"}})
	m.paramsInput.SetEntries([]headerEntry{{key: "expand", value: "profile"}})
	m.cookiesInput.SetEntries([]headerEntry{{key: "session", value: "{{token}}"}})
	m.testsInput.SetEntries([]headerEntry{{key: "status", value: "200"}, {key: "json.user.id", value: "42"}})
	m.authInput.SetConfig(authConfig{
		typeID: authAPIKey, apiKeyName: "api_key", apiKeyValue: "{{token}}", apiKeyLocation: apiKeyQuery,
	})
	m.bodyMode = bodyMultipart
	m.multipartInput.SetEntries([]headerEntry{{key: "description", value: "saved request"}, {key: "file", value: "@{{fixture}}"}})
	m.savedRequests = append(m.savedRequests, m.captureCurrentRequest())
	historyID := uuid.New()
	m.history = []historyItem{{
		createdAt: time.Date(2026, time.July, 21, 12, 0, 0, 123, time.UTC), method: "POST", url: "{{baseUrl}}/history",
		requestHeaders: []headerEntry{{key: "X-History", value: "yes"}}, requestParams: []headerEntry{{key: "page", value: "3"}},
		requestAuth:       authConfig{typeID: authBearer, bearerToken: "historic-token"},
		requestBodyConfig: bodyConfig{mode: bodyRaw, rawType: rawJSON, raw: `{"history":true}`},
		requestCookies:    []headerEntry{{key: "historic", value: "cookie"}}, requestTests: []headerEntry{{key: "status", value: "200"}},
		responseBody: "formatted response", responseRaw: `{"ok":true}`, responseRawAvailable: true,
		responseHeaders: "Content-Type: application/json", responseMeta: "200 OK • 12ms • 11 B", responseTests: "1/1 assertions passed", requestID: historyID,
	}}

	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("workspace permissions = %o, want 600", got)
	}

	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	variables := loaded.variablesInput.Entries()
	if len(variables) != 2 || variables[0].key != "baseUrl" || variables[1].value != "secret" {
		t.Fatalf("loaded environment = %#v", variables)
	}
	if len(loaded.savedRequests) != 1 {
		t.Fatalf("loaded requests = %d", len(loaded.savedRequests))
	}
	request := loaded.savedRequests[0]
	if request.method != "PATCH" || request.url != "{{baseUrl}}/users/1" {
		t.Fatalf("loaded target = %s %s", request.method, request.url)
	}
	if request.auth.typeID != authAPIKey || request.auth.apiKeyLocation != apiKeyQuery || request.auth.apiKeyValue != "{{token}}" {
		t.Fatalf("loaded auth = %#v", request.auth)
	}
	if request.body.mode != bodyMultipart || len(request.body.multipart) != 2 || request.body.multipart[1].value != "@{{fixture}}" {
		t.Fatalf("loaded body = %#v", request.body)
	}
	if len(request.headers) != 1 || len(request.params) != 1 || len(request.cookies) != 1 || len(request.tests) != 2 {
		t.Fatalf("loaded request components = headers %#v params %#v cookies %#v tests %#v", request.headers, request.params, request.cookies, request.tests)
	}
	if loaded.settings.config.followRedirects || loaded.settings.config.httpVersion != httpVersion2 || !loaded.settings.config.skipTLSVerify || loaded.settings.config.timeout != 17*time.Second || loaded.settings.config.proxyURL != "socks5h://proxy.example.test:1080" || loaded.settings.config.proxyBypass != "localhost,.internal.example" || loaded.settings.config.caCertPath != "{{certDir}}/root-ca.pem" || loaded.settings.config.clientCertPath != "{{certDir}}/client.pem" || loaded.settings.config.clientKeyPath != "{{certDir}}/client-key.pem" {
		t.Fatalf("loaded settings = %#v", loaded.settings.config)
	}
	if loaded.settings.caCertInput.Value() != "{{certDir}}/root-ca.pem" || loaded.settings.clientCertInput.Value() != "{{certDir}}/client.pem" || loaded.settings.clientKeyInput.Value() != "{{certDir}}/client-key.pem" {
		t.Fatalf("loaded TLS inputs = CA %q cert %q key %q", loaded.settings.caCertInput.Value(), loaded.settings.clientCertInput.Value(), loaded.settings.clientKeyInput.Value())
	}
	if len(loaded.history) != 1 {
		t.Fatalf("loaded history = %#v", loaded.history)
	}
	history := loaded.history[0]
	if history.requestID != historyID || history.method != "POST" || history.url != "{{baseUrl}}/history" || history.createdAt.Nanosecond() != 123 || history.requestAuth.bearerToken != "historic-token" || history.requestBodyConfig.raw != `{"history":true}` || history.responseRaw != `{"ok":true}` || history.responseMeta != "200 OK • 12ms • 11 B" {
		t.Fatalf("loaded history snapshot = %#v", history)
	}
}

func TestNamedEnvironmentsRoundTripAndLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.variablesInput.SetEntries([]headerEntry{{key: "host", value: "dev.example.test"}})
	m.syncActiveEnvironment()
	m.environments = append(m.environments, environmentProfile{name: "Production", values: []headerEntry{{key: "host", value: "api.example.test"}}})
	m.activateEnvironmentIndex(1)
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if len(loaded.environments) != 2 || loaded.activeEnvironmentName() != "Production" || loaded.variablesInput.Entries()[0].value != "api.example.test" {
		t.Fatalf("loaded environments = %#v, active=%q", loaded.environments, loaded.activeEnvironmentName())
	}
	if err := loaded.ActivateEnvironment("Default"); err != nil || loaded.variablesInput.Entries()[0].value != "dev.example.test" {
		t.Fatalf("activate default: entries=%#v err=%v", loaded.variablesInput.Entries(), err)
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	legacy := `{"version":1,"environment":[{"key":"token","value":"legacy"}]}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyModel := NewModel()
	if err := legacyModel.LoadWorkspace(legacyPath); err != nil {
		t.Fatal(err)
	}
	if len(legacyModel.environments) != 1 || legacyModel.activeEnvironmentName() != defaultEnvironmentName || legacyModel.variablesInput.Entries()[0].value != "legacy" {
		t.Fatalf("legacy migration = %#v", legacyModel.environments)
	}
}

func TestCompletedResponsePersistsHistoryImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.New()
	m.requestId = requestID
	m.history = []historyItem{{
		createdAt: time.Now().UTC(), method: "GET", url: "https://example.test/history",
		requestBodyConfig: bodyConfig{mode: bodyNone}, requestID: requestID,
	}}
	updated, _ := m.Update(responseMsg{
		requestID: requestID, responseBody: "pretty body", responseRaw: "raw body", responseRawAvailable: true,
		responseHeaders: "X-History: yes", responseMeta: "200 OK", assertionResults: []AssertionResult{{Expression: "status", Expected: "200", Actual: "200", Passed: true}},
	})
	m = updated.(model)

	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if len(loaded.history) != 1 {
		t.Fatalf("persisted history length = %d", len(loaded.history))
	}
	item := loaded.history[0]
	if item.responseBody != "pretty body" || item.responseRaw != "raw body" || item.responseHeaders != "X-History: yes" || !strings.Contains(item.responseTests, "1/1 assertions passed") {
		t.Fatalf("persisted response snapshot = %#v", item)
	}
}

func TestWorkspaceHistoryRetentionKeepsNewestEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxHistoryEntries+7; index++ {
		m.history = append([]historyItem{{method: "GET", url: fmt.Sprintf("https://example.test/%03d", index), requestBodyConfig: bodyConfig{mode: bodyNone}, requestID: uuid.New()}}, m.history...)
	}
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if len(loaded.history) != maxHistoryEntries {
		t.Fatalf("retained history length = %d, want %d", len(loaded.history), maxHistoryEntries)
	}
	if loaded.history[0].url != "https://example.test/106" || loaded.history[maxHistoryEntries-1].url != "https://example.test/007" {
		t.Fatalf("history ordering changed: first=%q last=%q", loaded.history[0].url, loaded.history[maxHistoryEntries-1].url)
	}
}

func TestHistoryDeletionPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.history = []historyItem{
		{method: "GET", url: "https://example.test/new", requestBodyConfig: bodyConfig{mode: bodyNone}, requestID: uuid.New()},
		{method: "GET", url: "https://example.test/old", requestBodyConfig: bodyConfig{mode: bodyNone}, requestID: uuid.New()},
	}
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	m.sidebarMode = sidebarHistory
	m.historyPos = 0
	m.handleHistoryKeys("d")
	m.handleHistoryKeys("d")
	if len(m.history) != 1 || m.history[0].url != "https://example.test/old" {
		t.Fatalf("history after deletion = %#v", m.history)
	}
	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if len(loaded.history) != 1 || loaded.history[0].url != "https://example.test/old" {
		t.Fatalf("persisted history after deletion = %#v", loaded.history)
	}
}

func TestTrimHistoryCapsSerializedSize(t *testing.T) {
	payload := strings.Repeat("x", 512<<10)
	history := make([]historyItem, 60)
	for index := range history {
		history[index] = historyItem{method: "GET", url: fmt.Sprintf("https://example.test/%d", index), responseBody: payload, requestBodyConfig: bodyConfig{mode: bodyNone}, requestID: uuid.New()}
	}
	trimmed := trimHistory(history)
	if len(trimmed) == 0 || len(trimmed) >= len(history) {
		t.Fatalf("storage retention kept %d of %d entries", len(trimmed), len(history))
	}
	total := 0
	for _, item := range trimmed {
		encoded, err := json.Marshal(item.toWorkspace())
		if err != nil {
			t.Fatal(err)
		}
		total += len(encoded)
	}
	if total > maxHistoryStorageBytes {
		t.Fatalf("serialized retained history = %d bytes, limit %d", total, maxHistoryStorageBytes)
	}
}

func TestWorkspaceRejectsInvalidHistoryMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	contents := `{"version":1,"history":[{"id":"not-a-uuid","request":{"method":"GET","url":"https://example.test","auth":{"type":"none"},"body":{"mode":"none"}}}]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.LoadWorkspace(path); err == nil || !strings.Contains(err.Error(), "invalid request ID") {
		t.Fatalf("invalid history error = %v", err)
	}
}

func TestCollectionControlsSaveAndLoadRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.methodIdx = methodIndex(t, "POST")
	m.urlInput.SetValue("https://api.example.test/items")
	m.bodyInput.SetValue(`{"saved":true}`)
	m.headersInput.SetEntries([]headerEntry{{key: "X-Saved", value: "yes"}})

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	m = updated.(model)
	if len(m.savedRequests) != 1 || m.sidebarMode != sidebarCollections {
		t.Fatalf("save control result = requests %d mode %d", len(m.savedRequests), m.sidebarMode)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("workspace was not persisted: %v", err)
	}

	m.urlInput.SetValue("https://changed.example.test")
	m.headersInput.SetEntries(nil)
	m.setFocus(paneHistory)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if m.urlInput.Value() != "https://api.example.test/items" || methods[m.methodIdx] != "POST" {
		t.Fatalf("loaded saved target = %s %s", methods[m.methodIdx], m.urlInput.Value())
	}
	if headers := m.headersInput.Entries(); len(headers) != 1 || headers[0].value != "yes" {
		t.Fatalf("loaded saved headers = %#v", headers)
	}

	m.setFocus(paneHistory)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(model)
	if len(m.savedRequests) != 0 {
		t.Fatalf("dd left %d saved requests", len(m.savedRequests))
	}
	reloaded := NewModel()
	if err := reloaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.savedRequests) != 0 {
		t.Fatalf("deleted request remained on disk: %d", len(reloaded.savedRequests))
	}
}

func TestWorkspaceRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	if err := os.WriteFile(path, []byte(`{"version":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.LoadWorkspace(path); err == nil {
		t.Fatal("unknown workspace version was accepted")
	}
}

func TestWorkspaceRejectsInvalidSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"settings":{"timeout":"never"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.LoadWorkspace(path); err == nil {
		t.Fatal("invalid workspace timeout was accepted")
	}
}

func TestWorkspaceRoundTripsPFXSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.settings.SetConfig(requestSettings{followRedirects: true, timeout: 3 * time.Second, clientPFXPath: "{{certDir}}/client.pfx", clientPFXPassword: "{{pfxPassword}}"})
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if loaded.settings.config.clientPFXPath != "{{certDir}}/client.pfx" || loaded.settings.config.clientPFXPassword != "{{pfxPassword}}" {
		t.Fatalf("loaded PFX settings = %#v", loaded.settings.config)
	}
}

func TestWorkspaceLoadsNoTimeoutSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"settings":{"timeout":"0s"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if m.settings.config.timeout != 0 {
		t.Fatalf("loaded timeout = %s", m.settings.config.timeout)
	}
}

func TestWorkspaceRoundTripsGraphQLBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.urlInput.SetValue("https://example.test/graphql")
	m.bodyMode = bodyGraphQL
	m.graphqlQueryInput.SetValue("query Viewer { viewer { id } }")
	m.graphqlVariablesInput.SetValue(`{"includeName":true}`)
	m.graphqlOperationInput.SetValue("Viewer")
	m.savedRequests = append(m.savedRequests, m.captureCurrentRequest())
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	body := loaded.savedRequests[0].body
	if body.mode != bodyGraphQL || body.graphqlQuery != "query Viewer { viewer { id } }" || body.graphqlVariables != `{"includeName":true}` || body.graphqlOperationName != "Viewer" {
		t.Fatalf("GraphQL body = %#v", body)
	}
}

func TestWorkspaceRoundTripsOAuth2ClientCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.urlInput.SetValue("https://api.example.test/resource")
	m.authInput.SetConfig(authConfig{
		typeID: authOAuth2ClientCredentials, oauthTokenURL: "{{issuer}}/oauth/token",
		oauthClientID: "{{clientId}}", oauthClientSecret: "{{clientSecret}}", oauthScope: "read write",
	})
	m.savedRequests = append(m.savedRequests, m.captureCurrentRequest())
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	auth := loaded.savedRequests[0].auth
	if auth.typeID != authOAuth2ClientCredentials || auth.oauthTokenURL != "{{issuer}}/oauth/token" || auth.oauthClientID != "{{clientId}}" || auth.oauthClientSecret != "{{clientSecret}}" || auth.oauthScope != "read write" {
		t.Fatalf("OAuth config = %#v", auth)
	}
}

func TestWorkspaceRoundTripsOAuth1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.urlInput.SetValue("https://api.example.test/resource")
	want := authConfig{
		typeID: authOAuth1, oauth1ConsumerKey: "{{consumerKey}}", oauth1ConsumerSecret: "{{consumerSecret}}",
		oauth1Token: "{{token}}", oauth1TokenSecret: "{{tokenSecret}}", oauth1PrivateKey: "{{privateKey}}",
		oauth1SignatureMethod: "RSA-SHA1", oauth1Realm: "example", oauth1Callback: "{{callback}}", oauth1Verifier: "{{verifier}}", oauth1IncludeBodyHash: true, oauth1Location: apiKeyQuery,
	}
	m.authInput.SetConfig(want)
	m.savedRequests = append(m.savedRequests, m.captureCurrentRequest())
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	got := loaded.savedRequests[0].auth
	if got.typeID != want.typeID || got.oauth1ConsumerKey != want.oauth1ConsumerKey || got.oauth1ConsumerSecret != want.oauth1ConsumerSecret ||
		got.oauth1Token != want.oauth1Token || got.oauth1TokenSecret != want.oauth1TokenSecret || got.oauth1PrivateKey != want.oauth1PrivateKey ||
		got.oauth1SignatureMethod != want.oauth1SignatureMethod || got.oauth1Realm != want.oauth1Realm || got.oauth1Callback != want.oauth1Callback ||
		got.oauth1Verifier != want.oauth1Verifier || got.oauth1IncludeBodyHash != want.oauth1IncludeBodyHash || got.oauth1Location != want.oauth1Location {
		t.Fatalf("OAuth 1.0 config = %#v, want OAuth fields from %#v", got, want)
	}
}

func TestWorkspaceRoundTripsDigestAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.urlInput.SetValue("https://api.example.test/private")
	m.authInput.SetConfig(authConfig{typeID: authDigest, username: "{{digestUser}}", password: "{{digestPassword}}"})
	m.savedRequests = append(m.savedRequests, m.captureCurrentRequest())
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	auth := loaded.savedRequests[0].auth
	if auth.typeID != authDigest || auth.username != "{{digestUser}}" || auth.password != "{{digestPassword}}" {
		t.Fatalf("Digest auth = %#v", auth)
	}
}

func TestWorkspaceRoundTripsAWSSignatureV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.urlInput.SetValue("https://api.example.test/private")
	m.authInput.SetConfig(authConfig{
		typeID: authAWSSignatureV4, awsAccessKey: "{{awsAccess}}", awsSecretKey: "{{awsSecret}}",
		awsRegion: "us-east-2", awsService: "execute-api", awsSessionToken: "{{awsToken}}",
	})
	m.savedRequests = append(m.savedRequests, m.captureCurrentRequest())
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded := NewModel()
	if err := loaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	auth := loaded.savedRequests[0].auth
	if auth.typeID != authAWSSignatureV4 || auth.awsAccessKey != "{{awsAccess}}" || auth.awsSecretKey != "{{awsSecret}}" || auth.awsRegion != "us-east-2" || auth.awsService != "execute-api" || auth.awsSessionToken != "{{awsToken}}" {
		t.Fatalf("AWS Signature v4 auth = %#v", auth)
	}
}

func TestCollectionUpdateRenameDuplicateAndActiveIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	m.urlInput.SetValue("https://example.test/original")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	m = updated.(model)
	if len(m.savedRequests) != 1 || m.activeSavedIndex != 0 {
		t.Fatalf("initial save = requests %d active %d", len(m.savedRequests), m.activeSavedIndex)
	}
	originalName := m.savedRequests[0].name
	m.urlInput.SetValue("https://example.test/updated")
	m.headersInput.SetEntries([]headerEntry{{key: "X-Updated", value: "yes"}})
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	m = updated.(model)
	if len(m.savedRequests) != 1 || m.savedRequests[0].url != "https://example.test/updated" || m.savedRequests[0].name != originalName {
		t.Fatalf("update-in-place = %#v", m.savedRequests)
	}

	m.setFocus(paneHistory)
	m.handleHistoryKeys("r")
	if !m.collectionRenameOpen {
		t.Fatal("rename did not open")
	}
	m.collectionRenameInput.SetValue("Users / Updated request")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if m.savedRequests[0].name != "Users / Updated request" || m.collectionRenameOpen {
		t.Fatalf("renamed request = %#v open=%v", m.savedRequests[0], m.collectionRenameOpen)
	}

	m.setFocus(paneHistory)
	m.handleHistoryKeys("c")
	if len(m.savedRequests) != 2 || m.collectionPos != 1 || m.activeSavedIndex != 1 || m.savedRequests[1].name != "Users / Updated request copy" {
		t.Fatalf("duplicate result = requests %#v pos=%d active=%d", m.savedRequests, m.collectionPos, m.activeSavedIndex)
	}
	m.handleHistoryKeys("d")
	m.handleHistoryKeys("d")
	if len(m.savedRequests) != 1 || m.activeSavedIndex != -1 {
		t.Fatalf("delete duplicate = requests %#v active=%d", m.savedRequests, m.activeSavedIndex)
	}

	reloaded := NewModel()
	if err := reloaded.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.savedRequests) != 1 || reloaded.savedRequests[0].name != "Users / Updated request" || reloaded.savedRequests[0].url != "https://example.test/updated" {
		t.Fatalf("persisted collection edits = %#v", reloaded.savedRequests)
	}
}

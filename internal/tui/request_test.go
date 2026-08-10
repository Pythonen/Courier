package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

	var gotMethod, gotPath, gotBody, gotContentType string
	var gotHeaders []string
	var gotQuery url.Values
	var gotAuthorization string
	var gotCookies = map[string]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body failed: %v", err)
		}

		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeaders = r.Header.Values("X-Test")
		gotBody = string(body)
		gotQuery = r.URL.Query()
		gotAuthorization = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		for _, cookie := range r.Cookies() {
			gotCookies[cookie.Name] = cookie.Value
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Server", "teatest")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"name":"courier"}`))
	}))
	defer srv.Close()

	m := NewModel()
	m.methodIdx = methodIndex(t, "POST")
	m.urlInput.SetValue(srv.URL + "/api?existing=yes")
	m.bodyInput.SetValue(`{"from":"test"}`)
	m.headersInput.SetEntries([]headerEntry{
		{key: "X-Test", value: "abc123"},
		{key: "X-Test", value: "second"},
	})
	m.paramsInput.SetEntries([]headerEntry{
		{key: "tag", value: "one"},
		{key: "tag", value: "two"},
	})
	m.authInput.SetConfig(authConfig{typeID: authBearer, bearerToken: "secret-token"})
	m.cookiesInput.SetEntries([]headerEntry{{key: "theme", value: "dark"}})

	msg := m.DoRequest()()
	resp, ok := msg.(responseMsg)
	if !ok {
		t.Fatalf("message type = %T, want responseMsg", msg)
	}
	if !resp.responseRawAvailable || resp.responseRaw != `{"ok":true,"name":"courier"}` {
		t.Fatalf("raw response was not retained: available=%v body=%q", resp.responseRawAvailable, resp.responseRaw)
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
	if gotQuery.Get("existing") != "yes" || strings.Join(gotQuery["tag"], ",") != "one,two" {
		t.Fatalf("server saw query %v", gotQuery)
	}
	if gotAuthorization != "Bearer secret-token" {
		t.Fatalf("server saw Authorization %q", gotAuthorization)
	}
	if gotContentType != "application/json" {
		t.Fatalf("server saw Content-Type %q", gotContentType)
	}
	if gotCookies["theme"] != "dark" {
		t.Fatalf("server saw cookies %v", gotCookies)
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

func TestDoRequest_CookieJarPersistsServerCookies(t *testing.T) {
	t.Parallel()

	var profileCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "jar-token", Path: "/"})
		case "/profile":
			if cookie, err := r.Cookie("session"); err == nil {
				profileCookie = cookie.Value
			}
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	m := NewModel()
	m.bodyMode = bodyNone
	m.urlInput.SetValue(srv.URL + "/login")
	_ = m.DoRequest()()
	m.urlInput.SetValue(srv.URL + "/profile")
	_ = m.DoRequest()()

	if profileCookie != "jar-token" {
		t.Fatalf("profile received session cookie %q", profileCookie)
	}
}

func TestDoRequest_AdditionalMethods(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"HEAD", "OPTIONS", "TRACE", "PROPFIND"} {
		t.Run(method, func(t *testing.T) {
			var gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			m := NewModel()
			m.bodyMode = bodyNone
			m.methodIdx = methodIndex(t, method)
			m.urlInput.SetValue(srv.URL)
			_ = m.DoRequest()()
			if gotMethod != method {
				t.Fatalf("server saw method %q", gotMethod)
			}
		})
	}
}

func TestDoRequest_CustomHTTPMethod(t *testing.T) {
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		seen <- request.Method
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := NewModel()
	m.urlInput.SetValue(server.URL)
	m.setHTTPMethod("PURGE-CACHE")
	response := m.DoRequest()().(responseMsg)
	if response.statusCode != http.StatusNoContent || <-seen != "PURGE-CACHE" {
		t.Fatalf("custom method response = %#v", response)
	}
	request := m.captureCurrentRequest()
	loaded := NewModel()
	loaded.applySavedRequest(request)
	if loaded.displayedMethod() != "PURGE-CACHE" {
		t.Fatalf("restored custom method = %q", loaded.displayedMethod())
	}
}

func TestDoRequest_GraphQLBody(t *testing.T) {
	t.Parallel()
	var received map[string]interface{}
	var contentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode GraphQL body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user":{"id":"42"}}}`))
	}))
	defer server.Close()
	m := NewModel()
	m.methodIdx = methodIndex(t, "POST")
	m.urlInput.SetValue(server.URL)
	m.variablesInput.SetEntries([]headerEntry{{key: "userId", value: "42"}, {key: "operation", value: "GetUser"}})
	m.bodyMode = bodyGraphQL
	m.graphqlQueryInput.SetValue(`query GetUser($id: ID!) { user(id: $id) { id } }`)
	m.graphqlVariablesInput.SetValue(`{"id":"{{userId}}"}`)
	m.graphqlOperationInput.SetValue("{{operation}}")
	response := m.DoRequest()().(responseMsg)
	if response.statusCode != http.StatusOK {
		t.Fatalf("GraphQL response = %#v", response)
	}
	if contentType != "application/json" || received["operationName"] != "GetUser" || received["query"] != `query GetUser($id: ID!) { user(id: $id) { id } }` {
		t.Fatalf("GraphQL envelope = %#v content-type=%q", received, contentType)
	}
	variables, ok := received["variables"].(map[string]interface{})
	if !ok || variables["id"] != "42" {
		t.Fatalf("GraphQL variables = %#v", received["variables"])
	}
}

func TestBuildGraphQLBodyValidation(t *testing.T) {
	m := NewModel()
	m.bodyMode = bodyGraphQL
	resolver := newVariableResolver(nil)
	if _, _, _, err := m.buildRequestBody(resolver); err == nil || !strings.Contains(err.Error(), "requires a query") {
		t.Fatalf("empty GraphQL query error = %v", err)
	}
	m.graphqlQueryInput.SetValue("query { viewer { id } }")
	m.graphqlVariablesInput.SetValue("not-json")
	if _, _, _, err := m.buildRequestBody(resolver); err == nil || !strings.Contains(err.Error(), "GraphQL variables") {
		t.Fatalf("invalid GraphQL variables error = %v", err)
	}
}

func TestDoRequest_ResolvesEnvironmentVariables(t *testing.T) {
	t.Parallel()

	var gotPath, gotQuery, gotHeader, gotAuthorization, gotCookie string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("search")
		gotHeader = r.Header.Get("X-Courier")
		gotAuthorization = r.Header.Get("Authorization")
		if cookie, err := r.Cookie("session"); err == nil {
			gotCookie = cookie.Value
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	m := NewModel()
	m.methodIdx = methodIndex(t, "POST")
	m.variablesInput.SetEntries([]headerEntry{
		{key: "baseUrl", value: srv.URL},
		{key: "resource", value: "users"},
		{key: "term", value: "terminal client"},
		{key: "token", value: "environment-token"},
	})
	m.urlInput.SetValue("{{baseUrl}}/{{resource}}")
	m.paramsInput.SetEntries([]headerEntry{{key: "search", value: "{{term}}"}})
	m.headersInput.SetEntries([]headerEntry{{key: "X-Courier", value: "{{$guid}}"}})
	m.authInput.SetConfig(authConfig{typeID: authBearer, bearerToken: "{{token}}"})
	m.cookiesInput.SetEntries([]headerEntry{{key: "session", value: "{{token}}"}})
	m.bodyMode = bodyRaw
	m.rawBodyType = rawJSON
	m.bodyInput.SetValue(`{"term":"{{term}}","request_id":"{{$guid}}","unknown":"{{missing}}"}`)

	response := m.DoRequest()().(responseMsg)
	if strings.HasPrefix(response.responseBody, "Error") {
		t.Fatalf("request failed: %s", response.responseBody)
	}
	if gotPath != "/users" || gotQuery != "terminal client" {
		t.Fatalf("resolved target = path %q query %q", gotPath, gotQuery)
	}
	if gotAuthorization != "Bearer environment-token" || gotCookie != "environment-token" {
		t.Fatalf("resolved credentials = auth %q cookie %q", gotAuthorization, gotCookie)
	}
	if gotHeader == "" || gotBody["request_id"] != gotHeader {
		t.Fatalf("dynamic GUID was not stable across fields: header %q body %q", gotHeader, gotBody["request_id"])
	}
	if gotBody["term"] != "terminal client" || gotBody["unknown"] != "{{missing}}" {
		t.Fatalf("resolved body = %#v", gotBody)
	}
}

func TestDoRequest_AuthModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		config         authConfig
		wantHeaderName string
		wantHeader     string
		wantQueryName  string
		wantQuery      string
		wantBasicUser  string
		wantBasicPass  string
	}{
		{
			name:          "basic",
			config:        authConfig{typeID: authBasic, username: "courier", password: "s3cret"},
			wantBasicUser: "courier",
			wantBasicPass: "s3cret",
		},
		{
			name:           "api key header",
			config:         authConfig{typeID: authAPIKey, apiKeyName: "X-API-Key", apiKeyValue: "header-secret"},
			wantHeaderName: "X-API-Key",
			wantHeader:     "header-secret",
		},
		{
			name: "api key query",
			config: authConfig{
				typeID: authAPIKey, apiKeyName: "api_key", apiKeyValue: "query-secret", apiKeyLocation: apiKeyQuery,
			},
			wantQueryName: "api_key",
			wantQuery:     "query-secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotHeader, gotQuery, gotBasicUser, gotBasicPass string
			var gotBasic bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.wantHeaderName != "" {
					gotHeader = r.Header.Get(tt.wantHeaderName)
				}
				if tt.wantQueryName != "" {
					gotQuery = r.URL.Query().Get(tt.wantQueryName)
				}
				gotBasicUser, gotBasicPass, gotBasic = r.BasicAuth()
				_, _ = w.Write([]byte("ok"))
			}))
			defer srv.Close()

			m := NewModel()
			m.urlInput.SetValue(srv.URL)
			m.authInput.SetConfig(tt.config)
			_ = m.DoRequest()()
			if gotHeader != tt.wantHeader {
				t.Fatalf("header value = %q, want %q", gotHeader, tt.wantHeader)
			}
			if gotQuery != tt.wantQuery {
				t.Fatalf("query value = %q, want %q", gotQuery, tt.wantQuery)
			}
			if tt.wantBasicUser != "" && (!gotBasic || gotBasicUser != tt.wantBasicUser || gotBasicPass != tt.wantBasicPass) {
				t.Fatalf("BasicAuth = %q/%q/%v", gotBasicUser, gotBasicPass, gotBasic)
			}
		})
	}
}

func TestDoRequest_BodyModes(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "payload.txt")
	if err := os.WriteFile(filePath, []byte("file contents"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name            string
		configure       func(*model)
		wantContentType string
		wantBody        string
		multipart       bool
	}{
		{
			name: "raw XML",
			configure: func(m *model) {
				m.bodyMode = bodyRaw
				m.rawBodyType = rawXML
				m.bodyInput.SetValue("<hello>world</hello>")
			},
			wantContentType: "application/xml",
			wantBody:        "<hello>world</hello>",
		},
		{
			name: "form urlencoded",
			configure: func(m *model) {
				m.bodyMode = bodyFormURLEncoded
				m.formInput.SetEntries([]headerEntry{{key: "tag", value: "one"}, {key: "tag", value: "two"}})
			},
			wantContentType: "application/x-www-form-urlencoded",
			wantBody:        "tag=one&tag=two",
		},
		{
			name: "binary",
			configure: func(m *model) {
				m.bodyMode = bodyBinary
				m.binaryPathInput.SetValue(filePath)
			},
			wantContentType: "application/octet-stream",
			wantBody:        "file contents",
		},
		{
			name: "multipart text and file",
			configure: func(m *model) {
				m.bodyMode = bodyMultipart
				m.multipartInput.SetEntries([]headerEntry{
					{key: "description", value: "courier upload"},
					{key: "document", value: "@" + filePath},
				})
			},
			wantContentType: "multipart/form-data; boundary=",
			multipart:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotContentType, gotBody, gotField, gotFile, gotFilename string
			var parseErr error
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotContentType = r.Header.Get("Content-Type")
				if tt.multipart {
					parseErr = r.ParseMultipartForm(1 << 20)
					if parseErr == nil {
						gotField = r.FormValue("description")
						file, header, fileErr := r.FormFile("document")
						if fileErr != nil {
							parseErr = fileErr
						} else {
							gotFilename = header.Filename
							data, readErr := io.ReadAll(file)
							_ = file.Close()
							if readErr != nil {
								parseErr = readErr
							} else {
								gotFile = string(data)
							}
						}
					}
				} else {
					data, _ := io.ReadAll(r.Body)
					gotBody = string(data)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer srv.Close()

			m := NewModel()
			m.methodIdx = methodIndex(t, "POST")
			m.urlInput.SetValue(srv.URL)
			tt.configure(&m)
			response := m.DoRequest()().(responseMsg)
			if strings.HasPrefix(response.responseBody, "Error") {
				t.Fatalf("request failed: %s", response.responseBody)
			}
			if !strings.HasPrefix(gotContentType, tt.wantContentType) {
				t.Fatalf("Content-Type = %q, want prefix %q", gotContentType, tt.wantContentType)
			}
			if tt.multipart {
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				if gotField != "courier upload" || gotFile != "file contents" || gotFilename != "payload.txt" {
					t.Fatalf("multipart values = field %q file %q filename %q", gotField, gotFile, gotFilename)
				}
			} else if gotBody != tt.wantBody {
				t.Fatalf("body = %q, want %q", gotBody, tt.wantBody)
			}
		})
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
	m.settings.config.timeout = 10 * time.Millisecond
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

	body := strings.Repeat("x", maxResponseBody) + "tail=needle"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	m := NewModel()
	m.urlInput.SetValue(srv.URL)
	m.testsInput.SetEntries([]headerEntry{
		{key: "body.contains", value: "tail=needle"},
		{key: "set.tail", value: `body.matches:tail=(\w+)$`},
		{key: "size.lt", value: strconv.Itoa(len(body) + 1)},
	})

	resp := m.DoRequest()().(responseMsg)
	if !strings.Contains(resp.responseBody, "exceeds the 10.0 MiB display limit") {
		t.Fatalf("oversized response was not rejected, got: %q", resp.responseBody)
	}
	if resp.responseBytes != len(body) {
		t.Fatalf("response size = %d, want %d", resp.responseBytes, len(body))
	}
	for _, result := range resp.assertionResults {
		if !result.Passed {
			t.Errorf("assertion against complete oversized response failed: %#v", result)
		}
	}
	if len(resp.variableUpdates) != 1 || resp.variableUpdates[0] != (headerEntry{key: "tail", value: "needle"}) {
		t.Fatalf("oversized response extraction updates = %#v", resp.variableUpdates)
	}
}

func TestReadResponseBodyForAssertionsDrainsAndCountsBeyondLimit(t *testing.T) {
	reader := strings.NewReader("0123456789")
	body, size, truncated, err := readResponseBodyForAssertions(reader, 4)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(body) != "0123" || size != 10 || !truncated {
		t.Fatalf("body = %q, size = %d, truncated = %t", body, size, truncated)
	}
	if reader.Len() != 0 {
		t.Fatalf("reader retained %d bytes after drain", reader.Len())
	}
}

func TestDoRequest_RedirectSetting(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("final response"))
	}))
	defer srv.Close()

	m := NewModel()
	m.bodyMode = bodyNone
	m.urlInput.SetValue(srv.URL + "/start")
	m.settings.config.followRedirects = false
	response := m.DoRequest()().(responseMsg)
	if !strings.Contains(response.responseMeta, "302 Found") {
		t.Fatalf("redirect-disabled metadata = %q", response.responseMeta)
	}

	m.settings.config.followRedirects = true
	response = m.DoRequest()().(responseMsg)
	if !strings.Contains(stripANSI(response.responseBody), "final response") || !strings.Contains(response.responseMeta, "/final") {
		t.Fatalf("redirect-followed response = body %q meta %q", response.responseBody, response.responseMeta)
	}
}

func TestDoRequest_TLSVerificationSetting(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secure response"))
	}))
	defer srv.Close()

	m := NewModel()
	m.bodyMode = bodyNone
	m.urlInput.SetValue(srv.URL)
	failed := m.DoRequest()().(responseMsg)
	if !strings.Contains(failed.responseMeta, "Request failed") {
		t.Fatalf("untrusted TLS request unexpectedly succeeded: %q", failed.responseMeta)
	}

	m.settings.config.skipTLSVerify = true
	succeeded := m.DoRequest()().(responseMsg)
	if !strings.Contains(stripANSI(succeeded.responseBody), "secure response") {
		t.Fatalf("TLS override response = %q", succeeded.responseBody)
	}
}

func TestDoRequest_Cancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()

	m := NewModel()
	m.bodyMode = bodyNone
	m.urlInput.SetValue(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	m.requestContext = ctx
	m.cancelRequest = cancel
	result := make(chan tea.Msg, 1)
	go func() { result <- m.DoRequest()() }()
	<-started

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	m = updated.(model)
	response := (<-result).(responseMsg)
	if response.responseBody != "Request cancelled." || !strings.Contains(response.responseMeta, "Cancelled") {
		t.Fatalf("cancellation response = body %q meta %q", response.responseBody, response.responseMeta)
	}
}

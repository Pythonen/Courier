package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestParseCurlCommand(t *testing.T) {
	command := `curl --location --request PATCH 'https://api.example.test/users?expand=profile&tag=go' \
  --header 'Content-Type: application/json' \
  --header 'Authorization: Bearer {{token}}' \
  --header 'X-Client: Courier CLI' \
  --cookie 'session=abc; theme=dark' \
  --data-raw '{"name":"Ada Lovelace"}'`
	request, err := ParseCurlCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	if request.method != "PATCH" || request.url != "https://api.example.test/users" {
		t.Fatalf("target = %s %s", request.method, request.url)
	}
	if len(request.params) != 2 || request.params[0].key != "expand" || request.params[1].value != "go" {
		t.Fatalf("params = %#v", request.params)
	}
	if request.auth.typeID != authBearer || request.auth.bearerToken != "{{token}}" {
		t.Fatalf("auth = %#v", request.auth)
	}
	if len(request.headers) != 2 || request.headers[1].key != "X-Client" || request.headers[1].value != "Courier CLI" {
		t.Fatalf("headers = %#v", request.headers)
	}
	if len(request.cookies) != 2 || request.cookies[1].value != "dark" {
		t.Fatalf("cookies = %#v", request.cookies)
	}
	if request.body.mode != bodyRaw || request.body.rawType != rawJSON || request.body.raw != `{"name":"Ada Lovelace"}` {
		t.Fatalf("body = %#v", request.body)
	}
}

func TestParseCurlFormsAndMethodInference(t *testing.T) {
	tests := []struct {
		name    string
		command string
		assert  func(*testing.T, savedRequest)
	}{
		{
			name:    "url encoded post",
			command: `curl https://example.test/token --data-urlencode 'grant_type=client credentials' --data-urlencode client_id=abc`,
			assert: func(t *testing.T, request savedRequest) {
				if request.method != "POST" || request.body.mode != bodyFormURLEncoded || request.body.form[0].value != "client credentials" {
					t.Fatalf("request = %#v", request)
				}
			},
		},
		{
			name:    "multipart",
			command: `curl -F note=hello -F 'file=@/tmp/my file.txt' https://example.test/upload`,
			assert: func(t *testing.T, request savedRequest) {
				if request.body.mode != bodyMultipart || request.body.multipart[1].value != "@/tmp/my file.txt" {
					t.Fatalf("body = %#v", request.body)
				}
			},
		},
		{
			name:    "get data becomes query",
			command: `curl -G https://example.test/search --data-urlencode 'q=terminal client'`,
			assert: func(t *testing.T, request savedRequest) {
				if request.method != "GET" || request.body.mode != bodyNone || len(request.params) != 1 || request.params[0].value != "terminal client" {
					t.Fatalf("request = %#v", request)
				}
			},
		},
		{
			name:    "binary and basic auth",
			command: `curl -XPUT -u alice:secret --data-binary @archive.zip https://example.test/archive`,
			assert: func(t *testing.T, request savedRequest) {
				if request.method != "PUT" || request.auth.typeID != authBasic || request.body.mode != bodyBinary || request.body.binaryPath != "archive.zip" {
					t.Fatalf("request = %#v", request)
				}
			},
		},
		{
			name:    "json convenience option",
			command: `curl --json '{"ok":true}' https://example.test/events`,
			assert: func(t *testing.T, request savedRequest) {
				if request.method != "POST" || request.body.mode != bodyRaw || request.body.rawType != rawJSON || len(request.headers) != 2 {
					t.Fatalf("request = %#v", request)
				}
			},
		},
		{
			name:    "upload file",
			command: `curl --upload-file ./artifact.tgz https://example.test/artifact`,
			assert: func(t *testing.T, request savedRequest) {
				if request.method != "PUT" || request.body.mode != bodyBinary || request.body.binaryPath != "./artifact.tgz" {
					t.Fatalf("request = %#v", request)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := ParseCurlCommand(test.command)
			if err != nil {
				t.Fatal(err)
			}
			test.assert(t, request)
		})
	}
}

func TestCurlCommandRoundTrip(t *testing.T) {
	original := savedRequest{
		method:  "POST",
		url:     "{{baseUrl}}/users",
		headers: []headerEntry{{key: "X-Name", value: "O'Reilly"}},
		params:  []headerEntry{{key: "include", value: "profile details"}},
		auth:    authConfig{typeID: authBearer, bearerToken: "{{token}}"},
		cookies: []headerEntry{{key: "session", value: "a b"}},
		body:    bodyConfig{mode: bodyRaw, rawType: rawJSON, raw: `{"name":"Ada"}`},
	}
	command := CurlCommand(original)
	if !strings.Contains(command, `O'"'"'Reilly`) {
		t.Fatalf("single quote was not shell-escaped: %s", command)
	}
	parsed, err := ParseCurlCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.method != original.method || parsed.url != original.url || parsed.auth != original.auth || parsed.body.mode != original.body.mode || parsed.body.rawType != original.body.rawType || parsed.body.raw != original.body.raw {
		t.Fatalf("round trip mismatch\ncommand: %s\nparsed: %#v", command, parsed)
	}
	if len(parsed.params) != 1 || parsed.params[0] != original.params[0] || len(parsed.cookies) != 1 || parsed.cookies[0] != original.cookies[0] {
		t.Fatalf("round trip collections mismatch: params %#v cookies %#v", parsed.params, parsed.cookies)
	}
}

func TestCurlCommandPlacesQueryBeforeURLFragment(t *testing.T) {
	request := savedRequest{
		method: "GET",
		url:    "https://api.example.test/items?fixed=yes#details",
		params: []headerEntry{{key: "page", value: "2"}},
		auth:   authConfig{typeID: authAPIKey, apiKeyName: "api_key", apiKeyValue: "secret", apiKeyLocation: apiKeyQuery},
	}

	command := CurlCommand(request)
	if !strings.Contains(command, "--url 'https://api.example.test/items?fixed=yes&page=2&api_key=secret#details'") {
		t.Fatalf("cURL query parameters were placed after the fragment: %s", command)
	}
}

func TestParseCurlCommandRejectsUnsafeOrIncompleteInput(t *testing.T) {
	for _, command := range []string{"", "curl --request", "curl https://example.test ';' echo nope", "curl -X 'BAD METHOD' https://example.test"} {
		if _, err := ParseCurlCommand(command); err == nil {
			t.Fatalf("accepted %q", command)
		}
	}
}

func TestParseCurlCommandAcceptsCustomMethod(t *testing.T) {
	request, err := ParseCurlCommand("curl -X PURGE-CACHE https://example.test/cache")
	if err != nil {
		t.Fatal(err)
	}
	if request.method != "PURGE-CACHE" {
		t.Fatalf("custom cURL method = %q", request.method)
	}
}

func TestCurrentRequestCurlShortcut(t *testing.T) {
	m := NewModel()
	m.urlInput.SetValue("https://example.test")
	m.methodIdx = methodIndex(t, "GET")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated.(model)
	if m.responseMeta != "cURL command generated" || !strings.HasPrefix(m.response, "curl ") || m.focus != paneResponse {
		t.Fatalf("shortcut result = meta %q response %q focus %d", m.responseMeta, m.response, m.focus)
	}
}

func TestCurlCommandExportsGraphQLBody(t *testing.T) {
	request := savedRequest{
		method: "POST", url: "https://example.test/graphql",
		body: bodyConfig{mode: bodyGraphQL, graphqlQuery: "query Viewer { viewer { id } }", graphqlVariables: `{"active":true}`, graphqlOperationName: "Viewer"},
	}
	command := CurlCommand(request)
	if !strings.Contains(command, "Content-Type: application/json") || !strings.Contains(command, `"operationName":"Viewer"`) || !strings.Contains(command, `"variables":{"active":true}`) {
		t.Fatalf("GraphQL cURL = %s", command)
	}
}

func TestCurlCommandMarksOAuth2AccessTokenPlaceholder(t *testing.T) {
	request := savedRequest{method: "GET", url: "https://example.test", auth: authConfig{typeID: authOAuth2ClientCredentials}}
	if command := CurlCommand(request); !strings.Contains(command, "Authorization: Bearer {{oauth2_access_token}}") {
		t.Fatalf("OAuth cURL = %s", command)
	}
}

func TestCurlDigestAuthRoundTrip(t *testing.T) {
	request, err := ParseCurlCommand(`curl --digest --user '{{digestUser}}:{{digestPassword}}' https://example.test/private`)
	if err != nil {
		t.Fatal(err)
	}
	if request.auth.typeID != authDigest || request.auth.username != "{{digestUser}}" || request.auth.password != "{{digestPassword}}" {
		t.Fatalf("imported Digest auth = %#v", request.auth)
	}
	command := CurlCommand(request)
	if !strings.Contains(command, "--digest") || !strings.Contains(command, "{{digestUser}}:{{digestPassword}}") {
		t.Fatalf("exported Digest cURL = %q", command)
	}
}

func TestCurlAWSSignatureV4RoundTrip(t *testing.T) {
	request, err := ParseCurlCommand(`curl --aws-sigv4 'aws:amz:us-east-2:execute-api' --user '{{awsAccess}}:{{awsSecret}}' --header 'X-Amz-Security-Token: {{awsToken}}' https://example.test/private`)
	if err != nil {
		t.Fatal(err)
	}
	auth := request.auth
	if auth.typeID != authAWSSignatureV4 || auth.awsAccessKey != "{{awsAccess}}" || auth.awsSecretKey != "{{awsSecret}}" || auth.awsRegion != "us-east-2" || auth.awsService != "execute-api" || auth.awsSessionToken != "{{awsToken}}" {
		t.Fatalf("imported AWS Signature v4 auth = %#v", auth)
	}
	command := CurlCommand(request)
	for _, expected := range []string{"--aws-sigv4", "aws:amz:us-east-2:execute-api", "{{awsAccess}}:{{awsSecret}}", "X-Amz-Security-Token: {{awsToken}}"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("exported AWS cURL missing %q: %s", expected, command)
		}
	}
}

func TestCurlUnixSocketRoundTrip(t *testing.T) {
	request, err := ParseCurlCommand(`curl --unix-socket /var/run/docker.sock 'http://localhost/containers/json?all=1'`)
	if err != nil {
		t.Fatal(err)
	}
	if request.url != "http://unix:/var/run/docker.sock:/containers/json" || len(request.params) != 1 || request.params[0] != (headerEntry{key: "all", value: "1"}) {
		t.Fatalf("imported Unix socket cURL = %#v", request)
	}
	command := CurlCommand(request)
	if !strings.Contains(command, "--unix-socket /var/run/docker.sock") || !strings.Contains(command, "http://localhost/containers/json?all=1") {
		t.Fatalf("exported Unix socket cURL = %s", command)
	}
	roundTrip, err := ParseCurlCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.url != request.url || len(roundTrip.params) != 1 || roundTrip.params[0] != request.params[0] {
		t.Fatalf("Unix socket cURL round trip = %#v", roundTrip)
	}
}

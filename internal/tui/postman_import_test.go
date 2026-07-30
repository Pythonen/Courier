package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPostmanCollection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collection.json")
	data := `{
  "info": {"name": "Example", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable": [{"key": "baseUrl", "value": "https://example.test"}],
  "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "{{token}}"}]},
  "item": [{
    "name": "Users",
    "item": [{
      "name": "Create user",
      "request": {
        "method": "POST",
        "header": [{"key": "X-Client", "value": "courier"}, {"key": "Disabled", "value": "no", "disabled": true}],
        "url": {"raw": "{{baseUrl}}/users?expand=profile", "query": [{"key": "expand", "value": "profile"}]},
        "body": {"mode": "raw", "raw": "{\"name\":\"Ada\"}", "options": {"raw": {"language": "json"}}}
      }
    }]
  }, {
    "name": "Upload",
    "request": {
      "method": "PUT",
      "auth": {"type": "apikey", "apikey": [{"key": "key", "value": "api_key"}, {"key": "value", "value": "secret"}, {"key": "in", "value": "query"}]},
      "url": "https://example.test/upload?dry_run=true",
      "body": {"mode": "formdata", "formdata": [{"key": "note", "value": "hello"}, {"key": "file", "type": "file", "src": "/tmp/file.txt"}]}
    }
  }]}
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	count, err := m.ImportPostmanCollection(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(m.savedRequests) != 2 {
		t.Fatalf("imported %d requests, stored %d", count, len(m.savedRequests))
	}
	variables := m.variablesInput.Entries()
	if len(variables) != 1 || variables[0].key != "baseUrl" || variables[0].value != "https://example.test" {
		t.Fatalf("collection variables = %#v", variables)
	}
	create := m.savedRequests[0]
	if create.name != "Users / Create user" || create.method != "POST" || create.url != "{{baseUrl}}/users" {
		t.Fatalf("create request target = %#v", create)
	}
	if create.auth.typeID != authBearer || create.auth.bearerToken != "{{token}}" {
		t.Fatalf("inherited auth = %#v", create.auth)
	}
	if len(create.headers) != 1 || len(create.params) != 1 || create.body.mode != bodyRaw || create.body.rawType != rawJSON {
		t.Fatalf("create request components = %#v", create)
	}
	upload := m.savedRequests[1]
	if upload.url != "https://example.test/upload" || len(upload.params) != 1 || upload.params[0].key != "dry_run" {
		t.Fatalf("upload URL/query = %q %#v", upload.url, upload.params)
	}
	if upload.auth.typeID != authAPIKey || upload.auth.apiKeyLocation != apiKeyQuery {
		t.Fatalf("upload auth = %#v", upload.auth)
	}
	if upload.body.mode != bodyMultipart || len(upload.body.multipart) != 2 || upload.body.multipart[1].value != "@/tmp/file.txt" {
		t.Fatalf("upload body = %#v", upload.body)
	}
}

func TestImportPostmanEnvironmentMergesEnabledValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment.json")
	data := `{"values":[
  {"key":"baseUrl","value":"https://new.example.test","enabled":true},
  {"key":"count","value":12,"enabled":true},
  {"key":"disabled","value":"skip","enabled":false}
]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.variablesInput.SetEntries([]headerEntry{{key: "baseUrl", value: "https://old.example.test"}})
	count, err := m.ImportPostmanEnvironment(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("imported count = %d", count)
	}
	entries := m.variablesInput.Entries()
	if len(entries) != 2 || entries[0].value != "https://new.example.test" || entries[1].key != "count" || entries[1].value != "12" {
		t.Fatalf("merged environment = %#v", entries)
	}
}

func TestImportPostmanRejectsInvalidFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`{"info":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if _, err := m.ImportPostmanCollection(path); err == nil {
		t.Fatal("empty collection was accepted")
	}
}

func TestPostmanGraphQLBodyConversion(t *testing.T) {
	body := postmanBody{Mode: "graphql", GraphQL: json.RawMessage(`{
  "query":"query User($id: ID!) { user(id: $id) { id } }",
  "variables":"{\"id\":\"42\"}",
  "operationName":"User"
}`)}
	config := postmanBodyConfig(body)
	if config.mode != bodyGraphQL || config.graphqlOperationName != "User" || config.graphqlVariables != `{"id":"42"}` || !strings.Contains(config.graphqlQuery, "query User") {
		t.Fatalf("GraphQL config = %#v", config)
	}

	body.GraphQL = json.RawMessage(`{"query":"query { viewer { id } }","variables":{"enabled":true}}`)
	config = postmanBodyConfig(body)
	if config.graphqlVariables != `{"enabled":true}` {
		t.Fatalf("object GraphQL variables = %q", config.graphqlVariables)
	}
}

func TestPostmanOAuth2Conversion(t *testing.T) {
	auth := postmanAuth{Type: "oauth2", OAuth2: []postmanKV{
		{Key: "accessTokenUrl", Value: "{{issuer}}/token"},
		{Key: "clientId", Value: "{{clientId}}"},
		{Key: "clientSecret", Value: "{{secret}}"},
		{Key: "scope", Value: "read write"},
	}}
	config := postmanAuthConfig(&auth)
	if config.typeID != authOAuth2ClientCredentials || config.oauthTokenURL != "{{issuer}}/token" || config.oauthClientID != "{{clientId}}" || config.oauthClientSecret != "{{secret}}" || config.oauthScope != "read write" {
		t.Fatalf("OAuth client credentials config = %#v", config)
	}
	auth.OAuth2 = []postmanKV{{Key: "accessToken", Value: "existing-token"}}
	config = postmanAuthConfig(&auth)
	if config.typeID != authBearer || config.bearerToken != "existing-token" {
		t.Fatalf("OAuth access token fallback = %#v", config)
	}
}

func TestPostmanDigestAuthMapping(t *testing.T) {
	config := postmanAuthConfig(&postmanAuth{Type: "digest", Digest: []postmanKV{
		{Key: "username", Value: "{{digestUser}}"},
		{Key: "password", Value: "{{digestPassword}}"},
	}})
	if config.typeID != authDigest || config.username != "{{digestUser}}" || config.password != "{{digestPassword}}" {
		t.Fatalf("Digest auth config = %#v", config)
	}
}

func TestPostmanAWSSignatureV4Mapping(t *testing.T) {
	config := postmanAuthConfig(&postmanAuth{Type: "awsv4", AWSV4: []postmanKV{
		{Key: "accessKey", Value: "{{awsAccess}}"}, {Key: "secretKey", Value: "{{awsSecret}}"},
		{Key: "region", Value: "us-west-2"}, {Key: "service", Value: "execute-api"}, {Key: "sessionToken", Value: "{{awsToken}}"},
	}})
	if config.typeID != authAWSSignatureV4 || config.awsAccessKey != "{{awsAccess}}" || config.awsSecretKey != "{{awsSecret}}" || config.awsRegion != "us-west-2" || config.awsService != "execute-api" || config.awsSessionToken != "{{awsToken}}" {
		t.Fatalf("AWS Signature v4 config = %#v", config)
	}
}

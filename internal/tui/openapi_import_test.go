package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportOpenAPIYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.yaml")
	document := `openapi: 3.1.0
info:
  title: Example API
  version: 1.0.0
servers:
  - url: https://{host}/v1
    variables:
      host:
        default: api.example.test
security:
  - ApiToken: []
paths:
  /login:
    get:
      summary: Login status
      security:
        - BasicAuth: []
      responses:
        "200": {description: OK}
  /upload:
    post:
      tags: [Files]
      summary: Upload file
      requestBody:
        content:
          multipart/form-data:
            schema:
              type: object
              properties:
                file: {type: string, format: binary}
                note: {type: string, default: hello}
      responses:
        "201": {description: Created}
  /users/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema: {type: integer, example: 42}
    get:
      tags: [Users]
      summary: Get user
      parameters:
        - name: expand
          in: query
          schema: {type: string, default: profile}
        - $ref: '#/components/parameters/TraceHeader'
        - name: locale
          in: cookie
          example: en
      responses:
        "200": {description: OK}
    post:
      operationId: updateUser
      requestBody:
        $ref: '#/components/requestBodies/UserInput'
      responses:
        "200": {description: OK}
components:
  securitySchemes:
    ApiToken:
      type: apiKey
      in: header
      name: X-API-Key
    BasicAuth:
      type: http
      scheme: basic
  parameters:
    TraceHeader:
      name: X-Trace
      in: header
      example: trace-123
  requestBodies:
    UserInput:
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/UserInput'
  schemas:
    UserInput:
      type: object
      properties:
        active: {type: boolean}
        name: {type: string, example: Ada}
`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.variablesInput.SetEntries([]headerEntry{{key: "host", value: "local.example.test"}})
	count, err := m.ImportOpenAPI(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 || len(m.savedRequests) != 4 {
		t.Fatalf("imported %d requests, stored %d", count, len(m.savedRequests))
	}

	byName := make(map[string]savedRequest, len(m.savedRequests))
	for _, request := range m.savedRequests {
		byName[request.name] = request
	}
	login := byName["Login status"]
	if login.method != "GET" || login.auth.typeID != authBasic || login.auth.username != "{{username}}" {
		t.Fatalf("login request = %#v", login)
	}
	getUser := byName["Users / Get user"]
	if getUser.url != "https://{{host}}/v1/users/{{id}}" || getUser.auth.typeID != authAPIKey || getUser.auth.apiKeyName != "X-API-Key" {
		t.Fatalf("get user target/auth = %#v", getUser)
	}
	if len(getUser.params) != 1 || getUser.params[0] != (headerEntry{key: "expand", value: "profile"}) || len(getUser.headers) != 1 || getUser.headers[0].value != "trace-123" || len(getUser.cookies) != 1 {
		t.Fatalf("get user parameters = query %#v headers %#v cookies %#v", getUser.params, getUser.headers, getUser.cookies)
	}
	updateUser := byName["updateUser"]
	if updateUser.body.mode != bodyRaw || updateUser.body.rawType != rawJSON || !strings.Contains(updateUser.body.raw, `"name": "Ada"`) || !strings.Contains(updateUser.body.raw, `"active": false`) {
		t.Fatalf("JSON example body = %#v", updateUser.body)
	}
	upload := byName["Files / Upload file"]
	if upload.body.mode != bodyMultipart || len(upload.body.multipart) != 2 || upload.body.multipart[0] != (headerEntry{key: "file", value: "@/path/to/file"}) {
		t.Fatalf("multipart body = %#v", upload.body)
	}

	environment := map[string]string{}
	for _, entry := range m.variablesInput.Entries() {
		environment[entry.key] = entry.value
	}
	if environment["host"] != "local.example.test" || environment["id"] != "42" {
		t.Fatalf("server/path environment = %#v", environment)
	}
	for _, key := range []string{"ApiToken", "username", "password"} {
		if _, ok := environment[key]; !ok {
			t.Fatalf("missing auth environment %q: %#v", key, environment)
		}
	}
}

func TestImportOpenAPIJSONAndRelativeServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.json")
	document := `{
  "openapi":"3.0.3",
  "servers":[{"url":"/api"}],
  "paths":{"/health":{"get":{"responses":{"204":{"description":"healthy"}}}}}
}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	count, err := m.ImportOpenAPI(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || m.savedRequests[0].url != "http://localhost/api/health" || m.savedRequests[0].name != "GET /health" {
		t.Fatalf("JSON import = %#v", m.savedRequests)
	}
}

func TestImportOpenAPIRejectsUnsupportedAndBrokenDocuments(t *testing.T) {
	tests := []struct {
		name     string
		document string
	}{
		{name: "unsupported version", document: `{"swagger":"1.2","paths":{"/x":{"get":{}}}}`},
		{name: "no paths", document: `{"openapi":"3.0.0","paths":{}}`},
		{name: "remote ref", document: `{"openapi":"3.0.0","paths":{"/x":{"get":{"parameters":[{"$ref":"https://example.test/parameters.yaml#/Id"}]}}}}`},
		{name: "missing local ref", document: `{"openapi":"3.0.0","paths":{"/x":{"get":{"parameters":[{"$ref":"./missing.yaml#/Id"}]}}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "api.json")
			if err := os.WriteFile(path, []byte(test.document), 0o600); err != nil {
				t.Fatal(err)
			}
			m := NewModel()
			m.savedRequests = []savedRequest{{name: "existing", method: "GET", url: "https://example.test"}}
			if _, err := m.ImportOpenAPI(path); err == nil {
				t.Fatal("invalid OpenAPI document was accepted")
			}
			if len(m.savedRequests) != 1 || m.savedRequests[0].name != "existing" {
				t.Fatalf("failed import mutated collection: %#v", m.savedRequests)
			}
		})
	}
}

func TestImportSwagger2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swagger.yaml")
	document := `swagger: '2.0'
host: api.example.test
basePath: /v2
schemes: [https]
consumes: [application/json]
security: [{ApiKey: []}]
paths:
  /pets/{id}:
    parameters:
      - name: id
        in: path
        required: true
        type: integer
        x-example: 7
    get:
      summary: Read Swagger pet
      security: [{BasicAuth: []}]
      responses: {'200': {description: OK}}
    post:
      summary: Update Swagger pet
      parameters:
        - name: pet
          in: body
          required: true
          schema: {$ref: '#/definitions/Pet'}
      responses: {'200': {description: OK}}
  /token-check:
    get:
      summary: OAuth operation
      security: [{OAuthApp: [pets.read]}]
      responses: {'200': {description: OK}}
  /upload:
    post:
      summary: Swagger upload
      consumes: [multipart/form-data]
      parameters:
        - {name: file, in: formData, required: true, type: file}
        - {name: note, in: formData, type: string, default: hello}
      responses: {'201': {description: Created}}
securityDefinitions:
  ApiKey: {type: apiKey, in: query, name: api_key}
  BasicAuth: {type: basic}
  OAuthApp:
    type: oauth2
    flow: application
    tokenUrl: /oauth/token
    scopes: {pets.read: Read pets}
definitions:
  Pet:
    type: object
    properties:
      active: {type: boolean, default: true}
      name: {type: string, example: Fido}
`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	count, err := m.ImportOpenAPI(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("imported %d requests, want 4", count)
	}
	requests := make(map[string]savedRequest, count)
	for _, request := range m.savedRequests {
		requests[request.name] = request
	}
	readPet := requests["Read Swagger pet"]
	if readPet.url != "https://api.example.test/v2/pets/{{id}}" || readPet.auth.typeID != authBasic {
		t.Fatalf("Swagger server/basic auth = %#v", readPet)
	}
	updatePet := requests["Update Swagger pet"]
	if updatePet.auth.typeID != authAPIKey || updatePet.auth.apiKeyLocation != apiKeyQuery || !strings.Contains(updatePet.body.raw, `"active": true`) || !strings.Contains(updatePet.body.raw, `"name": "Fido"`) {
		t.Fatalf("Swagger body/API key = %#v", updatePet)
	}
	upload := requests["Swagger upload"]
	if upload.body.mode != bodyMultipart || len(upload.body.multipart) != 2 || upload.body.multipart[0].value != "@/path/to/file" || upload.body.multipart[1].value != "hello" {
		t.Fatalf("Swagger form body = %#v", upload.body)
	}
	oauth := requests["OAuth operation"].auth
	if oauth.typeID != authOAuth2ClientCredentials || oauth.oauthTokenURL != "https://api.example.test/oauth/token" || oauth.oauthScope != "pets.read" {
		t.Fatalf("Swagger OAuth = %#v", oauth)
	}
	values := map[string]string{}
	for _, entry := range m.variablesInput.Entries() {
		values[entry.key] = entry.value
	}
	if values["id"] != "7" {
		t.Fatalf("Swagger path environment = %#v", values)
	}
}

func TestImportOpenAPIMultiFileReferences(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"paths", "components"} {
		if err := os.MkdirAll(filepath.Join(directory, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"api.yaml": `openapi: 3.1.0
servers: [{url: https://api.example.test/v1}]
security: [{PetKey: []}]
paths:
  /pets/{id}:
    $ref: './paths/pet.yaml'
components:
  securitySchemes:
    PetKey:
      $ref: './components/security.yaml#/PetKey'
`,
		"paths/pet.yaml": `parameters:
  - $ref: '../components/parameters.yaml#/PetID'
get:
  summary: Read pet
  parameters:
    - $ref: '../components/parameters.yaml#/Trace'
  responses: {'200': {description: OK}}
post:
  summary: Update pet
  requestBody:
    $ref: '../components/bodies.yaml#/PetInput'
  responses: {'200': {description: OK}}
`,
		"components/security.yaml": `PetKey:
  type: apiKey
  in: header
  name: X-Pet-Key
`,
		"components/parameters.yaml": `PetID:
  name: id
  in: path
  required: true
  schema: {type: integer, example: 7}
Trace:
  name: X-Trace
  in: header
  example: external-trace
`,
		"components/bodies.yaml": `PetInput:
  content:
    application/json:
      schema:
        $ref: './schemas.yaml#/Pet'
`,
		"components/schemas.yaml": `Pet:
  type: object
  properties:
    name: {type: string, default: Fido}
    owner: {$ref: '#/Owner'}
Owner:
  type: object
  properties:
    id: {type: integer, example: 42}
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, filepath.FromSlash(name)), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewModel()
	count, err := m.ImportOpenAPI(filepath.Join(directory, "api.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("imported %d requests, want 2", count)
	}
	readPet, updatePet := m.savedRequests[0], m.savedRequests[1]
	if readPet.name != "Read pet" || readPet.url != "https://api.example.test/v1/pets/{{id}}" || readPet.auth.typeID != authAPIKey || readPet.auth.apiKeyName != "X-Pet-Key" {
		t.Fatalf("external path/auth = %#v", readPet)
	}
	if len(readPet.headers) != 1 || readPet.headers[0].value != "external-trace" {
		t.Fatalf("external parameter = %#v", readPet.headers)
	}
	if updatePet.name != "Update pet" || !strings.Contains(updatePet.body.raw, `"name": "Fido"`) || !strings.Contains(updatePet.body.raw, `"id": 42`) || strings.Contains(updatePet.body.raw, openAPISourceKey) {
		t.Fatalf("nested external schema body = %#v", updatePet.body)
	}
	values := map[string]string{}
	for _, entry := range m.variablesInput.Entries() {
		values[entry.key] = entry.value
	}
	if values["id"] != "7" {
		t.Fatalf("external path parameter environment = %#v", values)
	}
}

func TestImportOpenAPIRejectsCyclicExternalAliases(t *testing.T) {
	directory := t.TempDir()
	root := `openapi: 3.0.3
paths:
  /cycle: {$ref: './a.yaml'}
`
	for name, contents := range map[string]string{"api.yaml": root, "a.yaml": `$ref: './b.yaml'`, "b.yaml": `$ref: './a.yaml'`} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewModel()
	if _, err := m.ImportOpenAPI(filepath.Join(directory, "api.yaml")); err == nil || !strings.Contains(err.Error(), "cyclic OpenAPI reference") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestImportOpenAPIOAuth2ClientCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.yaml")
	document := `openapi: 3.0.3
servers:
  - url: https://api.example.test/v1
paths:
  /reports:
    get:
      security:
        - ReportsOAuth: [reports.read, profile]
      responses:
        "200": {description: OK}
components:
  securitySchemes:
    ReportsOAuth:
      type: oauth2
      flows:
        clientCredentials:
          tokenUrl: /oauth/token
          scopes:
            reports.read: Read reports
            profile: Read profile
`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if _, err := m.ImportOpenAPI(path); err != nil {
		t.Fatal(err)
	}
	auth := m.savedRequests[0].auth
	if auth.typeID != authOAuth2ClientCredentials || auth.oauthTokenURL != "https://api.example.test/oauth/token" || auth.oauthClientID != "{{ReportsOAuth_client_id}}" || auth.oauthScope != "reports.read profile" {
		t.Fatalf("OpenAPI OAuth config = %#v", auth)
	}
	environment := map[string]string{}
	for _, entry := range m.variablesInput.Entries() {
		environment[entry.key] = entry.value
	}
	if _, ok := environment["ReportsOAuth_client_id"]; !ok {
		t.Fatalf("missing OAuth client ID variable: %#v", environment)
	}
	if _, ok := environment["ReportsOAuth_client_secret"]; !ok {
		t.Fatalf("missing OAuth client secret variable: %#v", environment)
	}
}

func TestImportOpenAPIOAuth2AuthorizationCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-code.yaml")
	document := `openapi: 3.0.3
servers:
  - url: https://api.example.test/v1
paths:
  /profile:
    get:
      security:
        - BrowserOAuth: [profile]
      responses:
        "200": {description: OK}
components:
  securitySchemes:
    BrowserOAuth:
      type: oauth2
      flows:
        authorizationCode:
          authorizationUrl: https://id.example.test/authorize
          tokenUrl: /oauth/token
          scopes:
            profile: Read profile
`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if _, err := m.ImportOpenAPI(path); err != nil {
		t.Fatal(err)
	}
	auth := m.savedRequests[0].auth
	if auth.typeID != authOAuth2AuthorizationCode || auth.oauthAuthorizationURL != "https://id.example.test/authorize" || auth.oauthTokenURL != "https://api.example.test/oauth/token" || auth.oauthClientID != "{{BrowserOAuth_client_id}}" || auth.oauthScope != "profile" || !auth.oauthPKCE {
		t.Fatalf("OpenAPI authorization-code config = %#v", auth)
	}
}

package tui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	uuid "github.com/google/uuid"
)

const postmanCollectionSchema = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

type postmanExportCollection struct {
	Info postmanExportInfo   `json:"info"`
	Item []postmanExportItem `json:"item"`
}

type postmanExportInfo struct {
	ID     string `json:"_postman_id"`
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type postmanExportItem struct {
	Name     string                  `json:"name"`
	Item     []postmanExportItem     `json:"item,omitempty"`
	Request  *postmanExportRequest   `json:"request,omitempty"`
	Event    []postmanExportEvent    `json:"event,omitempty"`
	Response []postmanExportResponse `json:"response,omitempty"`
}

type postmanExportResponse struct {
	Name            string               `json:"name"`
	Status          string               `json:"status"`
	Code            int                  `json:"code"`
	Header          []map[string]any     `json:"header,omitempty"`
	Body            string               `json:"body"`
	OriginalRequest postmanExportRequest `json:"originalRequest"`
}

type postmanExportRequest struct {
	Method string           `json:"method"`
	Header []map[string]any `json:"header,omitempty"`
	URL    postmanExportURL `json:"url"`
	Auth   map[string]any   `json:"auth,omitempty"`
	Body   map[string]any   `json:"body,omitempty"`
}

type postmanExportURL struct {
	Raw   string           `json:"raw"`
	Query []map[string]any `json:"query,omitempty"`
}

type postmanExportEvent struct {
	Listen string              `json:"listen"`
	Script postmanExportScript `json:"script"`
}

type postmanExportScript struct {
	Type string   `json:"type"`
	Exec []string `json:"exec"`
}

type postmanExportEnvironment struct {
	ID            string                          `json:"id"`
	Name          string                          `json:"name"`
	Values        []postmanExportEnvironmentValue `json:"values"`
	Scope         string                          `json:"_postman_variable_scope"`
	ExportedAt    string                          `json:"_postman_exported_at"`
	ExportedUsing string                          `json:"_postman_exported_using"`
}

type postmanExportEnvironmentValue struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
}

type postmanExportNode struct {
	name     string
	request  *savedRequest
	children []*postmanExportNode
}

// ExportPostmanCollection writes all saved requests as a local Postman v2.1 collection.
func (m *model) ExportPostmanCollection(path string) error {
	if len(m.savedRequests) == 0 {
		return fmt.Errorf("workspace contains no saved requests")
	}
	root := &postmanExportNode{}
	for index := range m.savedRequests {
		insertPostmanExportRequest(root, &m.savedRequests[index])
	}
	collectionName := "Courier Collection"
	if m.workspacePath != "" {
		base := strings.TrimSuffix(filepath.Base(m.workspacePath), filepath.Ext(m.workspacePath))
		if base != "" && base != "." {
			collectionName = base
		}
	}
	collection := postmanExportCollection{
		Info: postmanExportInfo{ID: uuid.NewString(), Name: collectionName, Schema: postmanCollectionSchema},
		Item: exportPostmanNodes(root.children),
	}
	return writePostmanExport(path, collection)
}

// ExportPostmanEnvironment writes the active local environment in Postman's JSON format.
func (m *model) ExportPostmanEnvironment(path string) error {
	m.syncActiveEnvironment()
	name := m.activeEnvironmentName()
	environment := postmanExportEnvironment{
		ID: uuid.NewString(), Name: name, Scope: "environment",
		ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), ExportedUsing: "Courier",
	}
	for _, entry := range m.variablesInput.Entries() {
		environment.Values = append(environment.Values, postmanExportEnvironmentValue{Key: entry.key, Value: entry.value, Enabled: true, Type: "default"})
	}
	return writePostmanExport(path, environment)
}

func insertPostmanExportRequest(root *postmanExportNode, request *savedRequest) {
	parts := strings.Split(request.displayName(), " / ")
	clean := parts[:0]
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		clean = []string{request.method + " " + request.url}
	}
	parent := root
	for _, folderName := range clean[:len(clean)-1] {
		var folder *postmanExportNode
		for _, child := range parent.children {
			if child.request == nil && child.name == folderName {
				folder = child
				break
			}
		}
		if folder == nil {
			folder = &postmanExportNode{name: folderName}
			parent.children = append(parent.children, folder)
		}
		parent = folder
	}
	parent.children = append(parent.children, &postmanExportNode{name: clean[len(clean)-1], request: request})
}

func exportPostmanNodes(nodes []*postmanExportNode) []postmanExportItem {
	items := make([]postmanExportItem, 0, len(nodes))
	for _, node := range nodes {
		if node.request == nil {
			items = append(items, postmanExportItem{Name: node.name, Item: exportPostmanNodes(node.children)})
			continue
		}
		request := exportPostmanRequest(*node.request)
		item := postmanExportItem{Name: node.name, Request: &request}
		for _, example := range node.request.examples {
			response := postmanExportResponse{Name: example.name, Code: example.statusCode, Body: example.responseRaw, OriginalRequest: request}
			if !example.responseRawAvailable {
				response.Body = example.responseBody
			}
			status := strings.TrimSpace(strings.Split(example.responseMeta, "•")[0])
			response.Status = strings.TrimSpace(strings.TrimPrefix(status, strconv.Itoa(example.statusCode)))
			for _, header := range responseHeaderEntries(example.responseHeaders) {
				response.Header = append(response.Header, map[string]any{"key": header.key, "value": header.value, "type": "text"})
			}
			item.Response = append(item.Response, response)
		}
		if scripts := exportPostmanTests(node.request.tests); len(scripts) > 0 {
			item.Event = append(item.Event, postmanExportEvent{Listen: "test", Script: postmanExportScript{Type: "text/javascript", Exec: scripts}})
		}
		items = append(items, item)
	}
	return items
}

func exportPostmanRequest(request savedRequest) postmanExportRequest {
	result := postmanExportRequest{Method: request.method, Auth: exportPostmanAuth(request.auth), Body: exportPostmanBody(request.body)}
	for _, header := range request.headers {
		result.Header = append(result.Header, map[string]any{"key": header.key, "value": header.value, "type": "text"})
	}
	if len(request.cookies) > 0 {
		cookies := make([]string, 0, len(request.cookies))
		for _, cookie := range request.cookies {
			cookies = append(cookies, cookie.key+"="+cookie.value)
		}
		result.Header = append(result.Header, map[string]any{"key": "Cookie", "value": strings.Join(cookies, "; "), "type": "text"})
	}
	rawURL := request.url
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	var rawQuery []string
	for _, parameter := range request.params {
		result.URL.Query = append(result.URL.Query, map[string]any{"key": parameter.key, "value": parameter.value})
		rawQuery = append(rawQuery, url.QueryEscape(parameter.key)+"="+url.QueryEscape(parameter.value))
	}
	if len(rawQuery) > 0 {
		rawURL += separator + strings.Join(rawQuery, "&")
	}
	result.URL.Raw = rawURL
	return result
}

func exportPostmanAuth(auth authConfig) map[string]any {
	attribute := func(key, value string) map[string]any {
		return map[string]any{"key": key, "value": value, "type": "string"}
	}
	switch auth.typeID {
	case authBearer:
		return map[string]any{"type": "bearer", "bearer": []map[string]any{attribute("token", auth.bearerToken)}}
	case authJWTBearer:
		location := "header"
		if auth.jwtLocation == apiKeyQuery {
			location = "query"
		}
		return map[string]any{"type": "bearer", "bearer": []map[string]any{
			attribute("token", "{{courier_jwt_token}}"), attribute("courierJwtAlgorithm", auth.jwtAlgorithm),
			attribute("courierJwtKey", auth.jwtKey), attribute("courierJwtSecretBase64", strconv.FormatBool(auth.jwtSecretBase64)),
			attribute("courierJwtPayload", auth.jwtPayload), attribute("courierJwtHeaders", auth.jwtHeaders),
			attribute("courierJwtPrefix", auth.jwtPrefix), attribute("courierJwtLocation", location), attribute("courierJwtQueryName", auth.jwtQueryName),
		}}
	case authBasic:
		return map[string]any{"type": "basic", "basic": []map[string]any{attribute("username", auth.username), attribute("password", auth.password)}}
	case authDigest:
		return map[string]any{"type": "digest", "digest": []map[string]any{attribute("username", auth.username), attribute("password", auth.password)}}
	case authAPIKey:
		location := "header"
		if auth.apiKeyLocation == apiKeyQuery {
			location = "query"
		}
		return map[string]any{"type": "apikey", "apikey": []map[string]any{attribute("key", auth.apiKeyName), attribute("value", auth.apiKeyValue), attribute("in", location)}}
	case authAWSSignatureV4:
		return map[string]any{"type": "awsv4", "awsv4": []map[string]any{
			attribute("accessKey", auth.awsAccessKey), attribute("secretKey", auth.awsSecretKey), attribute("region", auth.awsRegion),
			attribute("service", auth.awsService), attribute("sessionToken", auth.awsSessionToken),
		}}
	case authHawk:
		return map[string]any{"type": "hawk", "hawk": []map[string]any{
			attribute("authId", auth.hawkID), attribute("authKey", auth.hawkKey), attribute("algorithm", auth.hawkAlgorithm), attribute("extraData", auth.hawkExt),
		}}
	case authNTLM:
		return map[string]any{"type": "ntlm", "ntlm": []map[string]any{
			attribute("username", auth.username), attribute("password", auth.password), attribute("domain", auth.ntlmDomain), attribute("workstation", auth.ntlmWorkstation),
		}}
	case authOAuth1:
		return map[string]any{"type": "oauth1", "oauth1": []map[string]any{
			attribute("consumerKey", auth.oauth1ConsumerKey), attribute("consumerSecret", auth.oauth1ConsumerSecret),
			attribute("token", auth.oauth1Token), attribute("tokenSecret", auth.oauth1TokenSecret),
			attribute("signatureMethod", auth.oauth1SignatureMethod), attribute("realm", auth.oauth1Realm),
			attribute("callback", auth.oauth1Callback), attribute("verifier", auth.oauth1Verifier),
			attribute("includeBodyHash", strconv.FormatBool(auth.oauth1IncludeBodyHash)),
			attribute("addParamsToHeader", strconv.FormatBool(auth.oauth1Location != apiKeyQuery)),
			attribute("privateKey", auth.oauth1PrivateKey),
		}}
	case authOAuth2ClientCredentials:
		return map[string]any{"type": "oauth2", "oauth2": []map[string]any{
			attribute("grant_type", "client_credentials"), attribute("accessTokenUrl", auth.oauthTokenURL), attribute("clientId", auth.oauthClientID),
			attribute("clientSecret", auth.oauthClientSecret), attribute("scope", auth.oauthScope),
		}}
	case authOAuth2AuthorizationCode:
		clientAuthentication := auth.oauthClientAuth
		if clientAuthentication == "" || clientAuthentication == "basic" {
			clientAuthentication = "header"
		}
		return map[string]any{"type": "oauth2", "oauth2": []map[string]any{
			attribute("grant_type", "authorization_code"), attribute("authUrl", auth.oauthAuthorizationURL), attribute("accessTokenUrl", auth.oauthTokenURL),
			attribute("clientId", auth.oauthClientID), attribute("clientSecret", auth.oauthClientSecret), attribute("scope", auth.oauthScope),
			attribute("callbackUrl", auth.oauthCallbackURL), attribute("accessToken", auth.oauthAccessToken), attribute("refreshToken", auth.oauthRefreshToken),
			attribute("tokenType", auth.oauthTokenType), attribute("client_authentication", clientAuthentication), attribute("challengeAlgorithm", map[bool]string{true: "S256"}[auth.oauthPKCE]),
		}}
	case authOAuth2Password:
		return map[string]any{"type": "oauth2", "oauth2": []map[string]any{
			attribute("grant_type", "password_credentials"), attribute("accessTokenUrl", auth.oauthTokenURL), attribute("clientId", auth.oauthClientID),
			attribute("clientSecret", auth.oauthClientSecret), attribute("scope", auth.oauthScope), attribute("username", auth.username), attribute("password", auth.password),
		}}
	case authOAuth2RefreshToken:
		return map[string]any{"type": "oauth2", "oauth2": []map[string]any{
			attribute("grant_type", "refresh_token"), attribute("accessTokenUrl", auth.oauthTokenURL), attribute("clientId", auth.oauthClientID),
			attribute("clientSecret", auth.oauthClientSecret), attribute("scope", auth.oauthScope), attribute("refreshToken", auth.oauthRefreshToken),
		}}
	default:
		return map[string]any{"type": "noauth"}
	}
}

func exportPostmanBody(body bodyConfig) map[string]any {
	entry := func(key, value string) map[string]any {
		return map[string]any{"key": key, "value": value, "type": "text"}
	}
	switch body.mode {
	case bodyRaw:
		languages := map[rawBodyType]string{rawJSON: "json", rawText: "text", rawXML: "xml", rawHTML: "html"}
		return map[string]any{"mode": "raw", "raw": body.raw, "options": map[string]any{"raw": map[string]any{"language": languages[body.rawType]}}}
	case bodyFormURLEncoded:
		fields := make([]map[string]any, 0, len(body.form))
		for _, field := range body.form {
			fields = append(fields, entry(field.key, field.value))
		}
		return map[string]any{"mode": "urlencoded", "urlencoded": fields}
	case bodyMultipart:
		fields := make([]map[string]any, 0, len(body.multipart))
		for _, field := range body.multipart {
			if strings.HasPrefix(field.value, "@") && !strings.HasPrefix(field.value, "@@") {
				fields = append(fields, map[string]any{"key": field.key, "type": "file", "src": strings.TrimPrefix(field.value, "@")})
			} else {
				value := field.value
				if strings.HasPrefix(value, "@@") {
					value = value[1:]
				}
				fields = append(fields, entry(field.key, value))
			}
		}
		return map[string]any{"mode": "formdata", "formdata": fields}
	case bodyBinary:
		return map[string]any{"mode": "file", "file": map[string]any{"src": body.binaryPath}}
	case bodyGraphQL:
		return map[string]any{"mode": "graphql", "graphql": map[string]any{"query": body.graphqlQuery, "variables": body.graphqlVariables, "operationName": body.graphqlOperationName}}
	default:
		return nil
	}
}

func exportPostmanTests(assertions []headerEntry) []string {
	lines := make([]string, 0, len(assertions))
	for _, assertion := range assertions {
		expression, expected := strings.TrimSpace(assertion.key), assertion.value
		metadata, _ := json.Marshal(workspaceEntry{Key: assertion.key, Value: assertion.value})
		lines = append(lines, "// courier-assertion:"+base64.RawURLEncoding.EncodeToString(metadata))
		name, _ := json.Marshal("Courier: " + expression)
		expectedJSON, _ := json.Marshal(expected)
		statement := ""
		switch {
		case strings.HasPrefix(expression, "set."):
			variableJSON, _ := json.Marshal(strings.TrimSpace(strings.TrimPrefix(expression, "set.")))
			source := strings.TrimSpace(expected)
			switch {
			case source == "body":
				statement = "pm.environment.set(" + string(variableJSON) + ", pm.response.text());"
			case source == "status":
				statement = "pm.environment.set(" + string(variableJSON) + ", String(pm.response.code));"
			case strings.HasPrefix(source, "header."):
				headerJSON, _ := json.Marshal(strings.TrimSpace(strings.TrimPrefix(source, "header.")))
				statement = "pm.environment.set(" + string(variableJSON) + ", pm.response.headers.get(" + string(headerJSON) + "));"
			case strings.HasPrefix(source, "json."):
				if path, ok := postmanJSONPath(strings.TrimPrefix(source, "json.")); ok {
					statement = "const value = pm.response.json()" + path + "; pm.environment.set(" + string(variableJSON) + ", typeof value === \"object\" ? JSON.stringify(value) : String(value));"
				}
			case strings.HasPrefix(source, "body.matches:"):
				patternJSON, _ := json.Marshal(strings.TrimSpace(strings.TrimPrefix(source, "body.matches:")))
				statement = "const match = pm.response.text().match(new RegExp(" + string(patternJSON) + ")); pm.expect(match).not.to.eql(null); pm.environment.set(" + string(variableJSON) + ", match.length > 1 ? match[1] : match[0]);"
			}
		case expression == "status":
			var statuses []string
			for status := range strings.SplitSeq(expected, ",") {
				if _, err := strconv.Atoi(strings.TrimSpace(status)); err == nil {
					statuses = append(statuses, strings.TrimSpace(status))
				}
			}
			statement = "pm.expect([" + strings.Join(statuses, ",") + "]).to.include(pm.response.code);"
		case expression == "status.class":
			statement = "pm.expect(Math.floor(pm.response.code / 100) + \"xx\").to.eql(" + string(expectedJSON) + ");"
		case strings.HasPrefix(expression, "header."):
			headerJSON, _ := json.Marshal(strings.TrimSpace(strings.TrimPrefix(expression, "header.")))
			if expected == "*" {
				statement = "pm.expect(pm.response.headers.has(" + string(headerJSON) + ")).to.be.true;"
			} else {
				statement = "pm.expect(pm.response.headers.get(" + string(headerJSON) + ")).to.eql(" + string(expectedJSON) + ");"
			}
		case expression == "body.contains":
			statement = "pm.expect(pm.response.text()).to.include(" + string(expectedJSON) + ");"
		case expression == "body.matches":
			statement = "pm.expect(pm.response.text()).to.match(new RegExp(" + string(expectedJSON) + "));"
		case strings.HasPrefix(expression, "json."):
			if path, ok := postmanJSONPath(strings.TrimPrefix(expression, "json.")); ok {
				statement = "const value = pm.response.json()" + path + "; pm.expect(typeof value === \"object\" ? JSON.stringify(value) : String(value)).to.eql(" + string(expectedJSON) + ");"
			}
		case expression == "time.lt":
			if limit, err := parseAssertionDuration(expected); err == nil {
				statement = fmt.Sprintf("pm.expect(pm.response.responseTime).to.be.below(%d);", limit.Milliseconds())
			}
		case expression == "size.lt":
			if limit, err := parseAssertionBytes(expected); err == nil {
				statement = fmt.Sprintf("pm.expect(pm.response.responseSize).to.be.below(%d);", limit)
			}
		}
		if statement == "" {
			statement = "// Unsupported Courier assertion: " + expression + " = " + expected
		}
		lines = append(lines, "pm.test("+string(name)+", function () { "+statement+" });")
	}
	return lines
}

func postmanJSONPath(path string) (string, bool) {
	var result strings.Builder
	for _, segment := range strings.Split(path, ".") {
		name := segment
		if bracket := strings.IndexByte(segment, '['); bracket >= 0 {
			name = segment[:bracket]
		}
		if name != "" {
			encoded, _ := json.Marshal(name)
			result.WriteByte('[')
			result.Write(encoded)
			result.WriteByte(']')
		}
		remainder := segment[len(name):]
		for remainder != "" {
			if !strings.HasPrefix(remainder, "[") {
				return "", false
			}
			end := strings.IndexByte(remainder, ']')
			if end < 0 {
				return "", false
			}
			index, err := strconv.Atoi(remainder[1:end])
			if err != nil || index < 0 {
				return "", false
			}
			fmt.Fprintf(&result, "[%d]", index)
			remainder = remainder[end+1:]
		}
	}
	return result.String(), true
}

func writePostmanExport(path string, value any) error {
	path, err := expandExportPath(path)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("postman export target already exists: %s", path)
		}
		return fmt.Errorf("create postman export: %w", err)
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode postman export: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync postman export: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close postman export: %w", err)
	}
	success = true
	return nil
}

func expandExportPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("postman export path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

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
)

type postmanCollection struct {
	Info struct {
		Name string `json:"name"`
	} `json:"info"`
	Auth     *postmanAuth  `json:"auth"`
	Item     []postmanItem `json:"item"`
	Variable []postmanKV   `json:"variable"`
}

type postmanItem struct {
	Name     string            `json:"name"`
	Auth     *postmanAuth      `json:"auth"`
	Item     []postmanItem     `json:"item"`
	Request  json.RawMessage   `json:"request"`
	Event    []postmanEvent    `json:"event"`
	Response []postmanResponse `json:"response"`
}

type postmanResponse struct {
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Code   int         `json:"code"`
	Header []postmanKV `json:"header"`
	Body   string      `json:"body"`
}

type postmanEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Exec json.RawMessage `json:"exec"`
	} `json:"script"`
}

type postmanRequest struct {
	Method string          `json:"method"`
	Header []postmanKV     `json:"header"`
	URL    json.RawMessage `json:"url"`
	Auth   *postmanAuth    `json:"auth"`
	Body   postmanBody     `json:"body"`
}

type postmanURL struct {
	Raw   string      `json:"raw"`
	Query []postmanKV `json:"query"`
}

type postmanKV struct {
	Key      string      `json:"key"`
	Value    interface{} `json:"value"`
	Disabled bool        `json:"disabled"`
	Type     string      `json:"type"`
	Src      interface{} `json:"src"`
}

type postmanAuth struct {
	Type   string      `json:"type"`
	Bearer []postmanKV `json:"bearer"`
	Basic  []postmanKV `json:"basic"`
	Digest []postmanKV `json:"digest"`
	APIKey []postmanKV `json:"apikey"`
	OAuth2 []postmanKV `json:"oauth2"`
	AWSV4  []postmanKV `json:"awsv4"`
	Hawk   []postmanKV `json:"hawk"`
	NTLM   []postmanKV `json:"ntlm"`
	OAuth1 []postmanKV `json:"oauth1"`
}

type postmanBody struct {
	Mode       string          `json:"mode"`
	Raw        string          `json:"raw"`
	URLEncoded []postmanKV     `json:"urlencoded"`
	FormData   []postmanKV     `json:"formdata"`
	File       json.RawMessage `json:"file"`
	GraphQL    json.RawMessage `json:"graphql"`
	Options    struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
}

type postmanEnvironment struct {
	Name   string `json:"name"`
	Values []struct {
		Key     string      `json:"key"`
		Value   interface{} `json:"value"`
		Enabled *bool       `json:"enabled"`
	} `json:"values"`
}

// ImportPostmanCollection appends requests from a Postman Collection v2.x file.
func (m *model) ImportPostmanCollection(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read Postman collection: %w", err)
	}
	var collection postmanCollection
	if err := json.Unmarshal(data, &collection); err != nil {
		return 0, fmt.Errorf("decode Postman collection: %w", err)
	}
	if len(collection.Item) == 0 {
		return 0, fmt.Errorf("postman collection contains no items")
	}

	before := len(m.savedRequests)
	m.importPostmanItems(collection.Item, nil, collection.Auth)
	m.mergeEnvironmentEntries(postmanEntries(collection.Variable))
	return len(m.savedRequests) - before, nil
}

func (m *model) importPostmanItems(items []postmanItem, folders []string, inheritedAuth *postmanAuth) {
	for _, item := range items {
		auth := inheritedAuth
		if item.Auth != nil {
			auth = item.Auth
		}
		if len(item.Item) > 0 {
			m.importPostmanItems(item.Item, append(folders, item.Name), auth)
			continue
		}
		if len(item.Request) == 0 || string(item.Request) == "null" {
			continue
		}
		var request postmanRequest
		if json.Unmarshal(item.Request, &request) != nil {
			continue
		}
		if request.Auth != nil {
			auth = request.Auth
		}
		headers, cookies := postmanRequestHeaders(request.Header)
		nameParts := append(append([]string{}, folders...), item.Name)
		m.savedRequests = append(m.savedRequests, savedRequest{
			name:     strings.Join(nameParts, " / "),
			method:   strings.TrimSpace(request.Method),
			url:      postmanRequestURL(request.URL),
			headers:  headers,
			params:   postmanQuery(request.URL),
			auth:     postmanAuthConfig(auth),
			body:     postmanBodyConfig(request.Body),
			cookies:  cookies,
			tests:    postmanCourierAssertions(item.Event),
			examples: postmanResponseExamples(item.Response),
		})
	}
}

func postmanResponseExamples(responses []postmanResponse) []savedExample {
	if len(responses) == 0 {
		return nil
	}
	examples := make([]savedExample, 0, len(responses))
	for index, response := range responses {
		name := strings.TrimSpace(response.Name)
		if name == "" {
			name = fmt.Sprintf("Example %d", index+1)
		}
		headers := postmanEntries(response.Header)
		contentType := ""
		for _, header := range headers {
			if strings.EqualFold(header.key, "Content-Type") {
				contentType = header.value
				break
			}
		}
		meta := strings.TrimSpace(strings.Join([]string{strconv.Itoa(response.Code), response.Status}, " "))
		examples = append(examples, savedExample{
			name: name, statusCode: response.Code,
			responseBody: formatResponseBody([]byte(response.Body), contentType), responseRaw: response.Body, responseRawAvailable: true,
			responseHeaders: formattedResponseHeaders(headers), responseMeta: meta,
		})
	}
	return examples
}

func postmanCourierAssertions(events []postmanEvent) []headerEntry {
	var assertions []headerEntry
	for _, event := range events {
		if !strings.EqualFold(event.Listen, "test") {
			continue
		}
		var lines []string
		if json.Unmarshal(event.Script.Exec, &lines) != nil {
			var script string
			if json.Unmarshal(event.Script.Exec, &script) == nil {
				lines = strings.Split(script, "\n")
			}
		}
		for _, line := range lines {
			encoded, ok := strings.CutPrefix(strings.TrimSpace(line), "// courier-assertion:")
			if !ok {
				continue
			}
			data, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
			if err != nil {
				continue
			}
			var assertion workspaceEntry
			if json.Unmarshal(data, &assertion) == nil && strings.TrimSpace(assertion.Key) != "" {
				assertions = append(assertions, headerEntry{key: assertion.Key, value: assertion.Value})
			}
		}
	}
	return assertions
}

func postmanRequestHeaders(values []postmanKV) ([]headerEntry, []headerEntry) {
	var headers, cookies []headerEntry
	for _, entry := range postmanEntries(values) {
		if strings.EqualFold(entry.key, "Cookie") {
			cookies = append(cookies, parseCurlCookies(entry.value)...)
			continue
		}
		headers = append(headers, entry)
	}
	return headers, cookies
}

func postmanRequestURL(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return stripURLQuery(text)
	}
	var value postmanURL
	if json.Unmarshal(raw, &value) == nil {
		return stripURLQuery(value.Raw)
	}
	return ""
}

func postmanQuery(raw json.RawMessage) []headerEntry {
	var value postmanURL
	if json.Unmarshal(raw, &value) == nil && len(value.Query) > 0 {
		return postmanEntries(value.Query)
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return nil
	}
	entries := make([]headerEntry, 0)
	for key, values := range parsed.Query() {
		for _, value := range values {
			entries = append(entries, headerEntry{key: key, value: value})
		}
	}
	return entries
}

func stripURLQuery(value string) string {
	if index := strings.IndexByte(value, '?'); index >= 0 {
		return value[:index]
	}
	return value
}

func postmanEntries(values []postmanKV) []headerEntry {
	entries := make([]headerEntry, 0, len(values))
	for _, value := range values {
		if value.Disabled || strings.TrimSpace(value.Key) == "" {
			continue
		}
		entries = append(entries, headerEntry{key: value.Key, value: stringifyPostmanValue(value.Value)})
	}
	return entries
}

func stringifyPostmanValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func postmanAuthConfig(auth *postmanAuth) authConfig {
	if auth == nil {
		return authConfig{typeID: authNone}
	}
	values := func(entries []postmanKV) map[string]string {
		result := make(map[string]string, len(entries))
		for _, entry := range entries {
			result[entry.Key] = stringifyPostmanValue(entry.Value)
		}
		return result
	}
	switch auth.Type {
	case "bearer":
		fields := values(auth.Bearer)
		if fields["courierJwtAlgorithm"] != "" {
			location := apiKeyHeader
			if fields["courierJwtLocation"] == "query" {
				location = apiKeyQuery
			}
			return authConfig{
				typeID: authJWTBearer, jwtAlgorithm: fields["courierJwtAlgorithm"], jwtKey: fields["courierJwtKey"],
				jwtSecretBase64: fields["courierJwtSecretBase64"] == "true", jwtPayload: fields["courierJwtPayload"],
				jwtHeaders: fields["courierJwtHeaders"], jwtPrefix: fields["courierJwtPrefix"], jwtLocation: location,
				jwtQueryName: fields["courierJwtQueryName"],
			}
		}
		return authConfig{typeID: authBearer, bearerToken: fields["token"]}
	case "basic":
		fields := values(auth.Basic)
		return authConfig{typeID: authBasic, username: fields["username"], password: fields["password"]}
	case "digest":
		fields := values(auth.Digest)
		return authConfig{typeID: authDigest, username: fields["username"], password: fields["password"]}
	case "awsv4":
		fields := values(auth.AWSV4)
		return authConfig{typeID: authAWSSignatureV4, awsAccessKey: fields["accessKey"], awsSecretKey: fields["secretKey"], awsRegion: fields["region"], awsService: fields["service"], awsSessionToken: fields["sessionToken"]}
	case "hawk":
		fields := values(auth.Hawk)
		return authConfig{typeID: authHawk, hawkID: fields["authId"], hawkKey: fields["authKey"], hawkAlgorithm: fields["algorithm"], hawkExt: fields["extraData"]}
	case "ntlm":
		fields := values(auth.NTLM)
		return authConfig{typeID: authNTLM, username: fields["username"], password: fields["password"], ntlmDomain: fields["domain"], ntlmWorkstation: fields["workstation"]}
	case "oauth1":
		fields := values(auth.OAuth1)
		location := apiKeyHeader
		if strings.EqualFold(fields["addParamsToHeader"], "false") {
			location = apiKeyQuery
		}
		return authConfig{
			typeID: authOAuth1, oauth1ConsumerKey: fields["consumerKey"], oauth1ConsumerSecret: fields["consumerSecret"],
			oauth1Token: fields["token"], oauth1TokenSecret: fields["tokenSecret"], oauth1PrivateKey: fields["privateKey"],
			oauth1SignatureMethod: strings.ToUpper(fields["signatureMethod"]), oauth1Realm: fields["realm"],
			oauth1Callback: fields["callback"], oauth1Verifier: fields["verifier"], oauth1IncludeBodyHash: strings.EqualFold(fields["includeBodyHash"], "true"), oauth1Location: location,
		}
	case "apikey":
		fields := values(auth.APIKey)
		location := apiKeyHeader
		if fields["in"] == "query" {
			location = apiKeyQuery
		}
		return authConfig{typeID: authAPIKey, apiKeyName: fields["key"], apiKeyValue: fields["value"], apiKeyLocation: location}
	case "oauth2":
		fields := values(auth.OAuth2)
		tokenURL := fields["accessTokenUrl"]
		if tokenURL == "" {
			tokenURL = fields["tokenUrl"]
		}
		clientID := fields["clientId"]
		grantType := strings.ToLower(fields["grant_type"])
		if grantType == "authorization_code" || grantType == "authorization_code_with_pkce" || fields["authUrl"] != "" {
			clientAuth := "basic"
			switch strings.ToLower(fields["client_authentication"]) {
			case "body":
				clientAuth = "body"
			case "none":
				clientAuth = "none"
			}
			return authConfig{
				typeID: authOAuth2AuthorizationCode, oauthAuthorizationURL: fields["authUrl"], oauthTokenURL: tokenURL,
				oauthClientID: clientID, oauthClientSecret: fields["clientSecret"], oauthScope: fields["scope"],
				oauthCallbackURL: fields["callbackUrl"], oauthAccessToken: fields["accessToken"], oauthRefreshToken: fields["refreshToken"],
				oauthTokenType: fields["tokenType"], oauthClientAuth: clientAuth,
				oauthPKCE: grantType == "authorization_code_with_pkce" || strings.EqualFold(fields["challengeAlgorithm"], "S256"),
			}
		}
		if fields["grant_type"] == "password" || fields["grant_type"] == "password_credentials" {
			return authConfig{typeID: authOAuth2Password, oauthTokenURL: tokenURL, oauthClientID: clientID, oauthClientSecret: fields["clientSecret"], oauthScope: fields["scope"], username: fields["username"], password: fields["password"]}
		}
		if fields["grant_type"] == "refresh_token" || fields["refreshToken"] != "" {
			return authConfig{typeID: authOAuth2RefreshToken, oauthTokenURL: tokenURL, oauthClientID: clientID, oauthClientSecret: fields["clientSecret"], oauthScope: fields["scope"], oauthRefreshToken: fields["refreshToken"]}
		}
		if tokenURL != "" && clientID != "" {
			return authConfig{typeID: authOAuth2ClientCredentials, oauthTokenURL: tokenURL, oauthClientID: clientID, oauthClientSecret: fields["clientSecret"], oauthScope: fields["scope"]}
		}
		if token := fields["accessToken"]; token != "" {
			return authConfig{typeID: authBearer, bearerToken: token}
		}
		return authConfig{typeID: authNone}
	default:
		return authConfig{typeID: authNone}
	}
}

func postmanBodyConfig(body postmanBody) bodyConfig {
	switch body.Mode {
	case "raw":
		rawType := rawText
		switch body.Options.Raw.Language {
		case "json":
			rawType = rawJSON
		case "xml":
			rawType = rawXML
		case "html":
			rawType = rawHTML
		}
		return bodyConfig{mode: bodyRaw, rawType: rawType, raw: body.Raw}
	case "urlencoded":
		return bodyConfig{mode: bodyFormURLEncoded, form: postmanEntries(body.URLEncoded)}
	case "formdata":
		entries := make([]headerEntry, 0, len(body.FormData))
		for _, field := range body.FormData {
			if field.Disabled {
				continue
			}
			value := stringifyPostmanValue(field.Value)
			if field.Type == "file" {
				value = "@" + postmanFileSource(field.Src)
			} else if strings.HasPrefix(value, "@") {
				value = "@" + value
			}
			entries = append(entries, headerEntry{key: field.Key, value: value})
		}
		return bodyConfig{mode: bodyMultipart, multipart: entries}
	case "file":
		var file struct {
			Src string `json:"src"`
		}
		_ = json.Unmarshal(body.File, &file)
		return bodyConfig{mode: bodyBinary, binaryPath: file.Src}
	case "graphql":
		if len(body.GraphQL) > 0 {
			var graphql struct {
				Query         string      `json:"query"`
				Variables     interface{} `json:"variables"`
				OperationName string      `json:"operationName"`
			}
			if json.Unmarshal(body.GraphQL, &graphql) == nil {
				variables := ""
				switch value := graphql.Variables.(type) {
				case string:
					variables = value
				case nil:
				default:
					if encoded, err := json.Marshal(value); err == nil {
						variables = string(encoded)
					}
				}
				return bodyConfig{mode: bodyGraphQL, graphqlQuery: graphql.Query, graphqlVariables: variables, graphqlOperationName: graphql.OperationName}
			}
		}
	}
	return bodyConfig{mode: bodyNone, rawType: rawJSON}
}

func postmanFileSource(source interface{}) string {
	switch value := source.(type) {
	case string:
		return value
	case []interface{}:
		if len(value) > 0 {
			return stringifyPostmanValue(value[0])
		}
	}
	return ""
}

// ImportPostmanEnvironment imports enabled values as a named local environment.
func (m *model) ImportPostmanEnvironment(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read Postman environment: %w", err)
	}
	var environment postmanEnvironment
	if err := json.Unmarshal(data, &environment); err != nil {
		return 0, fmt.Errorf("decode Postman environment: %w", err)
	}
	imported := make([]headerEntry, 0, len(environment.Values))
	for _, value := range environment.Values {
		if value.Enabled != nil && !*value.Enabled || strings.TrimSpace(value.Key) == "" {
			continue
		}
		imported = append(imported, headerEntry{key: value.Key, value: stringifyPostmanValue(value.Value)})
	}
	name := strings.TrimSpace(environment.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if name == "" || name == "." {
		name = "Imported"
	}
	m.syncActiveEnvironment()
	for index := range m.environments {
		if m.environments[index].name == name {
			m.activateEnvironmentIndex(index)
			m.mergeEnvironmentEntries(imported)
			m.syncActiveEnvironment()
			return len(imported), nil
		}
	}
	m.environments = append(m.environments, environmentProfile{name: name, values: append([]headerEntry(nil), imported...)})
	m.environmentPos = len(m.environments) - 1
	m.variablesInput.SetEntries(imported)
	return len(imported), nil
}

func (m *model) mergeEnvironmentEntries(imported []headerEntry) {
	m.variablesInput.SetEntries(mergeHeaderEntries(m.variablesInput.Entries(), imported))
}

func mergeHeaderEntries(entries, imported []headerEntry) []headerEntry {
	entries = append([]headerEntry(nil), entries...)
	index := make(map[string]int, len(entries))
	for i, entry := range entries {
		index[entry.key] = i
	}
	for _, entry := range imported {
		if i, ok := index[entry.key]; ok {
			entries[i] = entry
		} else {
			index[entry.key] = len(entries)
			entries = append(entries, entry)
		}
	}
	return entries
}

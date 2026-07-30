package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var openAPIMethods = []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}

const openAPISourceKey = "\x00courier-source-file"

type openAPIImporter struct {
	document    map[string]interface{}
	rootPath    string
	documents   map[string]map[string]interface{}
	security    map[string]interface{}
	swagger2    bool
	consumes    []interface{}
	environment []headerEntry
	err         error
}

// ImportOpenAPI appends operations from a Swagger 2.0 or OpenAPI 3.x JSON/YAML document.
func (m *model) ImportOpenAPI(path string) (int, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("resolve OpenAPI document path: %w", err)
	}
	importer := openAPIImporter{rootPath: filepath.Clean(absolutePath), documents: make(map[string]map[string]interface{})}
	document, err := importer.loadDocument(importer.rootPath)
	if err != nil {
		return 0, err
	}
	importer.document = document
	version := stringValue(document["openapi"])
	importer.swagger2 = stringValue(document["swagger"]) == "2.0"
	if !strings.HasPrefix(version, "3.") && !importer.swagger2 {
		return 0, fmt.Errorf("unsupported OpenAPI version %q; expected Swagger 2.0 or OpenAPI 3.x", version)
	}
	paths := mapValue(document["paths"])
	if len(paths) == 0 {
		return 0, fmt.Errorf("OpenAPI document contains no paths")
	}
	components := mapValue(document["components"])
	importer.security = mapValue(components["securitySchemes"])
	baseURL := importer.serverURL(sliceValue(document["servers"]))
	if importer.swagger2 {
		importer.security = mapValue(document["securityDefinitions"])
		importer.consumes = sliceValue(document["consumes"])
		baseURL = importer.swaggerServerURL(document)
	}
	globalSecurity := sliceValue(document["security"])

	pathNames := sortedMapKeys(paths)
	before := len(m.savedRequests)
	for _, pathName := range pathNames {
		pathItem := importer.resolveMap(paths[pathName])
		if pathItem == nil {
			continue
		}
		pathBaseURL := baseURL
		if !importer.swagger2 {
			if servers := sliceValue(pathItem["servers"]); len(servers) > 0 {
				pathBaseURL = importer.serverURL(servers)
			}
		}
		pathParameters := sliceValue(pathItem["parameters"])
		for _, methodName := range openAPIMethods {
			operation := importer.resolveMap(pathItem[methodName])
			if operation == nil {
				continue
			}
			operationBaseURL := pathBaseURL
			if !importer.swagger2 {
				if servers := sliceValue(operation["servers"]); len(servers) > 0 {
					operationBaseURL = importer.serverURL(servers)
				}
			}
			request := savedRequest{
				name:   openAPIOperationName(methodName, pathName, operation),
				method: strings.ToUpper(methodName),
				url:    strings.TrimRight(operationBaseURL, "/") + replaceOpenAPIPlaceholders(pathName),
				body:   bodyConfig{mode: bodyNone, rawType: rawJSON},
			}
			parameters := append(append([]interface{}{}, pathParameters...), sliceValue(operation["parameters"])...)
			importer.applyParameters(&request, parameters)
			security := globalSecurity
			if value, exists := operation["security"]; exists {
				security = sliceValue(value)
			}
			request.auth = importer.authConfig(security, operationBaseURL)
			if importer.swagger2 {
				request.body = importer.swaggerBodyConfig(parameters, sliceValue(operation["consumes"]))
			} else {
				request.body = importer.bodyConfig(operation["requestBody"])
			}
			m.savedRequests = append(m.savedRequests, request)
		}
	}
	count := len(m.savedRequests) - before
	if importer.err != nil {
		m.savedRequests = m.savedRequests[:before]
		return 0, importer.err
	}
	if count == 0 {
		return 0, fmt.Errorf("OpenAPI document contains no supported operations")
	}
	m.mergeEnvironmentDefaults(importer.environment)
	return count, nil
}

func (i *openAPIImporter) serverURL(servers []interface{}) string {
	if len(servers) == 0 {
		return "http://localhost"
	}
	server := i.resolveMap(servers[0])
	value := stringValue(server["url"])
	if value == "" {
		return "http://localhost"
	}
	variables := mapValue(server["variables"])
	for _, name := range sortedMapKeys(variables) {
		variable := i.resolveMap(variables[name])
		defaultValue := stringifyOpenAPIValue(variable["default"])
		i.addEnvironment(name, defaultValue)
		value = strings.ReplaceAll(value, "{"+name+"}", "{{"+name+"}}")
	}
	if strings.HasPrefix(value, "/") {
		value = "http://localhost" + value
	}
	return value
}

func (i *openAPIImporter) swaggerServerURL(document map[string]interface{}) string {
	scheme := "http"
	for _, raw := range sliceValue(document["schemes"]) {
		if candidate := strings.ToLower(stringValue(raw)); candidate == "http" || candidate == "https" {
			scheme = candidate
			break
		}
	}
	host := strings.TrimSpace(stringValue(document["host"]))
	if host == "" {
		host = "localhost"
	}
	basePath := strings.TrimSpace(stringValue(document["basePath"]))
	if basePath != "" && !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	return scheme + "://" + host + strings.TrimRight(basePath, "/")
}

func (i *openAPIImporter) applyParameters(request *savedRequest, parameters []interface{}) {
	for _, raw := range parameters {
		parameter := i.resolveMap(raw)
		if parameter == nil {
			continue
		}
		name := stringValue(parameter["name"])
		location := stringValue(parameter["in"])
		if name == "" || location == "" {
			continue
		}
		value := openAPIExampleValue(parameter)
		if value == "" {
			value = "{{" + name + "}}"
			i.addEnvironment(name, "")
		}
		switch location {
		case "path":
			i.addEnvironment(name, openAPIExampleValue(parameter))
		case "query":
			request.params = append(request.params, headerEntry{key: name, value: value})
		case "header":
			request.headers = append(request.headers, headerEntry{key: name, value: value})
		case "cookie":
			request.cookies = append(request.cookies, headerEntry{key: name, value: value})
		}
	}
}

func (i *openAPIImporter) authConfig(security []interface{}, baseURL string) authConfig {
	if len(security) == 0 {
		return authConfig{typeID: authNone}
	}
	requirement := mapValue(security[0])
	for _, schemeName := range sortedMapKeys(requirement) {
		scheme := i.resolveMap(i.security[schemeName])
		if scheme == nil {
			continue
		}
		schemeType := stringValue(scheme["type"])
		switch schemeType {
		case "apiKey":
			location := apiKeyHeader
			if stringValue(scheme["in"]) == "query" {
				location = apiKeyQuery
			}
			i.addEnvironment(schemeName, "")
			return authConfig{typeID: authAPIKey, apiKeyName: stringValue(scheme["name"]), apiKeyValue: "{{" + schemeName + "}}", apiKeyLocation: location}
		case "http":
			switch strings.ToLower(stringValue(scheme["scheme"])) {
			case "basic":
				i.addEnvironment("username", "")
				i.addEnvironment("password", "")
				return authConfig{typeID: authBasic, username: "{{username}}", password: "{{password}}"}
			case "bearer":
				i.addEnvironment(schemeName, "")
				return authConfig{typeID: authBearer, bearerToken: "{{" + schemeName + "}}"}
			}
		case "basic":
			i.addEnvironment("username", "")
			i.addEnvironment("password", "")
			return authConfig{typeID: authBasic, username: "{{username}}", password: "{{password}}"}
		case "oauth2":
			flows := mapValue(scheme["flows"])
			clientCredentials := i.resolveMap(flows["clientCredentials"])
			passwordFlow := i.resolveMap(flows["password"])
			authorizationCode := i.resolveMap(flows["authorizationCode"])
			if i.swagger2 {
				switch stringValue(scheme["flow"]) {
				case "application":
					clientCredentials = scheme
				case "password":
					passwordFlow = scheme
				case "accessCode":
					authorizationCode = scheme
				}
			}
			if tokenURL := stringValue(authorizationCode["tokenUrl"]); tokenURL != "" {
				authorizationURL := stringValue(authorizationCode["authorizationUrl"])
				clientIDVariable := schemeName + "_client_id"
				clientSecretVariable := schemeName + "_client_secret"
				i.addEnvironment(clientIDVariable, "")
				i.addEnvironment(clientSecretVariable, "")
				var scopes []string
				for _, value := range sliceValue(requirement[schemeName]) {
					if scope := stringValue(value); scope != "" {
						scopes = append(scopes, scope)
					}
				}
				return authConfig{
					typeID: authOAuth2AuthorizationCode, oauthAuthorizationURL: openAPITokenURL(authorizationURL, baseURL), oauthTokenURL: openAPITokenURL(tokenURL, baseURL),
					oauthClientID: "{{" + clientIDVariable + "}}", oauthClientSecret: "{{" + clientSecretVariable + "}}", oauthScope: strings.Join(scopes, " "),
					oauthCallbackURL: "http://127.0.0.1:8085/callback", oauthClientAuth: "basic", oauthPKCE: true,
				}
			}
			if tokenURL := stringValue(clientCredentials["tokenUrl"]); tokenURL != "" {
				tokenURL = openAPITokenURL(tokenURL, baseURL)
				clientIDVariable := schemeName + "_client_id"
				clientSecretVariable := schemeName + "_client_secret"
				i.addEnvironment(clientIDVariable, "")
				i.addEnvironment(clientSecretVariable, "")
				scopes := make([]string, 0)
				for _, value := range sliceValue(requirement[schemeName]) {
					if scope := stringValue(value); scope != "" {
						scopes = append(scopes, scope)
					}
				}
				return authConfig{typeID: authOAuth2ClientCredentials, oauthTokenURL: tokenURL, oauthClientID: "{{" + clientIDVariable + "}}", oauthClientSecret: "{{" + clientSecretVariable + "}}", oauthScope: strings.Join(scopes, " ")}
			}
			if tokenURL := stringValue(passwordFlow["tokenUrl"]); tokenURL != "" {
				tokenURL = openAPITokenURL(tokenURL, baseURL)
				clientIDVariable := schemeName + "_client_id"
				clientSecretVariable := schemeName + "_client_secret"
				i.addEnvironment(clientIDVariable, "")
				i.addEnvironment(clientSecretVariable, "")
				i.addEnvironment("username", "")
				i.addEnvironment("password", "")
				var scopes []string
				for _, value := range sliceValue(requirement[schemeName]) {
					if scope := stringValue(value); scope != "" {
						scopes = append(scopes, scope)
					}
				}
				return authConfig{typeID: authOAuth2Password, oauthTokenURL: tokenURL, oauthClientID: "{{" + clientIDVariable + "}}", oauthClientSecret: "{{" + clientSecretVariable + "}}", oauthScope: strings.Join(scopes, " "), username: "{{username}}", password: "{{password}}"}
			}
			i.addEnvironment(schemeName, "")
			return authConfig{typeID: authBearer, bearerToken: "{{" + schemeName + "}}"}
		case "openIdConnect":
			i.addEnvironment(schemeName, "")
			return authConfig{typeID: authBearer, bearerToken: "{{" + schemeName + "}}"}
		}
	}
	return authConfig{typeID: authNone}
}

func openAPITokenURL(tokenURL, baseURL string) string {
	if strings.HasPrefix(tokenURL, "/") {
		if parsedBase, err := url.Parse(baseURL); err == nil && parsedBase.Scheme != "" && parsedBase.Host != "" {
			return parsedBase.Scheme + "://" + parsedBase.Host + tokenURL
		}
	}
	return tokenURL
}

func (i *openAPIImporter) swaggerBodyConfig(parameters, operationConsumes []interface{}) bodyConfig {
	consumes := operationConsumes
	if len(consumes) == 0 {
		consumes = i.consumes
	}
	contentType := "application/json"
	if len(consumes) > 0 && stringValue(consumes[0]) != "" {
		contentType = stringValue(consumes[0])
	}
	var form []headerEntry
	for _, raw := range parameters {
		parameter := i.resolveMap(raw)
		switch stringValue(parameter["in"]) {
		case "body":
			media := map[string]interface{}{"schema": parameter["schema"]}
			if example, ok := parameter["x-example"]; ok {
				media["example"] = example
			}
			return i.bodyConfig(map[string]interface{}{"content": map[string]interface{}{contentType: media}})
		case "formData":
			name := stringValue(parameter["name"])
			if name == "" {
				continue
			}
			value := openAPIExampleValue(parameter)
			if stringValue(parameter["type"]) == "file" {
				value = "@/path/to/file"
			} else if value == "" {
				value = "{{" + name + "}}"
				i.addEnvironment(name, "")
			}
			form = append(form, headerEntry{key: name, value: value})
		}
	}
	if len(form) == 0 {
		return bodyConfig{mode: bodyNone, rawType: rawJSON}
	}
	for _, raw := range consumes {
		if stringValue(raw) == "multipart/form-data" {
			return bodyConfig{mode: bodyMultipart, multipart: form}
		}
	}
	return bodyConfig{mode: bodyFormURLEncoded, form: form}
}

func (i *openAPIImporter) bodyConfig(raw interface{}) bodyConfig {
	requestBody := i.resolveMap(raw)
	content := mapValue(requestBody["content"])
	if len(content) == 0 {
		return bodyConfig{mode: bodyNone, rawType: rawJSON}
	}
	contentType := preferredOpenAPIContentType(content)
	media := i.resolveMap(content[contentType])
	example := media["example"]
	if example == nil {
		for _, name := range sortedMapKeys(mapValue(media["examples"])) {
			exampleObject := i.resolveMap(mapValue(media["examples"])[name])
			if value, ok := exampleObject["value"]; ok {
				example = value
				break
			}
		}
	}
	if example == nil {
		example = i.schemaExample(media["schema"], 0, map[string]bool{})
	}
	switch {
	case strings.Contains(contentType, "json"):
		data, _ := json.MarshalIndent(example, "", "  ")
		if string(data) == "null" {
			data = []byte("{}")
		}
		return bodyConfig{mode: bodyRaw, rawType: rawJSON, raw: string(data)}
	case contentType == "application/x-www-form-urlencoded":
		return bodyConfig{mode: bodyFormURLEncoded, form: objectExampleEntries(example)}
	case strings.HasPrefix(contentType, "multipart/form-data"):
		return bodyConfig{mode: bodyMultipart, multipart: objectExampleEntries(example)}
	case contentType == "application/octet-stream":
		return bodyConfig{mode: bodyBinary, binaryPath: "/path/to/file"}
	case strings.Contains(contentType, "xml"):
		return bodyConfig{mode: bodyRaw, rawType: rawXML, raw: stringifyOpenAPIValue(example)}
	case strings.Contains(contentType, "html"):
		return bodyConfig{mode: bodyRaw, rawType: rawHTML, raw: stringifyOpenAPIValue(example)}
	default:
		return bodyConfig{mode: bodyRaw, rawType: rawText, raw: stringifyOpenAPIValue(example)}
	}
}

func (i *openAPIImporter) schemaExample(raw interface{}, depth int, seen map[string]bool) interface{} {
	if depth > 8 {
		return nil
	}
	schema := mapValue(raw)
	if ref := stringValue(schema["$ref"]); ref != "" {
		sourcePath := stringValue(schema[openAPISourceKey])
		if sourcePath == "" {
			sourcePath = i.rootPath
		}
		_, identity, err := i.resolveRef(sourcePath, ref)
		if err != nil {
			if i.err == nil {
				i.err = err
			}
			return nil
		}
		if seen[identity] {
			return nil
		}
		seen[identity] = true
		defer delete(seen, identity)
		schema = i.resolveMap(schema)
	}
	for _, key := range []string{"example", "default"} {
		if value, ok := schema[key]; ok {
			return value
		}
	}
	for _, composition := range []string{"oneOf", "anyOf"} {
		if choices := sliceValue(schema[composition]); len(choices) > 0 {
			return i.schemaExample(choices[0], depth+1, seen)
		}
	}
	if parts := sliceValue(schema["allOf"]); len(parts) > 0 {
		combined := map[string]interface{}{}
		for _, part := range parts {
			if object, ok := i.schemaExample(part, depth+1, seen).(map[string]interface{}); ok {
				for key, value := range object {
					combined[key] = value
				}
			}
		}
		return combined
	}
	if values := sliceValue(schema["enum"]); len(values) > 0 {
		return values[0]
	}
	switch stringValue(schema["type"]) {
	case "object", "":
		properties := mapValue(schema["properties"])
		if len(properties) == 0 {
			return map[string]interface{}{}
		}
		result := make(map[string]interface{}, len(properties))
		for _, name := range sortedMapKeys(properties) {
			result[name] = i.schemaExample(properties[name], depth+1, seen)
		}
		return result
	case "array":
		return []interface{}{i.schemaExample(schema["items"], depth+1, seen)}
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "string":
		if stringValue(schema["format"]) == "binary" {
			return "@/path/to/file"
		}
		return "string"
	default:
		return nil
	}
}

func (i *openAPIImporter) resolveMap(raw interface{}) map[string]interface{} {
	value := mapValue(raw)
	seen := make(map[string]bool)
	for ref := stringValue(value["$ref"]); ref != ""; ref = stringValue(value["$ref"]) {
		sourcePath := stringValue(value[openAPISourceKey])
		if sourcePath == "" {
			sourcePath = i.rootPath
		}
		resolved, identity, err := i.resolveRef(sourcePath, ref)
		if err != nil {
			if i.err == nil {
				i.err = err
			}
			return value
		}
		if seen[identity] {
			if i.err == nil {
				i.err = fmt.Errorf("cyclic OpenAPI reference %q", ref)
			}
			return value
		}
		seen[identity] = true
		result := make(map[string]interface{}, len(resolved)+len(value))
		for key, item := range resolved {
			result[key] = item
		}
		for key, item := range value {
			if key != "$ref" && key != openAPISourceKey {
				result[key] = item
			}
		}
		value = result
	}
	return value
}

func (i *openAPIImporter) loadDocument(path string) (map[string]interface{}, error) {
	path = filepath.Clean(path)
	if document, ok := i.documents[path]; ok {
		return document, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read OpenAPI reference %q: %w", path, err)
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode OpenAPI reference %q: %w", path, err)
	}
	if document == nil {
		return nil, fmt.Errorf("OpenAPI reference %q is not an object", path)
	}
	annotateOpenAPISource(document, path)
	i.documents[path] = document
	return document, nil
}

func annotateOpenAPISource(value interface{}, path string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		typed[openAPISourceKey] = path
		for key, item := range typed {
			if key != openAPISourceKey {
				annotateOpenAPISource(item, path)
			}
		}
	case []interface{}:
		for _, item := range typed {
			annotateOpenAPISource(item, path)
		}
	}
}

func (i *openAPIImporter) resolveRef(sourcePath, ref string) (map[string]interface{}, string, error) {
	reference, err := url.Parse(ref)
	if err != nil {
		return nil, "", fmt.Errorf("parse OpenAPI reference %q: %w", ref, err)
	}
	if reference.Scheme != "" || reference.Host != "" {
		return nil, "", fmt.Errorf("remote OpenAPI reference %q is not supported; use a local file", ref)
	}
	targetPath := sourcePath
	if reference.Path != "" {
		decodedPath, decodeErr := url.PathUnescape(reference.EscapedPath())
		if decodeErr != nil {
			return nil, "", fmt.Errorf("decode OpenAPI reference path %q: %w", ref, decodeErr)
		}
		if filepath.IsAbs(decodedPath) {
			targetPath = filepath.Clean(decodedPath)
		} else {
			targetPath = filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(decodedPath))
		}
	}
	document, err := i.loadDocument(targetPath)
	if err != nil {
		return nil, "", err
	}
	var current interface{} = document
	fragment := reference.Fragment
	if fragment == "" {
		return document, targetPath + "#", nil
	}
	if !strings.HasPrefix(fragment, "/") {
		return nil, "", fmt.Errorf("OpenAPI reference %q must use a JSON Pointer fragment", ref)
	}
	for _, encoded := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		object := mapValue(current)
		var exists bool
		current, exists = object[part]
		if !exists {
			return nil, "", fmt.Errorf("OpenAPI reference %q from %q was not found", ref, sourcePath)
		}
	}
	resolved := mapValue(current)
	if resolved == nil {
		return nil, "", fmt.Errorf("OpenAPI reference %q does not resolve to an object", ref)
	}
	return resolved, targetPath + "#" + fragment, nil
}

func (i *openAPIImporter) addEnvironment(key, value string) {
	if strings.TrimSpace(key) == "" {
		return
	}
	for index := range i.environment {
		if i.environment[index].key == key {
			if i.environment[index].value == "" && value != "" {
				i.environment[index].value = value
			}
			return
		}
	}
	i.environment = append(i.environment, headerEntry{key: key, value: value})
}

func (m *model) mergeEnvironmentDefaults(imported []headerEntry) {
	existing := m.variablesInput.Entries()
	used := make(map[string]bool, len(existing))
	for _, entry := range existing {
		used[entry.key] = true
	}
	for _, entry := range imported {
		if !used[entry.key] {
			existing = append(existing, entry)
			used[entry.key] = true
		}
	}
	m.variablesInput.SetEntries(existing)
}

func openAPIOperationName(method, path string, operation map[string]interface{}) string {
	name := stringValue(operation["summary"])
	if name == "" {
		name = stringValue(operation["operationId"])
	}
	if name == "" {
		name = strings.ToUpper(method) + " " + path
	}
	if tags := sliceValue(operation["tags"]); len(tags) > 0 && stringValue(tags[0]) != "" {
		name = stringValue(tags[0]) + " / " + name
	}
	return name
}

func openAPIExampleValue(parameter map[string]interface{}) string {
	for _, key := range []string{"example", "x-example", "default"} {
		if value, exists := parameter[key]; exists {
			return stringifyOpenAPIValue(value)
		}
	}
	schema := mapValue(parameter["schema"])
	for _, key := range []string{"example", "default"} {
		if value, exists := schema[key]; exists {
			return stringifyOpenAPIValue(value)
		}
	}
	if values := sliceValue(schema["enum"]); len(values) > 0 {
		return stringifyOpenAPIValue(values[0])
	}
	if values := sliceValue(parameter["enum"]); len(values) > 0 {
		return stringifyOpenAPIValue(values[0])
	}
	return ""
}

func preferredOpenAPIContentType(content map[string]interface{}) string {
	for _, preferred := range []string{"application/json", "application/x-www-form-urlencoded", "multipart/form-data", "application/octet-stream", "application/xml", "text/plain"} {
		if _, ok := content[preferred]; ok {
			return preferred
		}
	}
	keys := sortedMapKeys(content)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func objectExampleEntries(value interface{}) []headerEntry {
	object := mapValue(value)
	entries := make([]headerEntry, 0, len(object))
	for _, key := range sortedMapKeys(object) {
		itemValue := stringifyOpenAPIValue(object[key])
		entries = append(entries, headerEntry{key: key, value: itemValue})
	}
	return entries
}

func replaceOpenAPIPlaceholders(value string) string {
	var result strings.Builder
	for index := 0; index < len(value); {
		start := strings.IndexByte(value[index:], '{')
		if start < 0 {
			result.WriteString(value[index:])
			break
		}
		start += index
		end := strings.IndexByte(value[start+1:], '}')
		if end < 0 {
			result.WriteString(value[index:])
			break
		}
		end += start + 1
		result.WriteString(value[index:start])
		result.WriteString("{{" + value[start+1:end] + "}}")
		index = end + 1
	}
	return result.String()
}

func mapValue(value interface{}) map[string]interface{} {
	if result, ok := value.(map[string]interface{}); ok {
		return result
	}
	return nil
}

func sliceValue(value interface{}) []interface{} {
	if result, ok := value.([]interface{}); ok {
		return result
	}
	return nil
}

func stringValue(value interface{}) string {
	if result, ok := value.(string); ok {
		return result
	}
	return ""
}

func stringifyOpenAPIValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	if number, ok := value.(float64); ok && number == float64(int64(number)) {
		return strconv.FormatInt(int64(number), 10)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func sortedMapKeys(value map[string]interface{}) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		if key != openAPISourceKey {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

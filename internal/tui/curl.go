package tui

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// ParseCurlCommand converts a common curl command into a saved Courier request.
// It intentionally rejects shell operators instead of executing or expanding them.
func ParseCurlCommand(command string) (savedRequest, error) {
	args, err := splitShellWords(command)
	if err != nil {
		return savedRequest{}, err
	}
	if len(args) == 0 {
		return savedRequest{}, fmt.Errorf("empty cURL command")
	}
	if strings.EqualFold(args[0], "curl") {
		args = args[1:]
	}

	request := savedRequest{method: "", auth: authConfig{typeID: authNone}, body: bodyConfig{mode: bodyNone, rawType: rawJSON}}
	var dataValues, encodedValues, formValues []string
	var contentType string
	forceGET := false
	digestAuth := false
	ntlmAuth := false
	awsSignatureV4 := ""
	unixSocket := ""

	valueOptions := map[string]bool{
		"-X": true, "--request": true, "--url": true, "-H": true, "--header": true,
		"-d": true, "--data": true, "--data-raw": true, "--data-binary": true, "--data-urlencode": true,
		"-F": true, "--form": true, "-u": true, "--user": true, "-b": true, "--cookie": true,
		"-A": true, "--user-agent": true, "-e": true, "--referer": true, "--json": true,
		"-T": true, "--upload-file": true, "--oauth2-bearer": true, "--form-string": true,
		"--aws-sigv4": true, "--unix-socket": true,
	}
	ignoredValueOptions := map[string]bool{
		"-o": true, "--output": true, "-x": true, "--proxy": true, "--connect-timeout": true,
		"-m": true, "--max-time": true, "--retry": true, "--cacert": true, "--cert": true,
		"--key": true, "--resolve": true, "--interface": true, "--user-agent": false,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "|" || arg == ";" || arg == "&&" || arg == "||" {
			return savedRequest{}, fmt.Errorf("shell operators are not supported in cURL imports")
		}
		option, value, hasInline := splitLongOption(arg)
		if !hasInline {
			option, value, hasInline = splitShortOption(arg)
		}
		if valueOptions[option] || ignoredValueOptions[option] {
			if !hasInline {
				if i+1 >= len(args) {
					return savedRequest{}, fmt.Errorf("cURL option %s requires a value", option)
				}
				i++
				value = args[i]
			}
			if ignoredValueOptions[option] {
				continue
			}
			switch option {
			case "-X", "--request":
				request.method = strings.TrimSpace(value)
			case "--url":
				request.url = value
			case "-H", "--header":
				key, headerValue, ok := strings.Cut(value, ":")
				if !ok {
					return savedRequest{}, fmt.Errorf("invalid cURL header %q", value)
				}
				key, headerValue = strings.TrimSpace(key), strings.TrimSpace(headerValue)
				if strings.EqualFold(key, "Authorization") && strings.HasPrefix(strings.ToLower(headerValue), "bearer ") {
					request.auth = authConfig{typeID: authBearer, bearerToken: strings.TrimSpace(headerValue[len("bearer "):])}
					continue
				}
				if strings.EqualFold(key, "Cookie") {
					request.cookies = append(request.cookies, parseCurlCookies(headerValue)...)
					continue
				}
				if strings.EqualFold(key, "Content-Type") {
					contentType = headerValue
				}
				request.headers = append(request.headers, headerEntry{key: key, value: headerValue})
			case "-d", "--data", "--data-raw":
				dataValues = append(dataValues, value)
			case "--json":
				contentType = "application/json"
				request.headers = append(request.headers,
					headerEntry{key: "Content-Type", value: "application/json"},
					headerEntry{key: "Accept", value: "application/json"},
				)
				dataValues = append(dataValues, value)
			case "--data-binary":
				if strings.HasPrefix(value, "@") && len(dataValues) == 0 {
					request.body = bodyConfig{mode: bodyBinary, binaryPath: strings.TrimPrefix(value, "@")}
				} else {
					dataValues = append(dataValues, value)
				}
			case "--data-urlencode":
				encodedValues = append(encodedValues, value)
			case "-F", "--form":
				formValues = append(formValues, value)
			case "--form-string":
				field := parseKeyValue(value)
				if strings.HasPrefix(field.value, "@") {
					field.value = "@" + field.value
				}
				formValues = append(formValues, field.key+"="+field.value)
			case "-u", "--user":
				username, password, _ := strings.Cut(value, ":")
				request.auth = authConfig{typeID: authBasic, username: username, password: password}
			case "-b", "--cookie":
				request.cookies = append(request.cookies, parseCurlCookies(value)...)
			case "-A", "--user-agent":
				request.headers = append(request.headers, headerEntry{key: "User-Agent", value: value})
			case "-e", "--referer":
				request.headers = append(request.headers, headerEntry{key: "Referer", value: value})
			case "-T", "--upload-file":
				request.body = bodyConfig{mode: bodyBinary, binaryPath: strings.TrimPrefix(value, "@")}
				if request.method == "" {
					request.method = "PUT"
				}
			case "--oauth2-bearer":
				request.auth = authConfig{typeID: authBearer, bearerToken: value}
			case "--aws-sigv4":
				awsSignatureV4 = value
			case "--unix-socket":
				unixSocket = value
			}
			continue
		}

		switch arg {
		case "-G", "--get":
			forceGET = true
		case "-I", "--head":
			request.method = "HEAD"
		case "--digest":
			digestAuth = true
		case "--ntlm":
			ntlmAuth = true
		case "-L", "--location", "-k", "--insecure", "--compressed", "-s", "--silent", "-S", "--show-error", "-i", "--include", "-v", "--verbose":
			// These options affect curl execution or output, not the request representation.
		default:
			if !strings.HasPrefix(arg, "-") && request.url == "" {
				request.url = arg
			}
		}
	}

	if request.url == "" {
		return savedRequest{}, fmt.Errorf("cURL command does not contain a URL")
	}
	if digestAuth && request.auth.typeID == authBasic {
		request.auth.typeID = authDigest
	}
	if ntlmAuth && request.auth.typeID == authBasic {
		request.auth.typeID = authNTLM
		if domain, username, ok := strings.Cut(request.auth.username, `\`); ok {
			request.auth.ntlmDomain, request.auth.username = domain, username
		}
	}
	if awsSignatureV4 != "" && request.auth.typeID == authBasic {
		parts := strings.Split(awsSignatureV4, ":")
		request.auth.typeID = authAWSSignatureV4
		request.auth.awsAccessKey = request.auth.username
		request.auth.awsSecretKey = request.auth.password
		request.auth.username, request.auth.password = "", ""
		if len(parts) > 2 {
			request.auth.awsRegion = parts[2]
		}
		if len(parts) > 3 {
			request.auth.awsService = parts[3]
		}
		for index := 0; index < len(request.headers); index++ {
			if strings.EqualFold(request.headers[index].key, "X-Amz-Security-Token") {
				request.auth.awsSessionToken = request.headers[index].value
				request.headers = append(request.headers[:index], request.headers[index+1:]...)
				break
			}
		}
	}
	if unixSocket != "" {
		if !strings.HasPrefix(unixSocket, "/") {
			return savedRequest{}, fmt.Errorf("cURL Unix socket path must be absolute")
		}
		parsed, parseErr := url.Parse(request.url)
		if parseErr != nil {
			return savedRequest{}, fmt.Errorf("parse cURL URL for Unix socket: %w", parseErr)
		}
		scheme := parsed.Scheme
		if scheme == "" {
			scheme = "http"
		}
		resource := parsed.EscapedPath()
		if resource == "" {
			resource = "/"
		}
		if parsed.RawQuery != "" {
			resource += "?" + parsed.RawQuery
		}
		request.url = scheme + "://unix:" + unixSocket + ":" + resource
	}
	request.url, request.params = splitCurlURL(request.url)
	if forceGET {
		request.method = "GET"
		for _, value := range append(dataValues, encodedValues...) {
			request.params = append(request.params, parseKeyValue(value))
		}
		dataValues, encodedValues = nil, nil
	}
	if len(formValues) > 0 {
		request.body.mode = bodyMultipart
		for _, value := range formValues {
			request.body.multipart = append(request.body.multipart, parseKeyValue(value))
		}
	} else if len(encodedValues) > 0 || (len(dataValues) > 0 && strings.Contains(strings.ToLower(contentType), "application/x-www-form-urlencoded")) {
		request.body.mode = bodyFormURLEncoded
		for _, value := range append(dataValues, encodedValues...) {
			request.body.form = append(request.body.form, parseKeyValue(value))
		}
	} else if len(dataValues) > 0 {
		raw := strings.Join(dataValues, "&")
		request.body = bodyConfig{mode: bodyRaw, rawType: curlRawType(contentType, raw), raw: raw}
	}
	if request.method == "" {
		if request.body.mode != bodyNone {
			request.method = "POST"
		} else {
			request.method = "GET"
		}
	}
	if !validHTTPMethod(request.method) {
		return savedRequest{}, fmt.Errorf("invalid HTTP method %q", request.method)
	}
	request.name = request.url
	return request, nil
}

func splitLongOption(arg string) (string, string, bool) {
	if strings.HasPrefix(arg, "--") {
		if option, value, ok := strings.Cut(arg, "="); ok {
			return option, value, true
		}
	}
	return arg, "", false
}

func splitShortOption(arg string) (string, string, bool) {
	if len(arg) > 2 {
		for _, option := range []string{"-X", "-H", "-d", "-F", "-u", "-b", "-A", "-e", "-T", "-o", "-x", "-m"} {
			if strings.HasPrefix(arg, option) {
				return option, arg[len(option):], true
			}
		}
	}
	return arg, "", false
}

func parseCurlCookies(value string) []headerEntry {
	var result []headerEntry
	for part := range strings.SplitSeq(value, ";") {
		entry := parseKeyValue(strings.TrimSpace(part))
		if entry.key != "" {
			result = append(result, entry)
		}
	}
	return result
}

func parseKeyValue(value string) headerEntry {
	key, entryValue, ok := strings.Cut(value, "=")
	if !ok {
		return headerEntry{key: value}
	}
	return headerEntry{key: key, value: entryValue}
}

func splitCurlURL(value string) (string, []headerEntry) {
	queryStart := strings.IndexByte(value, '?')
	if queryStart < 0 {
		return value, nil
	}
	base := value[:queryStart]
	rawQuery := value[queryStart+1:]
	if fragmentStart := strings.IndexByte(rawQuery, '#'); fragmentStart >= 0 {
		base += rawQuery[fragmentStart:]
		rawQuery = rawQuery[:fragmentStart]
	}
	var params []headerEntry
	for part := range strings.SplitSeq(rawQuery, "&") {
		key, itemValue, _ := strings.Cut(part, "=")
		decodedKey, keyErr := url.QueryUnescape(key)
		decodedValue, valueErr := url.QueryUnescape(itemValue)
		if keyErr == nil {
			key = decodedKey
		}
		if valueErr == nil {
			itemValue = decodedValue
		}
		params = append(params, headerEntry{key: key, value: itemValue})
	}
	return base, params
}

func curlRawType(contentType, body string) rawBodyType {
	contentType = strings.ToLower(contentType)
	switch {
	case strings.Contains(contentType, "json"), strings.HasPrefix(strings.TrimSpace(body), "{") || strings.HasPrefix(strings.TrimSpace(body), "["):
		return rawJSON
	case strings.Contains(contentType, "xml"):
		return rawXML
	case strings.Contains(contentType, "html"):
		return rawHTML
	default:
		return rawText
	}
}

func splitShellWords(input string) ([]string, error) {
	var words []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	started := false
	flush := func() {
		if started {
			words = append(words, current.String())
			current.Reset()
			started = false
		}
	}
	for _, char := range input {
		if escaped {
			if char != '\n' {
				current.WriteRune(char)
				started = true
			}
			escaped = false
			continue
		}
		if quote != '\'' && char == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
				started = true
			} else {
				current.WriteRune(char)
				started = true
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
			started = true
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			current.WriteRune(char)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated quote or escape in cURL command")
	}
	flush()
	return words, nil
}

// CurlCommand renders a saved request as a copyable POSIX-shell curl command.
func CurlCommand(request savedRequest) string {
	urlValue := requestCommandURL(request)
	parts := []string{"curl"}
	if target, matched, err := parseUnixSocketURL(urlValue); err == nil && matched {
		parts = append(parts, "--unix-socket", shellQuote(target.socketPath))
		urlValue = target.requestURL
	}
	parts = append(parts, "--request", shellQuote(request.method), "--url", shellQuote(urlValue))
	for _, header := range request.headers {
		parts = append(parts, "--header", shellQuote(header.key+": "+header.value))
	}
	switch request.auth.typeID {
	case authBearer:
		parts = append(parts, "--header", shellQuote("Authorization: Bearer "+request.auth.bearerToken))
	case authJWTBearer:
		if request.auth.jwtLocation == apiKeyHeader {
			token := "{{courier_jwt_token}}"
			if prefix := strings.TrimSpace(request.auth.jwtPrefix); prefix != "" {
				token = prefix + " " + token
			}
			parts = append(parts, "--header", shellQuote("Authorization: "+token))
		}
	case authBasic:
		parts = append(parts, "--user", shellQuote(request.auth.username+":"+request.auth.password))
	case authDigest:
		parts = append(parts, "--digest", "--user", shellQuote(request.auth.username+":"+request.auth.password))
	case authAWSSignatureV4:
		provider := "aws:amz:" + request.auth.awsRegion + ":" + request.auth.awsService
		parts = append(parts, "--aws-sigv4", shellQuote(provider), "--user", shellQuote(request.auth.awsAccessKey+":"+request.auth.awsSecretKey))
		if request.auth.awsSessionToken != "" {
			parts = append(parts, "--header", shellQuote("X-Amz-Security-Token: "+request.auth.awsSessionToken))
		}
	case authAPIKey:
		if request.auth.apiKeyLocation == apiKeyHeader {
			parts = append(parts, "--header", shellQuote(request.auth.apiKeyName+": "+request.auth.apiKeyValue))
		}
	case authOAuth2ClientCredentials, authOAuth2Password, authOAuth2RefreshToken, authOAuth2AuthorizationCode:
		parts = append(parts, "--header", shellQuote("Authorization: Bearer {{oauth2_access_token}}"))
	case authHawk:
		parts = append(parts, "--header", shellQuote("Authorization: {{hawk_authorization}}"))
	case authNTLM:
		username := request.auth.username
		if request.auth.ntlmDomain != "" {
			username = request.auth.ntlmDomain + `\` + username
		}
		parts = append(parts, "--ntlm", "--user", shellQuote(username+":"+request.auth.password))
	case authOAuth1:
		if request.auth.oauth1Location == apiKeyHeader {
			parts = append(parts, "--header", shellQuote("Authorization: {{oauth1_authorization}}"))
		}
	}
	if len(request.cookies) > 0 {
		cookies := make([]string, 0, len(request.cookies))
		for _, cookie := range request.cookies {
			cookies = append(cookies, cookie.key+"="+cookie.value)
		}
		parts = append(parts, "--cookie", shellQuote(strings.Join(cookies, "; ")))
	}
	switch request.body.mode {
	case bodyRaw:
		if !hasHeader(request.headers, "Content-Type") {
			parts = append(parts, "--header", shellQuote("Content-Type: "+rawContentType(request.body.rawType)))
		}
		parts = append(parts, "--data-raw", shellQuote(request.body.raw))
	case bodyFormURLEncoded:
		for _, field := range request.body.form {
			parts = append(parts, "--data-urlencode", shellQuote(field.key+"="+field.value))
		}
		if oauth1UsesFormBody(request) {
			parts = append(parts, "--data-raw", shellQuote("{{oauth1_form_parameters}}"))
		}
	case bodyMultipart:
		for _, field := range request.body.multipart {
			parts = append(parts, "--form", shellQuote(field.key+"="+field.value))
		}
	case bodyBinary:
		parts = append(parts, "--data-binary", shellQuote("@"+request.body.binaryPath))
	case bodyGraphQL:
		payload, err := buildGraphQLPayload(request.body.graphqlQuery, request.body.graphqlVariables, request.body.graphqlOperationName, newVariableResolver(nil))
		if err == nil {
			if !hasHeader(request.headers, "Content-Type") {
				parts = append(parts, "--header", shellQuote("Content-Type: application/json"))
			}
			parts = append(parts, "--data-raw", shellQuote(string(payload)))
		}
	}
	return strings.Join(parts, " ")
}

func requestCommandURL(request savedRequest) string {
	urlValue := request.url
	if len(request.params) > 0 || (request.auth.typeID == authAPIKey && request.auth.apiKeyLocation == apiKeyQuery) || (request.auth.typeID == authJWTBearer && request.auth.jwtLocation == apiKeyQuery) || (request.auth.typeID == authOAuth1 && request.auth.oauth1Location == apiKeyQuery && !oauth1UsesFormBody(request)) {
		separator := "?"
		if strings.Contains(urlValue, "?") {
			separator = "&"
		}
		var query []string
		for _, entry := range request.params {
			query = append(query, url.QueryEscape(entry.key)+"="+url.QueryEscape(entry.value))
		}
		if request.auth.typeID == authAPIKey && request.auth.apiKeyLocation == apiKeyQuery {
			query = append(query, url.QueryEscape(request.auth.apiKeyName)+"="+url.QueryEscape(request.auth.apiKeyValue))
		}
		if request.auth.typeID == authJWTBearer && request.auth.jwtLocation == apiKeyQuery {
			name := strings.TrimSpace(request.auth.jwtQueryName)
			if name == "" {
				name = "jwt"
			}
			query = append(query, url.QueryEscape(name)+"={{courier_jwt_token}}")
		}
		if request.auth.typeID == authOAuth1 && request.auth.oauth1Location == apiKeyQuery && !oauth1UsesFormBody(request) {
			query = append(query, "{{oauth1_parameters}}")
		}
		urlValue += separator + strings.Join(query, "&")
	}
	return urlValue
}

func oauth1UsesFormBody(request savedRequest) bool {
	return request.auth.typeID == authOAuth1 && request.auth.oauth1Location == apiKeyQuery && request.body.mode == bodyFormURLEncoded && (request.method == http.MethodPost || request.method == http.MethodPut)
}

func rawContentType(bodyType rawBodyType) string {
	return map[rawBodyType]string{rawJSON: "application/json", rawText: "text/plain", rawXML: "application/xml", rawHTML: "text/html"}[bodyType]
}

func hasHeader(headers []headerEntry, name string) bool {
	for _, header := range headers {
		if strings.EqualFold(header.key, name) {
			return true
		}
	}
	return false
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_@%+=:,./{}-", r)
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (m *model) ImportCurl(command string) error {
	request, err := ParseCurlCommand(command)
	if err != nil {
		return err
	}
	m.savedRequests = append(m.savedRequests, request)
	m.collectionPos = len(m.savedRequests) - 1
	m.sidebarMode = sidebarCollections
	return nil
}

func (m *model) ExportSavedCurl(selector string) (string, error) {
	request, err := ParseSavedRequestSelector(selector, m.savedRequests)
	if err != nil {
		return "", err
	}
	return CurlCommand(request), nil
}

func ReadCurlCommand(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read cURL command: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func ParseSavedRequestSelector(value string, requests []savedRequest) (savedRequest, error) {
	if index, err := strconv.Atoi(value); err == nil {
		if index < 1 || index > len(requests) {
			return savedRequest{}, fmt.Errorf("saved request index must be between 1 and %d", len(requests))
		}
		return requests[index-1], nil
	}
	for _, request := range requests {
		if request.name == value {
			return request, nil
		}
	}
	return savedRequest{}, fmt.Errorf("saved request %q was not found", value)
}

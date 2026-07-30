package tui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

type harArchive struct {
	Log struct {
		Version string `json:"version,omitempty"`
		Creator struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"creator,omitempty"`
		Entries []harEntry `json:"entries"`
	} `json:"log"`
}

type harEntry struct {
	Request struct {
		Method      string     `json:"method"`
		URL         string     `json:"url"`
		Headers     []harValue `json:"headers"`
		QueryString []harValue `json:"queryString"`
		Cookies     []harValue `json:"cookies"`
		PostData    *struct {
			MIMEType string     `json:"mimeType"`
			Text     string     `json:"text"`
			Params   []harValue `json:"params"`
		} `json:"postData"`
		CourierName   string           `json:"_courierName,omitempty"`
		CourierURL    string           `json:"_courierURL,omitempty"`
		CourierParams []workspaceEntry `json:"_courierParams,omitempty"`
		CourierAuth   *workspaceAuth   `json:"_courierAuth,omitempty"`
		CourierBody   *workspaceBody   `json:"_courierBody,omitempty"`
		CourierTests  []workspaceEntry `json:"_courierTests,omitempty"`
	} `json:"request"`
	Response struct {
		Status     int        `json:"status"`
		StatusText string     `json:"statusText"`
		Headers    []harValue `json:"headers"`
		Content    struct {
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
			Encoding string `json:"encoding"`
		} `json:"content"`
		CourierExample *workspaceExample `json:"_courierExample,omitempty"`
	} `json:"response"`
}

type harValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harExportArchive struct {
	Log harExportLog `json:"log"`
}

type harExportLog struct {
	Version string            `json:"version"`
	Creator map[string]string `json:"creator"`
	Entries []harExportEntry  `json:"entries"`
}

type harExportEntry struct {
	StartedDateTime string             `json:"startedDateTime"`
	Time            int                `json:"time"`
	Request         harExportRequest   `json:"request"`
	Response        harExportResponse  `json:"response"`
	Cache           map[string]any     `json:"cache"`
	Timings         map[string]float64 `json:"timings"`
}

type harExportRequest struct {
	Method        string             `json:"method"`
	URL           string             `json:"url"`
	HTTPVersion   string             `json:"httpVersion"`
	Headers       []harValue         `json:"headers"`
	QueryString   []harValue         `json:"queryString"`
	Cookies       []harValue         `json:"cookies"`
	HeadersSize   int                `json:"headersSize"`
	BodySize      int                `json:"bodySize"`
	PostData      *harExportPostData `json:"postData,omitempty"`
	CourierName   string             `json:"_courierName,omitempty"`
	CourierURL    string             `json:"_courierURL,omitempty"`
	CourierParams []workspaceEntry   `json:"_courierParams,omitempty"`
	CourierAuth   workspaceAuth      `json:"_courierAuth"`
	CourierBody   workspaceBody      `json:"_courierBody"`
	CourierTests  []workspaceEntry   `json:"_courierTests,omitempty"`
}

type harExportPostData struct {
	MIMEType string     `json:"mimeType"`
	Text     string     `json:"text"`
	Params   []harValue `json:"params,omitempty"`
}

type harExportResponse struct {
	Status         int              `json:"status"`
	StatusText     string           `json:"statusText"`
	HTTPVersion    string           `json:"httpVersion"`
	Headers        []harValue       `json:"headers"`
	Cookies        []harValue       `json:"cookies"`
	Content        harExportContent `json:"content"`
	RedirectURL    string           `json:"redirectURL"`
	HeadersSize    int              `json:"headersSize"`
	BodySize       int              `json:"bodySize"`
	CourierExample workspaceExample `json:"_courierExample"`
}

type harExportContent struct {
	Size     int    `json:"size"`
	MIMEType string `json:"mimeType"`
	Text     string `json:"text"`
	Encoding string `json:"encoding,omitempty"`
}

// ExportHAR writes saved requests and response examples as a HAR 1.2 file.
func (m *model) ExportHAR(path string) error {
	if len(m.savedRequests) == 0 {
		return fmt.Errorf("workspace contains no saved requests")
	}
	archive := harExportArchive{Log: harExportLog{Version: "1.2", Creator: map[string]string{"name": "Courier", "version": "1"}}}
	for _, request := range m.savedRequests {
		examples := request.examples
		if len(examples) == 0 {
			examples = []savedExample{{}}
		}
		for _, example := range examples {
			entry, err := exportHAREntry(request, example)
			if err != nil {
				return fmt.Errorf("export HAR request %q: %w", request.displayName(), err)
			}
			archive.Log.Entries = append(archive.Log.Entries, entry)
		}
	}
	return writeHARExport(path, archive)
}

func exportHAREntry(request savedRequest, example savedExample) (harExportEntry, error) {
	parsed, err := url.Parse(request.url)
	if err != nil {
		return harExportEntry{}, err
	}
	query := parsed.Query()
	for _, parameter := range request.params {
		query.Add(parameter.key, parameter.value)
	}
	parsed.RawQuery = query.Encode()
	postData, bodySize, err := exportHARBody(request.body)
	if err != nil {
		return harExportEntry{}, err
	}
	headers := toHARValues(request.headers)
	if postData != nil && !hasHARHeader(headers, "Content-Type") {
		headers = append(headers, harValue{Name: "Content-Type", Value: postData.MIMEType})
	}
	responseHeaders := parseHARHeaderText(example.responseHeaders)
	responseBody := []byte(example.responseRaw)
	if !example.responseRawAvailable {
		responseBody = []byte(ansi.Strip(example.responseBody))
	}
	mimeType := harHeaderValue(responseHeaders, "Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	content := harExportContent{Size: len(responseBody), MIMEType: mimeType, Text: string(responseBody)}
	if !mqttTextPayload(responseBody) {
		content.Text = base64.StdEncoding.EncodeToString(responseBody)
		content.Encoding = "base64"
	}
	statusText := strings.TrimSpace(strings.TrimPrefix(example.responseMeta, strconv.Itoa(example.statusCode)))
	if statusText == "" {
		statusText = http.StatusText(example.statusCode)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return harExportEntry{
		StartedDateTime: now, Time: 0, Cache: map[string]any{}, Timings: map[string]float64{"send": 0, "wait": 0, "receive": 0},
		Request:  harExportRequest{Method: request.method, URL: parsed.String(), HTTPVersion: "HTTP/1.1", Headers: headers, QueryString: harValuesFromQuery(query), Cookies: toHARValues(request.cookies), HeadersSize: -1, BodySize: bodySize, PostData: postData, CourierName: request.name, CourierURL: request.url, CourierParams: toWorkspaceEntries(request.params), CourierAuth: request.auth.toWorkspace(), CourierBody: request.body.toWorkspace(), CourierTests: toWorkspaceEntries(request.tests)},
		Response: harExportResponse{Status: example.statusCode, StatusText: statusText, HTTPVersion: "HTTP/1.1", Headers: responseHeaders, Cookies: []harValue{}, Content: content, RedirectURL: harHeaderValue(responseHeaders, "Location"), HeadersSize: -1, BodySize: len(responseBody), CourierExample: workspaceExampleFromSaved(example)},
	}, nil
}

func exportHARBody(body bodyConfig) (*harExportPostData, int, error) {
	result := &harExportPostData{}
	switch body.mode {
	case bodyNone:
		return nil, 0, nil
	case bodyRaw:
		result.MIMEType = map[rawBodyType]string{rawJSON: "application/json", rawText: "text/plain", rawXML: "application/xml", rawHTML: "text/html"}[body.rawType]
		result.Text = body.raw
	case bodyFormURLEncoded:
		values := make(url.Values)
		result.MIMEType = "application/x-www-form-urlencoded"
		result.Params = toHARValues(body.form)
		for _, field := range body.form {
			values.Add(field.key, field.value)
		}
		result.Text = values.Encode()
	case bodyMultipart:
		result.MIMEType = "multipart/form-data"
		result.Params = toHARValues(body.multipart)
	case bodyBinary:
		data, err := os.ReadFile(body.binaryPath)
		if err != nil {
			return nil, 0, fmt.Errorf("read binary body: %w", err)
		}
		result.MIMEType = mime.TypeByExtension(filepath.Ext(body.binaryPath))
		if result.MIMEType == "" {
			result.MIMEType = "application/octet-stream"
		}
		result.Text = base64.StdEncoding.EncodeToString(data)
	case bodyGraphQL:
		payload := map[string]any{"query": body.graphqlQuery}
		if body.graphqlVariables != "" {
			var variables any
			if err := json.Unmarshal([]byte(body.graphqlVariables), &variables); err != nil {
				return nil, 0, fmt.Errorf("decode GraphQL variables: %w", err)
			}
			payload["variables"] = variables
		}
		if body.graphqlOperationName != "" {
			payload["operationName"] = body.graphqlOperationName
		}
		encoded, _ := json.Marshal(payload)
		result.MIMEType, result.Text = "application/json", string(encoded)
	}
	return result, len(result.Text), nil
}

// ImportHAR appends HTTP requests and captured responses from a HAR 1.2 file.
func (m *model) ImportHAR(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read HAR: %w", err)
	}
	var archive harArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		return 0, fmt.Errorf("decode HAR: %w", err)
	}
	if len(archive.Log.Entries) == 0 {
		return 0, fmt.Errorf("HAR contains no entries")
	}
	requests := make([]savedRequest, 0, len(archive.Log.Entries))
	for index, entry := range archive.Log.Entries {
		request, err := savedRequestFromHAR(entry, index)
		if err != nil {
			return 0, fmt.Errorf("HAR entry %d: %w", index+1, err)
		}
		if entry.Request.CourierName != "" && len(requests) > 0 {
			previous := &requests[len(requests)-1]
			if previous.name == request.name && previous.method == request.method && previous.url == request.url {
				previous.examples = append(previous.examples, request.examples...)
				continue
			}
		}
		requests = append(requests, request)
	}
	m.savedRequests = append(m.savedRequests, requests...)
	return len(requests), nil
}

func savedRequestFromHAR(entry harEntry, index int) (savedRequest, error) {
	parsed, err := url.Parse(entry.Request.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return savedRequest{}, fmt.Errorf("invalid request URL %q", entry.Request.URL)
	}
	query := entry.Request.QueryString
	if len(query) == 0 {
		for key, values := range parsed.Query() {
			for _, value := range values {
				query = append(query, harValue{Name: key, Value: value})
			}
		}
	}
	parsed.RawQuery, parsed.ForceQuery = "", false
	method := strings.TrimSpace(entry.Request.Method)
	if method == "" {
		method = "GET"
	}
	request := savedRequest{name: fmt.Sprintf("HAR / %03d %s %s", index+1, method, parsed.Path), method: method, url: parsed.String(), auth: authConfig{typeID: authNone}, body: bodyConfig{mode: bodyNone}}
	if entry.Request.CourierName != "" {
		request.name = entry.Request.CourierName
	}
	if entry.Request.CourierURL != "" {
		request.url = entry.Request.CourierURL
	}
	if entry.Request.CourierAuth != nil {
		request.auth = entry.Request.CourierAuth.fromWorkspace()
	}
	if entry.Request.CourierBody != nil {
		request.body = entry.Request.CourierBody.fromWorkspace()
	}
	request.tests = fromWorkspaceEntries(entry.Request.CourierTests)
	request.headers = harRequestHeaders(entry.Request.Headers, len(entry.Request.Cookies) > 0)
	if entry.Request.CourierURL != "" {
		request.params = fromWorkspaceEntries(entry.Request.CourierParams)
	} else {
		request.params = harEntries(query)
	}
	request.cookies = harEntries(entry.Request.Cookies)
	if entry.Request.PostData != nil && entry.Request.CourierBody == nil {
		request.body = harBody(*entry.Request.PostData)
	}
	if entry.Response.Status > 0 {
		body := []byte(entry.Response.Content.Text)
		if strings.EqualFold(entry.Response.Content.Encoding, "base64") {
			decoded, decodeErr := base64.StdEncoding.DecodeString(entry.Response.Content.Text)
			if decodeErr != nil {
				return savedRequest{}, fmt.Errorf("decode base64 response: %w", decodeErr)
			}
			body = decoded
		}
		responseHeaders := harEntries(entry.Response.Headers)
		example := savedExample{
			name: fmt.Sprintf("%d %s", entry.Response.Status, entry.Response.StatusText), statusCode: entry.Response.Status,
			responseBody: formatResponseBody(body, entry.Response.Content.MIMEType), responseRaw: string(body), responseRawAvailable: true,
			responseHeaders: formatHeaderEntries(responseHeaders), responseMeta: fmt.Sprintf("%d %s", entry.Response.Status, entry.Response.StatusText),
		}
		if entry.Response.CourierExample != nil {
			example = savedExampleFromWorkspace(*entry.Response.CourierExample)
		}
		request.examples = []savedExample{example}
	}
	return request, nil
}

func harEntries(values []harValue) []headerEntry {
	result := make([]headerEntry, 0, len(values))
	for _, value := range values {
		result = append(result, headerEntry{key: value.Name, value: value.Value})
	}
	return result
}

func harRequestHeaders(values []harValue, hasStructuredCookies bool) []headerEntry {
	result := make([]headerEntry, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" || strings.HasPrefix(name, ":") || strings.EqualFold(name, "Content-Length") || (hasStructuredCookies && strings.EqualFold(name, "Cookie")) {
			continue
		}
		result = append(result, headerEntry{key: name, value: value.Value})
	}
	return result
}

func harBody(postData struct {
	MIMEType string     `json:"mimeType"`
	Text     string     `json:"text"`
	Params   []harValue `json:"params"`
}) bodyConfig {
	mimeType := strings.ToLower(postData.MIMEType)
	if strings.Contains(mimeType, "application/x-www-form-urlencoded") {
		return bodyConfig{mode: bodyFormURLEncoded, form: harEntries(postData.Params)}
	}
	rawType := rawText
	switch {
	case strings.Contains(mimeType, "json"):
		rawType = rawJSON
	case strings.Contains(mimeType, "xml"):
		rawType = rawXML
	case strings.Contains(mimeType, "html"):
		rawType = rawHTML
	}
	return bodyConfig{mode: bodyRaw, rawType: rawType, raw: postData.Text}
}

func formatHeaderEntries(entries []headerEntry) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, entry.key+": "+entry.value)
	}
	return strings.Join(lines, "\n")
}

func workspaceExampleFromSaved(example savedExample) workspaceExample {
	return workspaceExample{Name: example.name, StatusCode: example.statusCode, ResponseBody: example.responseBody, ResponseRaw: example.responseRaw, ResponseRawAvailable: example.responseRawAvailable, ResponseHeaders: example.responseHeaders, ResponseMeta: example.responseMeta}
}

func savedExampleFromWorkspace(example workspaceExample) savedExample {
	return savedExample{name: example.Name, statusCode: example.StatusCode, responseBody: example.ResponseBody, responseRaw: example.ResponseRaw, responseRawAvailable: example.ResponseRawAvailable, responseHeaders: example.ResponseHeaders, responseMeta: example.ResponseMeta}
}

func toHARValues(entries []headerEntry) []harValue {
	result := make([]harValue, 0, len(entries))
	for _, entry := range entries {
		result = append(result, harValue{Name: entry.key, Value: entry.value})
	}
	return result
}

func harValuesFromQuery(values url.Values) []harValue {
	result := make([]harValue, 0)
	for key, entries := range values {
		for _, value := range entries {
			result = append(result, harValue{Name: key, Value: value})
		}
	}
	return result
}

func hasHARHeader(headers []harValue, name string) bool {
	return harHeaderValue(headers, name) != ""
}

func harHeaderValue(headers []harValue, name string) string {
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	return ""
}

func parseHARHeaderText(value string) []harValue {
	result := make([]harValue, 0)
	for line := range strings.SplitSeq(value, "\n") {
		name, field, ok := strings.Cut(strings.TrimSuffix(line, "\r"), ":")
		if ok && strings.TrimSpace(name) != "" {
			result = append(result, harValue{Name: strings.TrimSpace(name), Value: strings.TrimSpace(field)})
		}
	}
	return result
}

func writeHARExport(path string, value any) error {
	path, err := expandExportPath(path)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("HAR export target already exists: %s", path)
		}
		return fmt.Errorf("create HAR export: %w", err)
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
		return fmt.Errorf("encode HAR export: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync HAR export: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close HAR export: %w", err)
	}
	success = true
	return nil
}

package tui

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type mockCandidate struct {
	example savedExample
	score   int
	order   int
	values  []headerEntry
}

func (m *model) MockHandler(selector string) (http.Handler, error) {
	requests, err := m.selectSavedRequests(selector)
	if err != nil {
		return nil, err
	}
	exampleCount := 0
	for _, request := range requests {
		exampleCount += len(request.examples)
	}
	if exampleCount == 0 {
		return nil, fmt.Errorf("selected requests contain no saved response examples")
	}
	environment := append([]headerEntry(nil), m.variablesInput.Entries()...)
	return http.HandlerFunc(func(response http.ResponseWriter, incoming *http.Request) {
		m.serveMockResponse(response, incoming, requests, environment)
	}), nil
}

func (m *model) serveMockResponse(response http.ResponseWriter, incoming *http.Request, requests []savedRequest, environment []headerEntry) {
	requestedName := strings.TrimSpace(incoming.Header.Get("X-Mock-Response-Name"))
	requestedCode := 0
	if rawCode := strings.TrimSpace(incoming.Header.Get("X-Mock-Response-Code")); rawCode != "" {
		code, err := strconv.Atoi(rawCode)
		if err != nil || code < 100 || code > 999 {
			http.Error(response, "invalid X-Mock-Response-Code", http.StatusBadRequest)
			return
		}
		requestedCode = code
	}
	matchBody := strings.EqualFold(strings.TrimSpace(incoming.Header.Get("X-Mock-Match-Request-Body")), "true")
	var incomingBody []byte
	if matchBody {
		var err error
		incomingBody, err = io.ReadAll(io.LimitReader(incoming.Body, maxResponseBody+1))
		if err != nil {
			http.Error(response, "read request body", http.StatusBadRequest)
			return
		}
		if len(incomingBody) > maxResponseBody {
			http.Error(response, "request body is too large to match", http.StatusRequestEntityTooLarge)
			return
		}
	}

	baseResolver := newVariableResolver(environment)
	candidates := make([]mockCandidate, 0)
	order := 0
	for _, request := range requests {
		if !strings.EqualFold(request.method, incoming.Method) {
			order += len(request.examples)
			continue
		}
		expectedURL, err := mockSavedRequestURL(baseResolver.Resolve(request.url))
		if err != nil {
			order += len(request.examples)
			continue
		}
		pathScore, captures, matches := mockPathScore(expectedURL.Path, incoming.URL.Path)
		if !matches {
			order += len(request.examples)
			continue
		}
		expectedQuery := expectedURL.Query()
		for _, parameter := range request.params {
			expectedQuery.Add(baseResolver.Resolve(parameter.key), baseResolver.Resolve(parameter.value))
		}
		queryScore := mockQueryScore(expectedQuery, incoming.URL.Query())
		if !mockHeadersMatch(incoming, request, baseResolver) || !mockBodyMatches(incomingBody, matchBody, request, baseResolver) {
			order += len(request.examples)
			continue
		}
		for _, example := range request.examples {
			if requestedName != "" && example.name != requestedName {
				order++
				continue
			}
			status := mockExampleStatus(example)
			if requestedCode != 0 && status != requestedCode {
				order++
				continue
			}
			candidates = append(candidates, mockCandidate{example: example, score: pathScore + queryScore, order: order, values: captures})
			order++
		}
	}
	if len(candidates) == 0 {
		http.Error(response, "no matching saved response example", http.StatusNotFound)
		return
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].score != candidates[right].score {
			return candidates[left].score > candidates[right].score
		}
		leftOK := mockExampleStatus(candidates[left].example) == http.StatusOK
		rightOK := mockExampleStatus(candidates[right].example) == http.StatusOK
		if leftOK != rightOK {
			return leftOK
		}
		return candidates[left].order < candidates[right].order
	})
	selected := candidates[0]
	resolver := newVariableResolver(mergeHeaderEntries(environment, selected.values))
	body := resolver.Resolve(selected.example.responseRaw)
	if !selected.example.responseRawAvailable {
		body = resolver.Resolve(selected.example.responseBody)
	}
	headers, err := parseMockResponseHeaders(selected.example.responseHeaders)
	if err != nil {
		http.Error(response, "saved example contains invalid response headers", http.StatusInternalServerError)
		return
	}
	for name, values := range headers {
		if strings.EqualFold(name, "Content-Length") {
			continue
		}
		for _, value := range values {
			response.Header().Add(name, resolver.Resolve(value))
		}
	}
	response.Header().Set("X-Courier-Mock-Example", selected.example.name)
	status := mockExampleStatus(selected.example)
	response.WriteHeader(status)
	if incoming.Method != http.MethodHead {
		_, _ = io.WriteString(response, body)
	}
}

func mockExampleStatus(example savedExample) int {
	if example.statusCode == 0 {
		return http.StatusOK
	}
	return example.statusCode
}

func mockSavedRequestURL(value string) (*url.URL, error) {
	if target, matched, err := parseUnixSocketURL(value); matched {
		if err != nil {
			return nil, err
		}
		return url.Parse(target.requestURL)
	}
	return url.Parse(value)
}

func mockPathScore(expected, actual string) (int, []headerEntry, bool) {
	if expected == actual {
		return 100, nil, true
	}
	if strings.TrimSuffix(expected, "/") == strings.TrimSuffix(actual, "/") {
		return 95, nil, true
	}
	if strings.EqualFold(expected, actual) {
		return 90, nil, true
	}
	expectedSegments := strings.Split(strings.Trim(expected, "/"), "/")
	actualSegments := strings.Split(strings.Trim(actual, "/"), "/")
	if len(expectedSegments) != len(actualSegments) {
		return 0, nil, false
	}
	captures := make([]headerEntry, 0)
	for index, segment := range expectedSegments {
		if strings.HasPrefix(segment, "{{") && strings.HasSuffix(segment, "}}") {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(segment, "{{"), "}}"))
			if name == "" {
				return 0, nil, false
			}
			value, err := url.PathUnescape(actualSegments[index])
			if err != nil {
				return 0, nil, false
			}
			captures = append(captures, headerEntry{key: name, value: value})
			continue
		}
		if segment != actualSegments[index] {
			return 0, nil, false
		}
	}
	return 80, captures, true
}

func mockQueryScore(expected, actual url.Values) int {
	if len(expected) == 0 && len(actual) == 0 {
		return 20
	}
	score := 0
	for key, expectedValues := range expected {
		actualValues, exists := actual[key]
		if !exists {
			continue
		}
		score++
		for _, expectedValue := range expectedValues {
			if containsString(actualValues, expectedValue) {
				score += 2
			}
		}
	}
	return score
}

func mockHeadersMatch(incoming *http.Request, request savedRequest, resolver *variableResolver) bool {
	names := strings.Split(incoming.Header.Get("X-Mock-Match-Request-Headers"), ",")
	if len(names) == 1 && strings.TrimSpace(names[0]) == "" {
		return true
	}
	expected := make(http.Header)
	for _, header := range request.headers {
		expected.Add(resolver.Resolve(header.key), resolver.Resolve(header.value))
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && incoming.Header.Get(name) != expected.Get(name) {
			return false
		}
	}
	return true
}

func mockBodyMatches(incomingBody []byte, enabled bool, request savedRequest, resolver *variableResolver) bool {
	if !enabled {
		return true
	}
	if request.body.mode != bodyRaw {
		return false
	}
	return string(incomingBody) == resolver.Resolve(request.body.raw)
}

func parseMockResponseHeaders(value string) (http.Header, error) {
	if strings.TrimSpace(value) == "" {
		return make(http.Header), nil
	}
	reader := textproto.NewReader(bufio.NewReader(strings.NewReader(value + "\n")))
	headers, err := reader.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	return http.Header(headers), nil
}

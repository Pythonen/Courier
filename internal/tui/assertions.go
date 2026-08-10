package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type AssertionResult struct {
	Expression string `json:"expression"`
	Expected   string `json:"expected"`
	Actual     string `json:"actual,omitempty"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
}

type assertionResponse struct {
	status   int
	headers  http.Header
	body     []byte
	duration time.Duration
	size     int
}

func newTestsTable() headersTable {
	return newKeyValueTable("Assertion / Action", "Expected / Source", "status / set.token", "200 / json.access_token")
}

func evaluateAssertions(assertions []headerEntry, response assertionResponse) []AssertionResult {
	results := make([]AssertionResult, 0, len(assertions))
	for _, assertion := range assertions {
		result := AssertionResult{Expression: assertion.key, Expected: assertion.value}
		result.Actual, result.Passed, result.Error = evaluateAssertion(assertion.key, assertion.value, response)
		results = append(results, result)
	}
	return results
}

func unavailableAssertionResults(assertions []headerEntry, reason string) []AssertionResult {
	results := make([]AssertionResult, 0, len(assertions))
	for _, assertion := range assertions {
		results = append(results, AssertionResult{Expression: assertion.key, Expected: assertion.value, Error: reason})
	}
	return results
}

func evaluateAssertion(expression, expected string, response assertionResponse) (actual string, passed bool, assertionErr string) {
	expression = strings.TrimSpace(expression)
	switch {
	case strings.HasPrefix(expression, "set."):
		name := strings.TrimSpace(strings.TrimPrefix(expression, "set."))
		if name == "" {
			return "", false, "variable extraction requires a target name"
		}
		actual, err := extractResponseValue(expected, response)
		if err != nil {
			return "", false, err.Error()
		}
		return actual, true, ""
	case expression == "status":
		actual = strconv.Itoa(response.status)
		for expectedStatus := range strings.SplitSeq(expected, ",") {
			if actual == strings.TrimSpace(expectedStatus) {
				return actual, true, ""
			}
		}
		return actual, false, ""
	case expression == "status.class":
		actual = fmt.Sprintf("%dxx", response.status/100)
		return actual, strings.EqualFold(actual, strings.TrimSpace(expected)), ""
	case strings.HasPrefix(expression, "header."):
		name := strings.TrimSpace(strings.TrimPrefix(expression, "header."))
		if name == "" {
			return "", false, "header assertion requires a header name"
		}
		actual = response.headers.Get(name)
		if expected == "*" {
			return actual, actual != "", ""
		}
		return actual, actual == expected, ""
	case expression == "body.contains":
		actual = truncateAssertionValue(string(response.body))
		return actual, strings.Contains(string(response.body), expected), ""
	case expression == "body.matches":
		pattern, err := regexp.Compile(expected)
		if err != nil {
			return "", false, "invalid regular expression: " + err.Error()
		}
		actual = truncateAssertionValue(string(response.body))
		return actual, pattern.Match(response.body), ""
	case strings.HasPrefix(expression, "json."):
		path := strings.TrimPrefix(expression, "json.")
		value, err := jsonPathValue(response.body, path)
		if err != nil {
			return "", false, err.Error()
		}
		actual = stringifyAssertionValue(value)
		return actual, actual == expected, ""
	case expression == "time.lt":
		limit, err := parseAssertionDuration(expected)
		if err != nil {
			return response.duration.String(), false, err.Error()
		}
		actual = response.duration.String()
		return actual, response.duration < limit, ""
	case expression == "size.lt":
		limit, err := parseAssertionBytes(expected)
		if err != nil {
			return strconv.Itoa(response.size), false, err.Error()
		}
		actual = strconv.Itoa(response.size)
		return actual, response.size < limit, ""
	default:
		return "", false, "unknown assertion expression"
	}
}

func extractResponseValue(source string, response assertionResponse) (string, error) {
	source = strings.TrimSpace(source)
	switch {
	case source == "body":
		return string(response.body), nil
	case source == "status":
		return strconv.Itoa(response.status), nil
	case strings.HasPrefix(source, "header."):
		name := strings.TrimSpace(strings.TrimPrefix(source, "header."))
		if name == "" {
			return "", fmt.Errorf("header extraction requires a header name")
		}
		value := response.headers.Get(name)
		if value == "" {
			return "", fmt.Errorf("response header %q was not found", name)
		}
		return value, nil
	case strings.HasPrefix(source, "json."):
		value, err := jsonPathValue(response.body, strings.TrimPrefix(source, "json."))
		if err != nil {
			return "", err
		}
		return stringifyAssertionValue(value), nil
	case strings.HasPrefix(source, "body.matches:"):
		pattern := strings.TrimSpace(strings.TrimPrefix(source, "body.matches:"))
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("invalid extraction regular expression: %w", err)
		}
		match := expression.FindSubmatch(response.body)
		if len(match) == 0 {
			return "", fmt.Errorf("response body did not match extraction expression")
		}
		if len(match) > 1 {
			return string(match[1]), nil
		}
		return string(match[0]), nil
	default:
		return "", fmt.Errorf("unknown extraction source; use json.path, header.Name, body, body.matches:regex, or status")
	}
}

func successfulVariableUpdates(assertions []headerEntry, results []AssertionResult) []headerEntry {
	updates := make([]headerEntry, 0)
	for index, assertion := range assertions {
		if index >= len(results) || !results[index].Passed {
			continue
		}
		expression := strings.TrimSpace(assertion.key)
		if !strings.HasPrefix(expression, "set.") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(expression, "set."))
		if name != "" {
			updates = append(updates, headerEntry{key: name, value: results[index].Actual})
		}
	}
	return updates
}

func jsonPathValue(body []byte, path string) (interface{}, error) {
	var value interface{}
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("response is not valid JSON: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		return value, nil
	}
	for _, segment := range strings.Split(path, ".") {
		name := segment
		if bracket := strings.IndexByte(segment, '['); bracket >= 0 {
			name = segment[:bracket]
		}
		if name != "" {
			object, ok := value.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("JSON path %q expected an object at %q", path, name)
			}
			var exists bool
			value, exists = object[name]
			if !exists {
				return nil, fmt.Errorf("JSON path %q was not found", path)
			}
		}
		remainder := segment[len(name):]
		for remainder != "" {
			if !strings.HasPrefix(remainder, "[") {
				return nil, fmt.Errorf("invalid JSON path %q", path)
			}
			end := strings.IndexByte(remainder, ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid JSON path %q", path)
			}
			index, err := strconv.Atoi(remainder[1:end])
			if err != nil {
				return nil, fmt.Errorf("invalid array index in JSON path %q", path)
			}
			array, ok := value.([]interface{})
			if !ok || index < 0 || index >= len(array) {
				return nil, fmt.Errorf("JSON array index is out of range in path %q", path)
			}
			value = array[index]
			remainder = remainder[end+1:]
		}
	}
	return value, nil
}

func stringifyAssertionValue(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func parseAssertionDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if milliseconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if milliseconds <= 0 {
			return 0, fmt.Errorf("expected duration such as 500ms or 2s")
		}
		return time.Duration(milliseconds) * time.Millisecond, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("expected duration such as 500ms or 2s")
	}
	return duration, nil
}

func parseAssertionBytes(value string) (int, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	multiplier := 1
	for suffix, size := range map[string]int{"KIB": 1024, "KB": 1000, "MIB": 1024 * 1024, "MB": 1000 * 1000} {
		if strings.HasSuffix(value, suffix) {
			multiplier = size
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
			break
		}
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("expected a byte count such as 1024, 10KB, or 2MiB")
	}
	return number * multiplier, nil
}

func formatAssertionResults(results []AssertionResult) string {
	if len(results) == 0 {
		return "No assertions configured for this request."
	}
	lines := make([]string, 0, len(results)+1)
	passed := 0
	for _, result := range results {
		marker := "✗"
		if result.Passed {
			marker = "✓"
			passed++
		}
		line := fmt.Sprintf("%s %s = %s", marker, result.Expression, result.Expected)
		if !result.Passed {
			if result.Error != "" {
				line += " — " + sanitizeTerminalText(result.Error)
			} else {
				line += " — got " + truncateAssertionValue(result.Actual)
			}
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", fmt.Sprintf("%d/%d assertions passed", passed, len(results)))
	return strings.Join(lines, "\n")
}

func truncateAssertionValue(value string) string {
	value = strings.ReplaceAll(sanitizeTerminalText(value), "\n", "\\n")
	const limit = 120
	if len(value) > limit {
		return value[:limit-1] + "…"
	}
	return value
}

package tui

import (
	"context"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	uuid "github.com/google/uuid"
)

type RunOptions struct {
	Selector   string
	Iterations int
	Delay      time.Duration
	Data       []map[string]string
	Bail       bool
}

type RunResult struct {
	Iteration         int               `json:"iteration"`
	Index             int               `json:"index"`
	Name              string            `json:"name"`
	Method            string            `json:"method"`
	URL               string            `json:"url"`
	Status            int               `json:"status,omitempty"`
	DurationMS        int64             `json:"duration_ms"`
	Bytes             int               `json:"bytes"`
	Passed            bool              `json:"passed"`
	Error             string            `json:"error,omitempty"`
	Assertions        []AssertionResult `json:"assertions,omitempty"`
	AssertionFailures int               `json:"assertion_failures,omitempty"`
}

type RunReport struct {
	Results    []RunResult `json:"results"`
	Total      int         `json:"total"`
	Passed     int         `json:"passed"`
	Failed     int         `json:"failed"`
	DurationMS int64       `json:"duration_ms"`
}

func (m *model) RunCollection(ctx context.Context, options RunOptions) (RunReport, error) {
	requests, err := m.selectSavedRequests(options.Selector)
	if err != nil {
		return RunReport{}, err
	}
	if options.Iterations <= 0 {
		return RunReport{}, fmt.Errorf("iterations must be at least 1")
	}
	if options.Delay < 0 {
		return RunReport{}, fmt.Errorf("runner delay cannot be negative")
	}
	iterationCount := options.Iterations
	if len(options.Data) > 0 {
		iterationCount *= len(options.Data)
	}

	baseEnvironment := m.variablesInput.Entries()
	defer func() {
		m.variablesInput.SetEntries(baseEnvironment)
		m.syncActiveEnvironment()
	}()
	report := RunReport{Results: make([]RunResult, 0, len(requests)*iterationCount)}
	started := time.Now()
	sequence := 0
runLoop:
	for iteration := 1; iteration <= iterationCount; iteration++ {
		if len(options.Data) > 0 {
			m.variablesInput.SetEntries(mergeIterationData(baseEnvironment, options.Data[(iteration-1)%len(options.Data)]))
		}
		for index, request := range requests {
			if sequence > 0 && options.Delay > 0 {
				timer := time.NewTimer(options.Delay)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					report.Total = len(report.Results)
					report.DurationMS = time.Since(started).Milliseconds()
					return report, ctx.Err()
				case <-timer.C:
				}
			}
			select {
			case <-ctx.Done():
				report.Total = len(report.Results)
				report.DurationMS = time.Since(started).Milliseconds()
				return report, ctx.Err()
			default:
			}

			m.applySavedRequest(request)
			m.requestId = uuid.New()
			m.requestContext = ctx
			resolvedURL := newVariableResolver(m.variablesInput.Entries()).Resolve(request.url)
			if isWebSocketURL(resolvedURL) {
				report.Results = append(report.Results, RunResult{
					Iteration: iteration, Index: index + 1, Name: request.displayName(), Method: "WS", URL: resolvedURL,
					Passed: false, Error: "interactive WebSocket sessions are not executed by the headless collection runner",
				})
				report.Failed++
				sequence++
				if options.Bail {
					break runLoop
				}
				continue
			}
			if isMQTTURL(resolvedURL) {
				report.Results = append(report.Results, RunResult{
					Iteration: iteration, Index: index + 1, Name: request.displayName(), Method: "MQTT", URL: resolvedURL,
					Passed: false, Error: "interactive MQTT sessions are not executed by the headless collection runner",
				})
				report.Failed++
				sequence++
				if options.Bail {
					break runLoop
				}
				continue
			}
			if isSocketIOURL(resolvedURL) {
				report.Results = append(report.Results, RunResult{
					Iteration: iteration, Index: index + 1, Name: request.displayName(), Method: "SOCKET.IO", URL: resolvedURL,
					Passed: false, Error: "interactive Socket.IO sessions are not executed by the headless collection runner",
				})
				report.Failed++
				sequence++
				if options.Bail {
					break runLoop
				}
				continue
			}
			response := m.DoRequest()().(responseMsg)
			response = consumeResponseStream(response)
			if ctx.Err() != nil {
				report.Total = len(report.Results)
				report.DurationMS = time.Since(started).Milliseconds()
				return report, ctx.Err()
			}
			if len(response.variableUpdates) > 0 {
				baseEnvironment = mergeHeaderEntries(baseEnvironment, response.variableUpdates)
				m.variablesInput.SetEntries(mergeHeaderEntries(m.variablesInput.Entries(), response.variableUpdates))
			}
			result := RunResult{
				Iteration:  iteration,
				Index:      index + 1,
				Name:       request.displayName(),
				Method:     request.method,
				URL:        response.finalURL,
				Status:     response.statusCode,
				DurationMS: response.duration.Milliseconds(),
				Bytes:      response.responseBytes,
				Assertions: response.assertionResults,
			}
			if isGRPCURL(resolvedURL) {
				result.Method = "GRPC"
			}
			for _, assertion := range result.Assertions {
				if !assertion.Passed {
					result.AssertionFailures++
				}
			}
			result.Passed = response.statusCode >= 200 && response.statusCode < 400 && result.AssertionFailures == 0
			if result.URL == "" {
				result.URL = request.url
			}
			if response.statusCode == 0 {
				result.Error = strings.TrimSpace(ansi.Strip(response.responseBody))
			}
			report.Results = append(report.Results, result)
			if result.Passed {
				report.Passed++
			} else {
				report.Failed++
			}
			sequence++
			if options.Bail && !result.Passed {
				break runLoop
			}
		}
	}
	report.Total = len(report.Results)
	report.DurationMS = time.Since(started).Milliseconds()
	return report, nil
}

type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Time      string          `xml:"time,attr"`
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

func FormatRunReportJUnit(report RunReport) (string, error) {
	suite := junitTestSuite{Name: "Courier collection", Tests: report.Total, Time: junitSeconds(report.DurationMS)}
	for _, result := range report.Results {
		testCase := junitTestCase{
			Name: result.Name, ClassName: fmt.Sprintf("iteration.%d", result.Iteration), Time: junitSeconds(result.DurationMS),
		}
		if !result.Passed {
			if result.Status == 0 {
				suite.Errors++
				testCase.Error = &junitProblem{Message: "request error", Type: "transport", Body: result.Error}
			} else {
				suite.Failures++
				details := make([]string, 0, result.AssertionFailures+1)
				if result.Status < 200 || result.Status >= 400 {
					details = append(details, fmt.Sprintf("HTTP status %d", result.Status))
				}
				for _, assertion := range result.Assertions {
					if assertion.Passed {
						continue
					}
					detail := assertion.Expression + " expected " + assertion.Expected
					if assertion.Error != "" {
						detail += ": " + assertion.Error
					} else if assertion.Actual != "" {
						detail += ", got " + assertion.Actual
					}
					details = append(details, detail)
				}
				message := "request failed"
				failureType := "http"
				if result.AssertionFailures > 0 {
					message = fmt.Sprintf("%d assertion(s) failed", result.AssertionFailures)
					failureType = "assertion"
				}
				testCase.Failure = &junitProblem{Message: message, Type: failureType, Body: strings.Join(details, "\n")}
			}
		}
		suite.TestCases = append(suite.TestCases, testCase)
	}
	document := junitTestSuites{
		Name: "Courier", Tests: report.Total, Failures: suite.Failures, Errors: suite.Errors,
		Time: junitSeconds(report.DurationMS), Suites: []junitTestSuite{suite},
	}
	encoded, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode JUnit report: %w", err)
	}
	return xml.Header + string(encoded) + "\n", nil
}

func junitSeconds(milliseconds int64) string {
	return strconv.FormatFloat(float64(milliseconds)/1000, 'f', 3, 64)
}

func mergeIterationData(environment []headerEntry, data map[string]string) []headerEntry {
	merged := append([]headerEntry(nil), environment...)
	for key, value := range data {
		merged = append(merged, headerEntry{key: key, value: value})
	}
	return merged
}

func (m *model) selectSavedRequests(selector string) ([]savedRequest, error) {
	selector = strings.TrimSpace(selector)
	if len(m.savedRequests) == 0 {
		return nil, fmt.Errorf("workspace contains no saved requests")
	}
	if selector == "" || strings.EqualFold(selector, "all") {
		return append([]savedRequest(nil), m.savedRequests...), nil
	}
	if index, err := strconv.Atoi(selector); err == nil {
		if index < 1 || index > len(m.savedRequests) {
			return nil, fmt.Errorf("saved request index must be between 1 and %d", len(m.savedRequests))
		}
		return []savedRequest{m.savedRequests[index-1]}, nil
	}
	for _, request := range m.savedRequests {
		if request.name == selector {
			return []savedRequest{request}, nil
		}
	}
	prefix := strings.TrimSuffix(selector, " / ") + " / "
	var matches []savedRequest
	for _, request := range m.savedRequests {
		if strings.HasPrefix(request.name, prefix) {
			matches = append(matches, request)
		}
	}
	if len(matches) > 0 {
		return matches, nil
	}
	return nil, fmt.Errorf("saved request or folder %q was not found", selector)
}

func FormatRunReport(report RunReport) string {
	var output strings.Builder
	for _, result := range report.Results {
		state := "PASS"
		status := strconv.Itoa(result.Status)
		if !result.Passed {
			state = "FAIL"
			if result.Status == 0 {
				status = "ERROR"
			}
		}
		fmt.Fprintf(&output, "%s [%d.%d] %s  %s %s  %dms  %s\n", state, result.Iteration, result.Index, result.Name, result.Method, status, result.DurationMS, formatByteCount(result.Bytes))
		if result.Error != "" {
			fmt.Fprintf(&output, "  %s\n", result.Error)
		}
		for _, assertion := range result.Assertions {
			marker := "✓"
			if !assertion.Passed {
				marker = "✗"
			}
			fmt.Fprintf(&output, "  %s %s = %s", marker, assertion.Expression, assertion.Expected)
			if !assertion.Passed {
				if assertion.Error != "" {
					fmt.Fprintf(&output, " — %s", assertion.Error)
				} else {
					fmt.Fprintf(&output, " — got %s", truncateAssertionValue(assertion.Actual))
				}
			}
			output.WriteByte('\n')
		}
	}
	fmt.Fprintf(&output, "\n%d requests: %d passed, %d failed in %dms", report.Total, report.Passed, report.Failed, report.DurationMS)
	return output.String()
}

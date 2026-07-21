package tui

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	tea "charm.land/bubbletea/v2"
	uuid "github.com/google/uuid"
)

const maxResponseBody = 10 << 20 // 10 MiB

type responseMsg struct {
	requestID       uuid.UUID
	responseBody    string
	responseHeaders string
	responseMeta    string
}

func (m model) DoRequest() tea.Cmd {
	return func() tea.Msg {
		requestID := m.requestId
		method := methods[m.methodIdx]
		url := m.urlInput.Value()

		var bodyReader io.Reader
		if m.bodyInput.Value() != "" {
			bodyReader = bytes.NewBufferString(m.bodyInput.Value())
		}

		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error creating request: %v", err), responseMeta: "Request failed"}
		}

		// Add preserves repeated header names entered on separate rows.
		for _, header := range m.headersInput.Entries() {
			req.Header.Add(header.key, header.value)
		}

		client := m.client
		if client == nil {
			client = &http.Client{Timeout: 30 * time.Second}
		}
		started := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(started)
		if err != nil {
			return responseMsg{
				requestID:    requestID,
				responseBody: fmt.Sprintf("Error: %v", err),
				responseMeta: fmt.Sprintf("Request failed • %s", elapsed.Round(time.Millisecond)),
			}
		}
		defer resp.Body.Close() //nolint:errcheck

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
		if err != nil {
			return responseMsg{
				requestID:    requestID,
				responseBody: fmt.Sprintf("Error reading response: %v", err),
				responseMeta: responseMeta(resp, elapsed, 0, url),
			}
		}
		if len(body) > maxResponseBody {
			return responseMsg{
				requestID:       requestID,
				responseBody:    fmt.Sprintf("Response body exceeds the %s display limit.", formatByteCount(maxResponseBody)),
				responseHeaders: formatHeaders(resp.Header),
				responseMeta:    responseMeta(resp, elapsed, len(body), url),
			}
		}

		return responseMsg{
			requestID:       requestID,
			responseBody:    formatResponseBody(body, resp.Header.Get("Content-Type")),
			responseHeaders: formatHeaders(resp.Header),
			responseMeta:    responseMeta(resp, elapsed, len(body), url),
		}
	}
}

func responseMeta(resp *http.Response, elapsed time.Duration, size int, requestedURL string) string {
	meta := fmt.Sprintf("%s • %s • %s", resp.Status, elapsed.Round(time.Millisecond), formatByteCount(size))
	if resp.Request != nil && resp.Request.URL != nil && resp.Request.URL.String() != requestedURL {
		meta += " • → " + resp.Request.URL.String()
	}
	return meta
}

func formatByteCount(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
}

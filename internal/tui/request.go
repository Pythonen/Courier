package tui

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	tea "charm.land/bubbletea/v2"
)

type responseMsg struct {
	responseBody    string
	responseHeaders string
}

func (m model) DoRequest() tea.Cmd {
	return func() tea.Msg {
		method := methods[m.methodIdx]
		url := m.urlInput.Value()

		var bodyReader io.Reader
		if method == "POST" || method == "PUT" || method == "PATCH" {
			bodyReader = bytes.NewBufferString(m.bodyInput.Value())
		}

		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			return responseMsg{responseBody: fmt.Sprintf("Error creating request: %v", err)}
		}

		// Apply user-defined headers
		for k, v := range m.headersInput.Headers() {
			req.Header.Set(k, v)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return responseMsg{responseBody: fmt.Sprintf("Error: %v", err)}
		}
		defer resp.Body.Close() //nolint:errcheck

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return responseMsg{responseBody: fmt.Sprintf("Error reading response: %v", err)}
		}

		return responseMsg{
			responseBody:    formatResponseBody(body, resp.Header.Get("Content-Type")),
			responseHeaders: formatHeaders(resp.Header),
		}
	}
}

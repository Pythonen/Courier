package main

import (
	"io"
	"log"
	"net/http"

	tea "github.com/charmbracelet/bubbletea"
)

type responseMsg struct {
	responseBody    string
	responseHeaders string
}

func (m model) DoRequest() tea.Cmd {
	return func() tea.Msg {
		method := methods[m.methodIdx]
		url := m.urlInput.Value()
		if method == "GET" {
			resp, err := http.Get(url)
			if err != nil {
				log.Fatalln("Something went wrong", err)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Fatalln(err)
			}
			return responseMsg{
				responseBody:    formatResponseBody(body, resp.Header.Get("Content-Type")),
				responseHeaders: formatHeaders(resp.Header)}
		}
		return nil
	}
}

package main

import (
	"bytes"
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
		} else if method == "PATCH" || method == "PUT" || method == "POST" {
			req, err := http.NewRequest(method, url, bytes.NewBuffer([]byte(m.bodyInput.Value())))
			// TODO: Configure this via headers pane
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{}
			resp, err := client.Do(req)
			body, err := io.ReadAll(resp.Body)
			defer resp.Body.Close()
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

package main

import (
	"sync"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/golden"
	uuid "github.com/google/uuid"
	"github.com/muesli/termenv"
)

var configureSnapshotRendererOnce sync.Once

func configureSnapshotRenderer() {
	configureSnapshotRendererOnce.Do(func() {
		lipgloss.SetColorProfile(termenv.Ascii)
		lipgloss.SetHasDarkBackground(true)
	})
}

func TestViewGolden_URLPane(t *testing.T) {
	t.Parallel()
	configureSnapshotRenderer()

	m := newModel()
	m.width = 120
	m.height = 40
	m.sizeComponents()
	m.setFocus(paneURL)
	m.methodIdx = methodIndex(t, "PATCH")
	m.urlInput.SetValue("https://api.example.com/v1/users?active=true")

	view := m.viewURL(m.width - historyWidth - 4)
	golden.RequireEqual(t, []byte(view))
}

func TestViewGolden_RequestPane(t *testing.T) {
	t.Parallel()
	configureSnapshotRenderer()

	m := newModel()
	m.width = 120
	m.height = 40
	m.sizeComponents()
	m.setFocus(paneRequest)
	m.requestTab = requestTabHeaders
	m.inputMode = modeInsert

	m.headersInput.rows[0].key.SetValue("Authorization")
	m.headersInput.rows[0].value.SetValue("Bearer token")
	m.headersInput.UpdateNormal("o")
	m.headersInput.rows[1].key.SetValue("Content-Type")
	m.headersInput.rows[1].value.SetValue("application/json")
	m.headersInput.cursorRow = 1
	m.headersInput.cursorCol = 1

	view := m.viewRequest(m.width-historyWidth-4, 12)
	golden.RequireEqual(t, []byte(view))
}

func TestViewGolden_ResponsePane(t *testing.T) {
	t.Parallel()
	configureSnapshotRenderer()

	m := newModel()
	m.width = 120
	m.height = 40
	m.sizeComponents()
	m.setFocus(paneResponse)
	m.responseTab = responseTabHeaders
	m.response = "{\n  \"ok\": true\n}"
	m.responseHeaders = "Content-Type: application/json\nX-Request-Id: abc-123\n"
	m.responseModel.SetContent(m.response)
	m.responseHeadersModel.SetContent(m.responseHeaders)

	view := m.viewResponse(m.width-historyWidth-4, 14)
	golden.RequireEqual(t, []byte(view))
}

func TestViewGolden_HistoryPane(t *testing.T) {
	t.Parallel()
	configureSnapshotRenderer()

	m := newModel()
	m.width = 120
	m.height = 40
	m.sizeComponents()
	m.setFocus(paneHistory)
	m.history = []historyItem{
		{
			method:             "GET",
			url:                "https://api.example.com/v1/health",
			requestComponents:  map[string]string{"body": ""},
			responseComponents: map[string]string{"body": "ok", "headers": "x: 1"},
			requestId:          uuid.New(),
		},
		{
			method:             "POST",
			url:                "https://api.example.com/v1/users",
			requestComponents:  map[string]string{"body": "{\"name\":\"Ada\"}"},
			responseComponents: map[string]string{"body": "created", "headers": "x: 2"},
			requestId:          uuid.New(),
		},
		{
			method:             "DELETE",
			url:                "https://api.example.com/v1/users/1234567890",
			requestComponents:  map[string]string{"body": ""},
			responseComponents: map[string]string{"body": "deleted", "headers": "x: 3"},
			requestId:          uuid.New(),
		},
	}
	m.historyPos = 1

	view := m.viewHistory(20)
	golden.RequireEqual(t, []byte(view))
}

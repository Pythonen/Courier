package tui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestSanitizeTerminalTextEscapesTerminalControls(t *testing.T) {
	input := "safe\r\n\t\x00\x07\x1b]52;c;Y2xpcGJvYXJk\x07\x1b[2J\x1bPpayload\x1b\\\u009b2J\u202eend" + string([]byte{0xff})
	got := sanitizeTerminalText(input)

	for _, unsafe := range []string{"\r", "\t", "\x00", "\x07", "\x1b", "\u009b", "\u202e", string([]byte{0xff})} {
		if strings.Contains(got, unsafe) {
			t.Fatalf("sanitized value still contains unsafe bytes %q: %q", unsafe, got)
		}
	}
	for _, visible := range []string{`\t`, `\x00`, `\x07`, `\x1b]52`, `\x1b[2J`, `\x1bP`, `\u009b`, `\u202e`, `\xff`} {
		if !strings.Contains(got, visible) {
			t.Fatalf("sanitized value does not expose %q: %q", visible, got)
		}
	}
	if !strings.HasPrefix(got, "safe\n") {
		t.Fatalf("CRLF was not normalized: %q", got)
	}
}

func TestFormattedResponseSanitizesBeforeAddingANSI(t *testing.T) {
	display := formatResponseBody([]byte("hello\x1b]52;c;c2VjcmV0\x07\x1b[2J"), "text/plain")
	for _, unsafe := range []string{"\x1b]", "\x1b[2J", "\x07"} {
		if strings.Contains(display, unsafe) {
			t.Fatalf("formatted response contains unsafe sequence %q: %q", unsafe, display)
		}
	}
	plain := ansi.Strip(display)
	if !strings.Contains(plain, `\x1b]52`) || !strings.Contains(plain, `\x07`) || !strings.Contains(plain, `\x1b[2J`) {
		t.Fatalf("formatted response does not show escaped controls: %q", plain)
	}
}

func TestStreamingResponseKeepsRawAndSanitizesDisplay(t *testing.T) {
	m := NewModel()
	requestID := uuid.New()
	m.requestId = requestID
	m.history = []historyItem{{requestID: requestID}}
	chunk := "data: before\x1b]52;c;c2VjcmV0\x07after\n\n"

	updated, _ := m.Update(responseStreamMsg{requestID: requestID, chunk: chunk})
	m = updated.(model)

	if m.responseRaw != chunk || m.history[0].responseRaw != chunk {
		t.Fatalf("raw stream was not retained: response=%q history=%q", m.responseRaw, m.history[0].responseRaw)
	}
	if strings.Contains(m.response, "\x1b") || strings.Contains(m.history[0].responseBody, "\x1b") {
		t.Fatalf("stream display contains an escape byte: response=%q history=%q", m.response, m.history[0].responseBody)
	}
	if !strings.Contains(m.response, `\x1b]52`) {
		t.Fatalf("stream display does not expose escaped control: %q", m.response)
	}
}

func TestWebSocketTranscriptKeepsRawAndSanitizesDisplay(t *testing.T) {
	m := NewModel()
	requestID := uuid.New()
	m.requestId = requestID
	m.history = []historyItem{{requestID: requestID}}
	entry := formatWebSocketTranscript("←", websocket.TextMessage, []byte("before\x1b[2Jafter"))

	if err := m.appendWebSocketEntry(requestID, entry); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.responseRaw, "\x1b[2J") || !strings.Contains(m.history[0].responseRaw, "\x1b[2J") {
		t.Fatalf("raw WebSocket transcript was not retained: response=%q history=%q", m.responseRaw, m.history[0].responseRaw)
	}
	if strings.Contains(m.response, "\x1b") || strings.Contains(m.history[0].responseBody, "\x1b") {
		t.Fatalf("WebSocket display contains an escape byte: response=%q history=%q", m.response, m.history[0].responseBody)
	}
}

func TestHeadersAndAssertionsSanitizeRemoteValues(t *testing.T) {
	headers := formatHeaders(http.Header{"X-Test": {"before\x1b]0;spoofed\x07after"}})
	if strings.Contains(headers, "\x1b") || strings.Contains(headers, "\x07") || !strings.Contains(headers, `\x1b]0`) {
		t.Fatalf("unsafe formatted headers: %q", headers)
	}

	results := formatAssertionResults([]AssertionResult{{Expression: "body", Expected: "ok", Actual: "bad\x1b[2J", Passed: false}})
	if strings.Contains(results, "\x1b") || !strings.Contains(results, `\x1b[2J`) {
		t.Fatalf("unsafe assertion output: %q", results)
	}
}

package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uuid "github.com/google/uuid"
)

func TestEventStreamArrivesIncrementallyAndCompletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("test response does not support flushing")
			return
		}
		_, _ = w.Write([]byte("event: greeting\ndata: one\n\n"))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("id: 2\ndata: two\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	m := NewModel()
	m.requestId = uuid.New()
	m.urlInput.SetValue(server.URL)
	m.testsInput.SetEntries([]headerEntry{{key: "status", value: "200"}, {key: "body.contains", value: "data: two"}})
	initial := m.DoRequest()().(responseMsg)
	if initial.stream == nil || !strings.Contains(initial.responseMeta, "streaming") || initial.statusCode != 200 {
		t.Fatalf("initial stream response = %#v", initial)
	}
	final := consumeResponseStream(initial)
	if final.stream != nil || final.responseRaw != "event: greeting\ndata: one\n\nid: 2\ndata: two\n\n" || final.responseBytes != len(final.responseRaw) {
		t.Fatalf("final stream response = %#v", final)
	}
	if len(final.assertionResults) != 2 || !final.assertionResults[0].Passed || !final.assertionResults[1].Passed {
		t.Fatalf("stream assertions = %#v", final.assertionResults)
	}
}

func TestEventStreamUpdatesTUIWithoutChangingPaneHeight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, event := range []string{"data: first\n\n", "data: second\n\n"} {
			_, _ = w.Write([]byte(event))
			flusher.Flush()
		}
	}))
	defer server.Close()

	initTestZones()
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(model)
	m.requestId = uuid.New()
	m.urlInput.SetValue(server.URL)
	m.history = []historyItem{{requestID: m.requestId, method: "GET", url: server.URL, requestBodyConfig: bodyConfig{mode: bodyNone}}}
	initial := m.DoRequest()().(responseMsg)
	updated, _ = m.Update(initial)
	m = updated.(model)
	mainWidth, _, _, responseHeight := layoutDimensions(m.width, m.height)
	wantHeight := lipgloss.Height(m.viewResponse(mainWidth, responseHeight))

	for {
		message, ok := <-initial.stream
		if !ok {
			break
		}
		message.stream = initial.stream
		updated, _ = m.Update(message)
		m = updated.(model)
		if got := lipgloss.Height(m.viewResponse(mainWidth, responseHeight)); got != wantHeight {
			t.Fatalf("stream changed response height: got %d want %d", got, wantHeight)
		}
		if message.final != nil {
			break
		}
	}
	if !strings.Contains(m.response, "data: first") || !strings.Contains(m.response, "data: second") || m.cancelRequest != nil {
		t.Fatalf("streamed TUI response = %q cancel=%v", m.response, m.cancelRequest != nil)
	}
	if len(m.history) != 1 || m.history[0].responseRaw != m.responseRaw {
		t.Fatalf("stream history = %#v", m.history)
	}
}

func TestEventStreamCancellationTerminatesReader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: connected\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	m := NewModel()
	m.requestId = uuid.New()
	m.requestContext = ctx
	m.urlInput.SetValue(server.URL)
	initial := m.DoRequest()().(responseMsg)
	cancel()
	final := consumeResponseStream(initial)
	if !strings.Contains(final.responseMeta, "Cancelled") {
		t.Fatalf("cancelled stream response = %#v", final)
	}
}

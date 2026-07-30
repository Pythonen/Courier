package tui

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gorilla/websocket"
)

func TestWebSocketSessionReusesRequestConfigurationAndStreamsTranscript(t *testing.T) {
	handshake := make(chan *http.Request, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handshake <- r.Clone(r.Context())
		connection, err := upgrader.Upgrade(w, r, http.Header{"X-Handshake": {"yes"}})
		if err != nil {
			return
		}
		defer connection.Close() //nolint:errcheck
		messageType, payload, err := connection.ReadMessage()
		if err == nil {
			_ = connection.WriteMessage(messageType, append([]byte("echo:"), payload...))
		}
		_, _, _ = connection.ReadMessage()
	}))
	defer server.Close()

	m := NewModel()
	initTestZones()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(model)
	m.urlInput.SetValue("ws" + strings.TrimPrefix(server.URL, "http") + "/socket")
	m.paramsInput.SetEntries([]headerEntry{{key: "room", value: "courier"}})
	m.headersInput.SetEntries([]headerEntry{{key: "X-Client", value: "terminal"}})
	m.cookiesInput.SetEntries([]headerEntry{{key: "session", value: "cookie-token"}})
	m.authInput.SetConfig(authConfig{typeID: authBearer, bearerToken: "bearer-token"})
	m.bodyInput.SetValue("hello")
	m.testsInput.SetEntries([]headerEntry{{key: "status", value: "101"}, {key: "body.contains", value: "echo:hello"}})

	updated, connect := m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m = updated.(model)
	if connect == nil || m.webSocketCancel == nil {
		t.Fatal("Ctrl+K did not start a WebSocket connection")
	}
	connected := connect().(webSocketConnectedMsg)
	if connected.err != nil {
		t.Fatal(connected.err)
	}
	updated, _ = m.Update(connected)
	m = updated.(model)
	mainWidth, _, _, responseHeight := layoutDimensions(m.width, m.height)
	wantHeight := lipgloss.Height(m.viewResponse(mainWidth, responseHeight))
	if m.webSocket == nil || m.responseStatusCode != http.StatusSwitchingProtocols || !strings.Contains(m.responseHeaders, "X-Handshake: yes") {
		t.Fatalf("connected model = socket %v status %d headers %q", m.webSocket != nil, m.responseStatusCode, m.responseHeaders)
	}
	request := <-handshake
	if request.URL.Query().Get("room") != "courier" || request.Header.Get("X-Client") != "terminal" || request.Header.Get("Authorization") != "Bearer bearer-token" {
		t.Fatalf("handshake request = %s headers=%v", request.URL.String(), request.Header)
	}
	if cookie, err := request.Cookie("session"); err != nil || cookie.Value != "cookie-token" {
		t.Fatalf("handshake cookie = %#v err=%v", cookie, err)
	}

	messageType, payload, err := m.webSocketPayload()
	if err != nil {
		t.Fatal(err)
	}
	sent := sendWebSocketMessage(m.webSocket, messageType, payload)().(webSocketSentMsg)
	updated, _ = m.Update(sent)
	m = updated.(model)
	incoming := <-connected.events
	incoming.events = connected.events
	updated, _ = m.Update(incoming)
	m = updated.(model)
	if got := lipgloss.Height(m.viewResponse(mainWidth, responseHeight)); got != wantHeight {
		t.Fatalf("WebSocket message changed response height: got %d want %d", got, wantHeight)
	}
	if !strings.Contains(m.response, "→ TEXT 5 B\nhello") || !strings.Contains(m.response, "← TEXT 10 B\necho:hello") {
		t.Fatalf("WebSocket transcript = %q", m.response)
	}

	closeWebSocketSession(m.webSocket)
	closed := <-connected.events
	closed.events = connected.events
	updated, _ = m.Update(closed)
	m = updated.(model)
	if m.webSocket != nil || !strings.Contains(m.responseMeta, "WebSocket disconnected") || len(m.assertionResults) != 2 || !m.assertionResults[0].Passed || !m.assertionResults[1].Passed {
		t.Fatalf("closed WebSocket = socket %v meta %q assertions %#v", m.webSocket != nil, m.responseMeta, m.assertionResults)
	}
	if len(m.history) != 1 || m.history[0].responseRaw != m.responseRaw {
		t.Fatalf("WebSocket history = %#v", m.history)
	}
}

func TestSecureWebSocketUsesCustomCABundle(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = connection.WriteMessage(websocket.TextMessage, []byte("secure"))
		_ = connection.Close()
	}))
	defer server.Close()

	caPath := filepath.Join(t.TempDir(), "server-ca.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.settings.SetConfig(requestSettings{followRedirects: true, timeout: 5 * time.Second, caCertPath: caPath})
	m.urlInput.SetValue("wss" + strings.TrimPrefix(server.URL, "https"))
	connected := m.beginWebSocketConnection()().(webSocketConnectedMsg)
	if connected.err != nil {
		t.Fatal(connected.err)
	}
	event := <-connected.events
	if event.err != nil || string(event.payload) != "secure" {
		t.Fatalf("secure WebSocket event = %#v", event)
	}
	closeWebSocketSession(connected.session)
}

func TestWebSocketBinaryPayloadAndTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.bin")
	if err := os.WriteFile(path, []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.bodyMode = bodyBinary
	m.binaryPathInput.SetValue(path)
	messageType, payload, err := m.webSocketPayload()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage || string(payload) != string([]byte{0, 1, 2, 3}) {
		t.Fatalf("binary WebSocket payload = type %d bytes %v", messageType, payload)
	}
	if transcript := formatWebSocketTranscript("→", messageType, payload); !strings.Contains(transcript, "BINARY 4 B") || !strings.Contains(transcript, "AAECAw==") {
		t.Fatalf("binary transcript = %q", transcript)
	}
}

func TestWebSocketHandshakeFailureIncludesServerResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "access denied", http.StatusUnauthorized)
	}))
	defer server.Close()
	m := NewModel()
	m.urlInput.SetValue("ws" + strings.TrimPrefix(server.URL, "http"))
	message := m.beginWebSocketConnection()().(webSocketConnectedMsg)
	if message.err == nil || !strings.Contains(message.err.Error(), "401 Unauthorized") || !strings.Contains(message.err.Error(), "access denied") {
		t.Fatalf("handshake error = %v", message.err)
	}
}

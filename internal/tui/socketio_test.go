package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSocketIONativeSession(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/custom/" || request.URL.Query().Get("EIO") != "4" || request.URL.Query().Get("transport") != "websocket" || request.URL.Query().Get("token") != "abc" {
			t.Errorf("handshake URL = %s", request.URL.String())
		}
		if request.Header.Get("X-Test") != "yes" {
			t.Errorf("handshake header = %q", request.Header.Get("X-Test"))
		}
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_ = connection.WriteMessage(websocket.TextMessage, []byte(`0{"sid":"test","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`))
		_, connect, err := connection.ReadMessage()
		if err != nil || string(connect) != `40/chat,{"secret":"value"}` {
			t.Errorf("namespace connect = %q (%v)", connect, err)
			return
		}
		_ = connection.WriteMessage(websocket.TextMessage, []byte(`40/chat,{"sid":"namespace"}`))
		_ = connection.WriteMessage(websocket.TextMessage, []byte(`42/chat,["welcome",{"ok":true}]`))
		_, event, err := connection.ReadMessage()
		if err == nil {
			received <- string(event)
		}
	}))
	defer server.Close()

	m := NewModel()
	m.urlInput.SetValue(strings.Replace(server.URL, "http://", "socketio://", 1) + `?token=abc&event=hello&namespace=/chat&handshake_path=/custom/&auth={"secret":"value"}`)
	m.headersInput.SetEntries([]headerEntry{{key: "X-Test", value: "yes"}})
	connected := m.beginSocketIOConnection()().(socketIOConnectedMsg)
	if connected.err != nil || connected.session == nil {
		t.Fatalf("connect = %#v", connected)
	}
	defer closeSocketIOSession(connected.session)

	incoming := waitSocketIOEvent(connected.session)().(socketIOEventMsg)
	if incoming.err != nil || incoming.event != "welcome" || len(incoming.args) != 1 || string(incoming.args[0]) != `{"ok":true}` {
		t.Fatalf("incoming event = %#v", incoming)
	}
	sent := sendSocketIOEvent(connected.session, connected.session.config.event, socketIOArgs([]byte(`[{"id":1},"two"]`)))().(socketIOSentMsg)
	if sent.err != nil {
		t.Fatal(sent.err)
	}
	select {
	case packet := <-received:
		if packet != `42/chat,["hello",{"id":1},"two"]` {
			t.Fatalf("event packet = %q", packet)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not receive Socket.IO event")
	}
}

func TestParseSocketIOEventWithAckID(t *testing.T) {
	event, args, ok := parseSocketIOEvent([]byte(`42/admin,17["updated",1,true]`))
	if !ok || event != "updated" || len(args) != 2 || string(args[0]) != "1" || string(args[1]) != "true" {
		t.Fatalf("parsed = event %q args %q ok %t", event, args, ok)
	}
}

func TestSocketIOArgs(t *testing.T) {
	if got := socketIOArgs([]byte("plain")); len(got) != 1 || string(got[0]) != `"plain"` {
		t.Fatalf("plain args = %q", got)
	}
	if got := socketIOArgs([]byte(`{"id":1}`)); len(got) != 1 || string(got[0]) != `{"id":1}` {
		t.Fatalf("object args = %q", got)
	}
}

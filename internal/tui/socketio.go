package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type socketIOConfig struct {
	url       string
	namespace string
	event     string
	auth      json.RawMessage
}

type socketIOSession struct {
	connection *websocket.Conn
	requestID  uuid.UUID
	url        string
	started    time.Time
	config     socketIOConfig
	headers    http.Header
	assertions []headerEntry
	events     <-chan socketIOEventMsg
	context    context.Context
	cancel     context.CancelFunc
	writes     sync.Mutex
	closeOnce  sync.Once
}

type socketIOConnectedMsg struct {
	requestID uuid.UUID
	session   *socketIOSession
	duration  time.Duration
	err       error
}

type socketIOEventMsg struct {
	requestID uuid.UUID
	event     string
	args      []json.RawMessage
	err       error
	session   *socketIOSession
}

type socketIOSentMsg struct {
	requestID uuid.UUID
	event     string
	args      []json.RawMessage
	err       error
}

func isSocketIOURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (strings.EqualFold(parsed.Scheme, "socketio") || strings.EqualFold(parsed.Scheme, "socketios"))
}

func (m *model) beginSocketIOConnection() tea.Cmd {
	if m.socketIO != nil {
		closeSocketIOSession(m.socketIO)
		m.socketIO = nil
	}
	if m.socketIOCancel != nil {
		m.socketIOCancel()
	}
	requestID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	m.requestId, m.requestContext, m.socketIOCancel = requestID, ctx, cancel
	m.cancelRequest = cancel
	m.response, m.responseRaw, m.responseHeaders, m.responseTests = "", "", "", ""
	m.responseRawAvailable, m.responseStatusCode = true, 0
	m.responseMeta = "Connecting Socket.IO..."
	m.responseModel.SetContent("")
	m.responseHeadersModel.SetContent("")
	m.responseTestsModel.SetContent("")
	m.historyPos = 0
	m.history = append([]historyItem{{
		createdAt: time.Now().UTC(), method: "SOCKET.IO", url: m.urlInput.Value(), requestBody: m.bodyInput.Value(),
		requestHeaders: m.headersInput.Entries(), requestParams: m.paramsInput.Entries(), requestAuth: m.authInput.Config(),
		requestBodyConfig: m.bodyConfig(), requestCookies: m.cookiesInput.Entries(), requestTests: m.testsInput.Entries(), requestID: requestID,
	}}, m.history...)
	m.history = trimHistory(m.history)
	return m.connectSocketIO(ctx, cancel, requestID)
}

func (m model) connectSocketIO(ctx context.Context, cancel context.CancelFunc, requestID uuid.UUID) tea.Cmd {
	return func() tea.Msg {
		started := time.Now()
		resolver := newVariableResolver(m.variablesInput.Entries())
		parsed, err := url.Parse(resolver.Resolve(m.urlInput.Value()))
		if err != nil || (parsed.Scheme != "socketio" && parsed.Scheme != "socketios") || parsed.Host == "" {
			return socketIOConnectedMsg{requestID: requestID, err: fmt.Errorf("Socket.IO URL must use socketio:// or socketios://")}
		}
		secure := parsed.Scheme == "socketios"
		parsed.Scheme = "ws"
		if secure {
			parsed.Scheme = "wss"
		}
		query := parsed.Query()
		for _, parameter := range m.paramsInput.Entries() {
			query.Add(resolver.Resolve(parameter.key), resolver.Resolve(parameter.value))
		}
		namespace := strings.TrimSpace(query.Get("namespace"))
		if namespace != "" && namespace != "/" && !strings.HasPrefix(namespace, "/") {
			namespace = "/" + namespace
		}
		event := strings.TrimSpace(query.Get("event"))
		handshakePath := strings.TrimSpace(query.Get("handshake_path"))
		if handshakePath == "" {
			handshakePath = "/socket.io/"
		}
		if !strings.HasPrefix(handshakePath, "/") {
			handshakePath = "/" + handshakePath
		}
		authJSON := strings.TrimSpace(query.Get("auth"))
		var authPayload json.RawMessage
		if authJSON != "" {
			if !json.Valid([]byte(authJSON)) {
				return socketIOConnectedMsg{requestID: requestID, err: fmt.Errorf("Socket.IO auth must be valid JSON")}
			}
			authPayload = json.RawMessage(authJSON)
		}
		for _, key := range []string{"namespace", "event", "handshake_path", "auth"} {
			query.Del(key)
		}
		query.Set("EIO", "4")
		query.Set("transport", "websocket")
		parsed.Path, parsed.RawPath, parsed.RawQuery = handshakePath, "", query.Encode()

		auth := m.authInput.Config().resolved(resolver)
		if auth.typeID == authNTLM {
			return socketIOConnectedMsg{requestID: requestID, err: fmt.Errorf("NTLM authentication is supported for HTTP requests only")}
		}
		if err := auth.applyQuery(query); err != nil {
			return socketIOConnectedMsg{requestID: requestID, err: fmt.Errorf("authorize Socket.IO handshake: %w", err)}
		}
		parsed.RawQuery = query.Encode()
		headers := make(http.Header)
		for _, header := range m.headersInput.Entries() {
			headers.Add(resolver.Resolve(header.key), resolver.Resolve(header.value))
		}
		cookieURL := webSocketCookieURL(parsed)
		cookies := make([]*http.Cookie, 0, len(m.cookiesInput.Entries()))
		if m.client != nil && m.client.Jar != nil {
			cookies = append(cookies, m.client.Jar.Cookies(cookieURL)...)
		}
		for _, cookie := range m.cookiesInput.Entries() {
			cookies = append(cookies, &http.Cookie{Name: resolver.Resolve(cookie.key), Value: resolver.Resolve(cookie.value)})
		}
		if len(cookies) > 0 {
			request := &http.Request{Header: headers}
			for _, cookie := range cookies {
				request.AddCookie(cookie)
			}
		}
		dummyRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return socketIOConnectedMsg{requestID: requestID, err: err}
		}
		dummyRequest.Header = headers.Clone()
		m.settings.syncConfig()
		settings := m.settings.config
		settings.proxyURL = resolver.Resolve(settings.proxyURL)
		settings.proxyBypass = resolver.Resolve(settings.proxyBypass)
		settings.caCertPath = resolver.Resolve(settings.caCertPath)
		settings.clientCertPath = resolver.Resolve(settings.clientCertPath)
		settings.clientKeyPath = resolver.Resolve(settings.clientKeyPath)
		settings.clientPFXPath = resolver.Resolve(settings.clientPFXPath)
		settings.clientPFXPassword = resolver.Resolve(settings.clientPFXPassword)
		client, err := configuredClient(m.client, settings)
		if err != nil {
			return socketIOConnectedMsg{requestID: requestID, err: fmt.Errorf("configure Socket.IO transport: %w", err)}
		}
		if err := auth.authorize(ctx, client, dummyRequest, nil); err != nil {
			return socketIOConnectedMsg{requestID: requestID, err: fmt.Errorf("authorize Socket.IO handshake: %w", err)}
		}
		headers = dummyRequest.Header.Clone()
		dialer := websocket.Dialer{HandshakeTimeout: settings.timeout, EnableCompression: true}
		if transport, ok := client.Transport.(*http.Transport); ok {
			dialer.Proxy, dialer.NetDialContext = transport.Proxy, transport.DialContext
			if transport.TLSClientConfig != nil {
				dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
				dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
			}
		}
		connection, response, err := dialer.DialContext(ctx, parsed.String(), headers)
		if err != nil {
			if response != nil {
				_ = response.Body.Close()
			}
			return socketIOConnectedMsg{requestID: requestID, err: fmt.Errorf("connect Socket.IO: %w", err)}
		}
		responseHeaders := make(http.Header)
		if response != nil {
			responseHeaders = response.Header.Clone()
			_ = response.Body.Close()
			if client.Jar != nil {
				client.Jar.SetCookies(cookieURL, response.Cookies())
			}
		}
		connection.SetReadLimit(maxResponseBody)
		if settings.timeout > 0 {
			_ = connection.SetReadDeadline(time.Now().Add(settings.timeout))
		}
		if err := socketIOHandshake(connection, namespace, authPayload); err != nil {
			_ = connection.Close()
			return socketIOConnectedMsg{requestID: requestID, err: fmt.Errorf("complete Socket.IO handshake: %w", err)}
		}
		_ = connection.SetReadDeadline(time.Time{})
		config := socketIOConfig{url: parsed.String(), namespace: namespace, event: event, auth: authPayload}
		session := &socketIOSession{connection: connection, requestID: requestID, url: parsed.String(), started: started, config: config, headers: responseHeaders, assertions: m.testsInput.Entries(), context: ctx, cancel: cancel}
		session.events = startSocketIOReader(session)
		return socketIOConnectedMsg{requestID: requestID, session: session, duration: time.Since(started)}
	}
}

func socketIOHandshake(connection *websocket.Conn, namespace string, auth json.RawMessage) error {
	_, open, err := connection.ReadMessage()
	if err != nil {
		return err
	}
	if len(open) == 0 || open[0] != '0' || !json.Valid(open[1:]) {
		return fmt.Errorf("invalid Engine.IO open packet")
	}
	packet := "40"
	if namespace != "" && namespace != "/" {
		packet += namespace + ","
	}
	if len(auth) > 0 {
		packet += string(auth)
	}
	if err := connection.WriteMessage(websocket.TextMessage, []byte(packet)); err != nil {
		return err
	}
	for {
		messageType, message, err := connection.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.TextMessage || len(message) == 0 {
			continue
		}
		if string(message) == "2" {
			if err := connection.WriteMessage(websocket.TextMessage, []byte("3")); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(string(message), "40") {
			return nil
		}
		if strings.HasPrefix(string(message), "44") {
			return fmt.Errorf("server rejected namespace: %s", message[2:])
		}
	}
}

func startSocketIOReader(session *socketIOSession) <-chan socketIOEventMsg {
	events := make(chan socketIOEventMsg, 16)
	go func() {
		defer close(events)
		for {
			messageType, payload, err := session.connection.ReadMessage()
			if err != nil {
				events <- socketIOEventMsg{requestID: session.requestID, err: err, session: session}
				return
			}
			if messageType != websocket.TextMessage {
				continue
			}
			if string(payload) == "2" {
				session.writes.Lock()
				err = session.connection.WriteMessage(websocket.TextMessage, []byte("3"))
				session.writes.Unlock()
				if err != nil {
					events <- socketIOEventMsg{requestID: session.requestID, err: err, session: session}
					return
				}
				continue
			}
			event, args, ok := parseSocketIOEvent(payload)
			if ok {
				events <- socketIOEventMsg{requestID: session.requestID, event: event, args: args, session: session}
			}
		}
	}()
	return events
}

func parseSocketIOEvent(packet []byte) (string, []json.RawMessage, bool) {
	value := string(packet)
	if !strings.HasPrefix(value, "42") {
		return "", nil, false
	}
	payload := value[2:]
	if strings.HasPrefix(payload, "/") {
		comma := strings.IndexByte(payload, ',')
		if comma < 0 {
			return "", nil, false
		}
		payload = payload[comma+1:]
	}
	for len(payload) > 0 && payload[0] >= '0' && payload[0] <= '9' {
		payload = payload[1:]
	}
	var values []json.RawMessage
	if json.Unmarshal([]byte(payload), &values) != nil || len(values) == 0 {
		return "", nil, false
	}
	var event string
	if json.Unmarshal(values[0], &event) != nil {
		return "", nil, false
	}
	return event, values[1:], true
}

func socketIOArgs(payload []byte) []json.RawMessage {
	if json.Valid(payload) {
		var array []json.RawMessage
		if json.Unmarshal(payload, &array) == nil {
			return array
		}
		return []json.RawMessage{append(json.RawMessage(nil), payload...)}
	}
	encoded, _ := json.Marshal(string(payload))
	return []json.RawMessage{encoded}
}

func sendSocketIOEvent(session *socketIOSession, event string, args []json.RawMessage) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(event) == "" {
			return socketIOSentMsg{requestID: session.requestID, err: fmt.Errorf("Socket.IO requires event=name in Params or the URL query")}
		}
		values := make([]json.RawMessage, 0, len(args)+1)
		name, _ := json.Marshal(event)
		values = append(values, name)
		values = append(values, args...)
		payload, err := json.Marshal(values)
		packet := "42"
		if session.config.namespace != "" && session.config.namespace != "/" {
			packet += session.config.namespace + ","
		}
		packet += string(payload)
		if err == nil {
			session.writes.Lock()
			err = session.connection.WriteMessage(websocket.TextMessage, []byte(packet))
			session.writes.Unlock()
		}
		return socketIOSentMsg{requestID: session.requestID, event: event, args: args, err: err}
	}
}

func waitSocketIOEvent(session *socketIOSession) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-session.events
		if !ok {
			return nil
		}
		return event
	}
}

func closeSocketIOSession(session *socketIOSession) {
	if session == nil {
		return
	}
	session.closeOnce.Do(func() {
		session.writes.Lock()
		_ = session.connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = session.connection.Close()
		session.writes.Unlock()
		session.cancel()
	})
}

func disconnectSocketIOSession(session *socketIOSession) tea.Cmd {
	return func() tea.Msg {
		closeSocketIOSession(session)
		return socketIOEventMsg{requestID: session.requestID, err: net.ErrClosed, session: session}
	}
}

func formatSocketIOTranscript(direction, event string, args []json.RawMessage) string {
	payload, _ := json.Marshal(args)
	return fmt.Sprintf("%s EVENT %q %s\n%s\n", direction, event, formatByteCount(len(payload)), payload)
}

func (m *model) appendSocketIOEntry(requestID uuid.UUID, entry string) error {
	return m.appendWebSocketEntry(requestID, entry)
}

func isNormalSocketIOClose(err error) bool {
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF)
}

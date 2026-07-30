package tui

import (
	"context"
	"encoding/base64"
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
	uuid "github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type webSocketSession struct {
	connection *websocket.Conn
	writes     sync.Mutex
	requestID  uuid.UUID
	url        string
	started    time.Time
	headers    http.Header
	assertions []headerEntry
}

type webSocketConnectedMsg struct {
	requestID uuid.UUID
	session   *webSocketSession
	events    <-chan webSocketEventMsg
	headers   string
	duration  time.Duration
	err       error
}

type webSocketEventMsg struct {
	requestID   uuid.UUID
	messageType int
	payload     []byte
	err         error
	events      <-chan webSocketEventMsg
	session     *webSocketSession
}

type webSocketSentMsg struct {
	requestID   uuid.UUID
	messageType int
	payload     []byte
	err         error
}

func isWebSocketURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (strings.EqualFold(parsed.Scheme, "ws") || strings.EqualFold(parsed.Scheme, "wss"))
}

func (m *model) beginWebSocketConnection() tea.Cmd {
	if m.webSocket != nil {
		closeWebSocketSession(m.webSocket)
		m.webSocket = nil
	}
	if m.webSocketCancel != nil {
		m.webSocketCancel()
	}
	requestID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	m.requestId = requestID
	m.requestContext = ctx
	m.webSocketCancel = cancel
	m.cancelRequest = cancel
	m.response = ""
	m.responseRaw = ""
	m.responseRawAvailable = true
	m.responseHeaders = ""
	m.responseTests = ""
	m.responseStatusCode = 0
	m.responseMeta = "Connecting WebSocket..."
	m.responseModel.SetContent("")
	m.responseHeadersModel.SetContent("")
	m.responseTestsModel.SetContent("")
	m.historyPos = 0
	m.history = append([]historyItem{{
		createdAt: time.Now().UTC(), method: "WS", url: m.urlInput.Value(),
		requestBody: m.bodyInput.Value(), requestHeaders: m.headersInput.Entries(), requestParams: m.paramsInput.Entries(),
		requestAuth: m.authInput.Config(), requestBodyConfig: m.bodyConfig(), requestCookies: m.cookiesInput.Entries(), requestTests: m.testsInput.Entries(),
		requestID: requestID,
	}}, m.history...)
	m.history = trimHistory(m.history)
	return m.connectWebSocket(ctx, requestID)
}

func (m *model) webSocketPayload() (int, []byte, error) {
	resolver := newVariableResolver(m.variablesInput.Entries())
	payload, _, _, err := m.buildRequestBody(resolver)
	if err != nil {
		return 0, nil, err
	}
	messageType := websocket.TextMessage
	if m.bodyMode == bodyBinary {
		messageType = websocket.BinaryMessage
	}
	return messageType, payload, nil
}

func (m model) connectWebSocket(ctx context.Context, requestID uuid.UUID) tea.Cmd {
	return func() tea.Msg {
		started := time.Now()
		resolver := newVariableResolver(m.variablesInput.Entries())
		parsed, err := url.Parse(resolver.Resolve(m.urlInput.Value()))
		if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
			return webSocketConnectedMsg{requestID: requestID, err: fmt.Errorf("WebSocket URL must use ws:// or wss://")}
		}
		query := parsed.Query()
		for _, param := range m.paramsInput.Entries() {
			query.Add(resolver.Resolve(param.key), resolver.Resolve(param.value))
		}
		auth := m.authInput.Config().resolved(resolver)
		if auth.typeID == authNTLM {
			return webSocketConnectedMsg{requestID: requestID, err: fmt.Errorf("NTLM authentication is supported for HTTP requests only")}
		}
		if err := auth.applyQuery(query); err != nil {
			return webSocketConnectedMsg{requestID: requestID, err: fmt.Errorf("authorize WebSocket handshake: %w", err)}
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
			return webSocketConnectedMsg{requestID: requestID, err: fmt.Errorf("create WebSocket handshake: %w", err)}
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
			return webSocketConnectedMsg{requestID: requestID, err: fmt.Errorf("configure WebSocket transport: %w", err)}
		}
		if err := auth.authorize(ctx, client, dummyRequest, nil); err != nil {
			return webSocketConnectedMsg{requestID: requestID, err: fmt.Errorf("authorize WebSocket handshake: %w", err)}
		}
		headers = dummyRequest.Header.Clone()

		dialer := websocket.Dialer{HandshakeTimeout: settings.timeout, EnableCompression: true}
		if transport, ok := client.Transport.(*http.Transport); ok {
			dialer.Proxy = transport.Proxy
			dialer.NetDialContext = transport.DialContext
			if transport.TLSClientConfig != nil {
				dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
				dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
			}
		}
		connection, response, dialErr := dialer.DialContext(ctx, parsed.String(), headers)
		if dialErr != nil && auth.typeID == authDigest && response != nil && response.StatusCode == http.StatusUnauthorized {
			authorization, digestErr := digestAuthorization(dummyRequest, auth, response.Header.Values("WWW-Authenticate"))
			_ = response.Body.Close()
			if digestErr != nil {
				return webSocketConnectedMsg{requestID: requestID, err: fmt.Errorf("authorize WebSocket handshake: %w", digestErr)}
			}
			headers.Set("Authorization", authorization)
			connection, response, dialErr = dialer.DialContext(ctx, parsed.String(), headers)
		}
		if dialErr != nil {
			detail := ""
			if response != nil {
				body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
				_ = response.Body.Close()
				detail = fmt.Sprintf(" (%s%s)", response.Status, responseErrorDetail(body))
			}
			return webSocketConnectedMsg{requestID: requestID, err: fmt.Errorf("connect WebSocket%s: %w", detail, dialErr)}
		}
		connection.SetReadLimit(maxResponseBody)
		responseHeaders := make(http.Header)
		if response != nil {
			_ = response.Body.Close()
			responseHeaders = response.Header.Clone()
			if client.Jar != nil {
				client.Jar.SetCookies(cookieURL, response.Cookies())
			}
		}
		session := &webSocketSession{
			connection: connection, requestID: requestID, url: parsed.String(), started: started,
			headers: responseHeaders, assertions: m.testsInput.Entries(),
		}
		events := startWebSocketReader(session)
		return webSocketConnectedMsg{
			requestID: requestID, session: session, events: events,
			headers: formatHeaders(responseHeaders), duration: time.Since(started),
		}
	}
}

func responseErrorDetail(body []byte) string {
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return ""
	}
	if len(detail) > 200 {
		detail = detail[:199] + "…"
	}
	return ": " + detail
}

func webSocketCookieURL(value *url.URL) *url.URL {
	copyURL := *value
	if copyURL.Scheme == "wss" {
		copyURL.Scheme = "https"
	} else {
		copyURL.Scheme = "http"
	}
	return &copyURL
}

func startWebSocketReader(session *webSocketSession) <-chan webSocketEventMsg {
	events := make(chan webSocketEventMsg, 8)
	go func() {
		defer close(events)
		for {
			messageType, payload, err := session.connection.ReadMessage()
			events <- webSocketEventMsg{requestID: session.requestID, messageType: messageType, payload: payload, err: err, session: session}
			if err != nil {
				return
			}
		}
	}()
	return events
}

func waitWebSocketEvent(events <-chan webSocketEventMsg) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return nil
		}
		event.events = events
		return event
	}
}

func sendWebSocketMessage(session *webSocketSession, messageType int, payload []byte) tea.Cmd {
	return func() tea.Msg {
		session.writes.Lock()
		err := session.connection.WriteMessage(messageType, payload)
		session.writes.Unlock()
		return webSocketSentMsg{requestID: session.requestID, messageType: messageType, payload: payload, err: err}
	}
}

func closeWebSocketSession(session *webSocketSession) {
	if session == nil {
		return
	}
	session.writes.Lock()
	_ = session.connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	_ = session.connection.Close()
	session.writes.Unlock()
}

func formatWebSocketTranscript(direction string, messageType int, payload []byte) string {
	kind := "TEXT"
	content := string(payload)
	if messageType == websocket.BinaryMessage {
		kind = "BINARY"
		content = base64.StdEncoding.EncodeToString(payload)
	}
	return fmt.Sprintf("%s %s %s\n%s\n", direction, kind, formatByteCount(len(payload)), content)
}

func appendWebSocketTranscript(existing, entry string) (string, error) {
	if len(existing)+len(entry) > maxResponseBody {
		return existing, fmt.Errorf("WebSocket transcript exceeds the %s display limit", formatByteCount(maxResponseBody))
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return existing + entry, nil
}

func (m *model) appendWebSocketEntry(requestID uuid.UUID, entry string) error {
	base := ""
	for index := range m.history {
		if m.history[index].requestID == requestID {
			base = m.history[index].responseRaw
			break
		}
	}
	if requestID == m.requestId {
		base = m.responseRaw
	}
	transcript, err := appendWebSocketTranscript(base, entry)
	if err != nil {
		return err
	}
	for index := range m.history {
		if m.history[index].requestID == requestID {
			m.history[index].responseBody = transcript
			m.history[index].responseRaw = transcript
			m.history[index].responseRawAvailable = true
			break
		}
	}
	if requestID == m.requestId {
		wasAtBottom := m.responseModel.AtBottom()
		m.response = transcript
		m.responseRaw = transcript
		m.responseRawAvailable = true
		m.responseModel.SetContent(transcript)
		if wasAtBottom {
			m.responseModel.GotoBottom()
		}
	}
	return nil
}

func isNormalWebSocketClose(err error) bool {
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(err, net.ErrClosed)
}

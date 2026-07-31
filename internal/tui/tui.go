package tui

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uuid "github.com/google/uuid"
	zone "github.com/lrstanley/bubblezone/v2"
)

const (
	helpHeight   = 3
	urlBarHeight = 3
	historyWidth = 28
	methodWidth  = 10
)

// Pane focus targets
type pane int

const (
	paneURL pane = iota
	paneRequest
	paneResponse
	paneHistory
	paneCount // sentinel for wrapping
)

var methods = []string{
	"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE",
	"COPY", "LINK", "UNLINK", "PURGE", "LOCK", "UNLOCK", "PROPFIND", "VIEW",
}

func (m model) displayedMethod() string {
	if isSocketIOURL(m.urlInput.Value()) {
		return "SOCKET.IO"
	}
	if isMQTTURL(m.urlInput.Value()) {
		return "MQTT"
	}
	if isGRPCURL(m.urlInput.Value()) {
		return "GRPC"
	}
	if isWebSocketURL(m.urlInput.Value()) {
		return "WS"
	}
	if m.customMethod != "" {
		return m.customMethod
	}
	return methods[m.methodIdx]
}

func (m *model) setHTTPMethod(method string) {
	method = strings.TrimSpace(method)
	m.customMethod = ""
	for index, candidate := range methods {
		if method == candidate {
			m.methodIdx = index
			return
		}
	}
	if validHTTPMethod(method) {
		m.customMethod = method
	}
}

func (m *model) setMethodForURL(method, rawURL string) {
	if isGRPCURL(rawURL) || isWebSocketURL(rawURL) || isSocketIOURL(rawURL) || isMQTTURL(rawURL) {
		m.customMethod = ""
		return
	}
	m.setHTTPMethod(method)
}

func validHTTPMethod(method string) bool {
	if method == "" {
		return false
	}
	for _, character := range method {
		if character <= 0x20 || character >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", character) {
			return false
		}
	}
	return true
}

type historyItem struct {
	createdAt            time.Time
	method               string
	url                  string
	requestBody          string
	requestHeaders       []headerEntry
	requestParams        []headerEntry
	requestAuth          authConfig
	requestBodyConfig    bodyConfig
	requestCookies       []headerEntry
	requestTests         []headerEntry
	responseBody         string
	responseRaw          string
	responseRawAvailable bool
	responseHeaders      string
	responseMeta         string
	responseTests        string
	requestID            uuid.UUID
}

type inputMode int

const (
	modeNormal inputMode = iota
	modeInsert
)

type keymap struct {
	next, prev, send, cancel, connect, save, saveExample, exportCurl, exportResponse, collections, settings, environment, cycleMethod, editMethod, quit key.Binding
}

type model struct {
	width  int
	height int
	client *http.Client

	urlInput              textinput.Model
	methodIdx             int
	customMethod          string
	methodEditOpen        bool
	methodInput           textinput.Model
	bodyInput             textarea.Model
	bodyMode              bodyMode
	rawBodyType           rawBodyType
	formInput             headersTable
	multipartInput        headersTable
	binaryPathInput       textinput.Model
	graphqlQueryInput     textarea.Model
	graphqlVariablesInput textarea.Model
	graphqlOperationInput textinput.Model
	graphqlField          int
	headersInput          headersTable
	paramsInput           headersTable
	authInput             authPane
	cookiesInput          headersTable
	testsInput            headersTable
	responseHeadersModel  viewport.Model
	responseHeaders       string
	responseMeta          string
	responseTests         string
	responseTab           responseTab
	requestTab            requestTab
	history               []historyItem // TODO: Would be nice to have this as map
	historyPos            int
	historyPendingD       bool
	cookiePos             int
	cookiePendingD        bool
	responseModel         viewport.Model
	response              string
	responseRaw           string
	responseRawAvailable  bool
	responseStatusCode    int
	responseSearchOpen    bool
	responseSearchInput   textinput.Model
	responseSearchMatches []int
	responseSearchPos     int
	responseSearchStatus  string
	responseFilterOpen    bool
	responseFilterInput   textinput.Model
	responseFilterActive  bool
	responseFilterContent string
	responseFilterStatus  string
	responseSaveOpen      bool
	responseSaveInput     textinput.Model
	responseTestsModel    viewport.Model
	assertionResults      []AssertionResult
	requestId             uuid.UUID
	requestContext        context.Context
	cancelRequest         context.CancelFunc
	oauthLoginID          uuid.UUID
	webSocket             *webSocketSession
	webSocketCancel       context.CancelFunc
	mqtt                  *mqttSession
	mqttCancel            context.CancelFunc
	socketIO              *socketIOSession
	socketIOCancel        context.CancelFunc
	settingsOpen          bool
	settings              settingsPane
	environmentOpen       bool
	variablesInput        headersTable
	environments          []environmentProfile
	environmentPos        int
	environmentNameOpen   bool
	environmentCreating   bool
	environmentNameInput  textinput.Model
	environmentPendingD   bool
	workspacePath         string
	savedRequests         []savedRequest
	collectionPos         int
	collectionPendingD    bool
	examplePos            int
	examplePendingD       bool
	activeSavedIndex      int
	collectionRenameOpen  bool
	collectionRenameInput textinput.Model
	sidebarMode           sidebarMode

	focus     pane
	inputMode inputMode
	keymap    keymap
	help      help.Model
}

func NewModel() model {
	jar := newPersistentCookieJar()
	ti := textinput.New()
	ti.Placeholder = "https://api.example.com/endpoint"
	ti.CharLimit = 2048
	ti.Focus()
	methodInput := textinput.New()
	methodInput.Prompt = ""
	methodInput.Placeholder = "CUSTOM"
	methodInput.CharLimit = 128
	methodInput.SetWidth(methodWidth - 2)
	methodInput.Blur()

	ta := textarea.New()
	ta.Placeholder = `{"key": "value"}`
	ta.ShowLineNumbers = true
	ta.Prompt = ""
	ta.Blur()
	searchInput := textinput.New()
	searchInput.Prompt = ""
	searchInput.Placeholder = "search response"
	searchInput.CharLimit = 2048
	searchInput.Blur()
	responseSaveInput := textinput.New()
	responseSaveInput.Prompt = ""
	responseSaveInput.Placeholder = "response output path"
	responseSaveInput.CharLimit = 4096
	responseSaveInput.Blur()
	responseFilterInput := textinput.New()
	responseFilterInput.Prompt = ""
	responseFilterInput.Placeholder = "$.items[?(@.active)] or //item"
	responseFilterInput.CharLimit = 4096
	responseFilterInput.Blur()
	graphqlOperationInput := textinput.New()
	graphqlOperationInput.Prompt = ""
	graphqlOperationInput.Placeholder = "optional operation name"
	graphqlOperationInput.CharLimit = 1024
	graphqlOperationInput.Blur()
	renameInput := textinput.New()
	renameInput.Prompt = ""
	renameInput.Placeholder = "Folder / Request name"
	renameInput.CharLimit = 2048
	renameInput.Blur()
	environmentNameInput := textinput.New()
	environmentNameInput.Prompt = ""
	environmentNameInput.Placeholder = "Environment name"
	environmentNameInput.CharLimit = 256
	environmentNameInput.Blur()

	m := model{
		client:                &http.Client{Timeout: 30 * time.Second, Jar: jar},
		urlInput:              ti,
		methodInput:           methodInput,
		bodyInput:             ta,
		bodyMode:              bodyRaw,
		rawBodyType:           rawJSON,
		formInput:             newFormTable(),
		multipartInput:        newMultipartTable(),
		binaryPathInput:       newBinaryPathInput(),
		graphqlQueryInput:     newGraphQLTextarea("query GetUser($id: ID!) { user(id: $id) { id name } }"),
		graphqlVariablesInput: newGraphQLTextarea(`{"id":"123"}`),
		graphqlOperationInput: graphqlOperationInput,
		headersInput:          newHeadersTable(),
		paramsInput:           newParamsTable(),
		authInput:             newAuthPane(),
		cookiesInput:          newCookiesTable(),
		testsInput:            newTestsTable(),
		responseModel: viewport.New(
			viewport.WithWidth(0),
			viewport.WithHeight(0),
		),
		responseSearchInput:   searchInput,
		responseFilterInput:   responseFilterInput,
		responseSaveInput:     responseSaveInput,
		collectionRenameInput: renameInput,
		environmentNameInput:  environmentNameInput,
		responseHeadersModel: viewport.New(
			viewport.WithWidth(0),
			viewport.WithHeight(0),
		),
		responseTestsModel: viewport.New(
			viewport.WithWidth(0),
			viewport.WithHeight(0),
		),
		history:          []historyItem{},
		activeSavedIndex: -1,
		focus:            paneURL,
		inputMode:        modeNormal,
		help:             help.New(),
		settings:         newSettingsPane(),
		variablesInput:   newVariablesTable(),
		environments:     []environmentProfile{{name: defaultEnvironmentName}},
		keymap: keymap{
			next: key.NewBinding(
				key.WithKeys("tab"),
				key.WithHelp("tab", "next pane"),
			),
			prev: key.NewBinding(
				key.WithKeys("shift+tab"),
				key.WithHelp("shift+tab", "prev pane"),
			),
			send: key.NewBinding(
				key.WithKeys("ctrl+s", "enter"),
				key.WithHelp("ctrl+s/enter", "send request"),
			),
			cancel: key.NewBinding(
				key.WithKeys("ctrl+x"),
				key.WithHelp("ctrl+x", "cancel request"),
			),
			connect: key.NewBinding(
				key.WithKeys("ctrl+k"),
				key.WithHelp("ctrl+k", "connect"),
			),
			save: key.NewBinding(
				key.WithKeys("ctrl+w"),
				key.WithHelp("ctrl+w", "save request"),
			),
			saveExample: key.NewBinding(
				key.WithKeys("ctrl+y"),
				key.WithHelp("ctrl+y", "save example"),
			),
			exportCurl: key.NewBinding(
				key.WithKeys("ctrl+g"),
				key.WithHelp("ctrl+g", "show cURL"),
			),
			exportResponse: key.NewBinding(
				key.WithKeys("ctrl+d"),
				key.WithHelp("ctrl+d", "save response"),
			),
			collections: key.NewBinding(
				key.WithKeys("ctrl+p"),
				key.WithHelp("ctrl+p", "sidebar"),
			),
			settings: key.NewBinding(
				key.WithKeys("ctrl+t"),
				key.WithHelp("ctrl+t", "settings"),
			),
			environment: key.NewBinding(
				key.WithKeys("ctrl+e"),
				key.WithHelp("ctrl+e", "environment"),
			),
			cycleMethod: key.NewBinding(
				key.WithKeys("ctrl+o"),
				key.WithHelp("ctrl+o", "cycle method"),
			),
			editMethod: key.NewBinding(
				key.WithKeys("O"),
				key.WithHelp("O", "edit method"),
			),
			quit: key.NewBinding(
				key.WithKeys("ctrl+c"),
				key.WithHelp("ctrl+c", "quit"),
			),
		},
	}
	return m
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case oauthLoginMsg:
		if msg.id != m.oauthLoginID {
			break
		}
		m.oauthLoginID = uuid.Nil
		if m.cancelRequest != nil {
			m.cancelRequest()
		}
		m.cancelRequest, m.requestContext = nil, nil
		if msg.err != nil {
			m.responseMeta = "OAuth 2 authorization failed: " + msg.err.Error()
			break
		}
		config := m.authInput.Config()
		if config.typeID != authOAuth2AuthorizationCode {
			m.responseMeta = "OAuth 2 token received after authorization mode changed"
			break
		}
		config.oauthAccessToken = msg.token.accessToken
		config.oauthTokenType = msg.token.tokenType
		config.oauthAccessTokenExpiry = msg.token.expiresAt
		if msg.token.refreshToken != "" {
			config.oauthRefreshToken = msg.token.refreshToken
		}
		m.authInput.SetConfig(config)
		if m.activeSavedIndex >= 0 && m.activeSavedIndex < len(m.savedRequests) {
			m.savedRequests[m.activeSavedIndex].auth = config
			m.saveWorkspaceWithStatus()
		}
		m.responseMeta = "OAuth 2 authorization complete"

	case socketIOConnectedMsg:
		if msg.requestID != m.requestId {
			closeSocketIOSession(msg.session)
			break
		}
		if msg.err != nil {
			m.response, m.responseRaw, m.responseMeta = msg.err.Error(), "", "Socket.IO connection failed"
			m.responseRawAvailable = false
			m.responseModel.SetContent(m.response)
			m.cancelRequest, m.requestContext = nil, nil
			if m.socketIOCancel != nil {
				m.socketIOCancel()
				m.socketIOCancel = nil
			}
			for index := range m.history {
				if m.history[index].requestID == msg.requestID {
					m.history[index].responseBody, m.history[index].responseMeta = m.response, m.responseMeta
					break
				}
			}
			m.saveWorkspaceWithStatus()
			break
		}
		m.socketIO = msg.session
		m.cancelRequest = func() { closeSocketIOSession(msg.session) }
		m.responseHeaders = formatHeaders(msg.session.headers)
		m.responseHeadersModel.SetContent(m.responseHeaders)
		m.responseMeta = fmt.Sprintf("Socket.IO connected • %s", msg.duration.Round(time.Millisecond))
		m.responseStatusCode = http.StatusSwitchingProtocols
		_ = m.appendSocketIOEntry(msg.requestID, "CONNECTED "+msg.session.url+"\n")
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				m.history[index].responseHeaders, m.history[index].responseMeta = m.responseHeaders, m.responseMeta
				break
			}
		}
		cmds = append(cmds, waitSocketIOEvent(msg.session))

	case socketIOSentMsg:
		if msg.requestID == m.requestId {
			if msg.err != nil {
				m.responseMeta = "Socket.IO send failed: " + msg.err.Error()
			} else if err := m.appendSocketIOEntry(msg.requestID, formatSocketIOTranscript("→", msg.event, msg.args)); err != nil {
				m.responseMeta = err.Error()
				closeSocketIOSession(m.socketIO)
			}
		}

	case socketIOEventMsg:
		if msg.err == nil {
			if err := m.appendSocketIOEntry(msg.requestID, formatSocketIOTranscript("←", msg.event, msg.args)); err != nil {
				closeSocketIOSession(msg.session)
			} else {
				cmds = append(cmds, waitSocketIOEvent(msg.session))
			}
			break
		}
		transcript := ""
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				transcript = m.history[index].responseRaw
				break
			}
		}
		elapsed := time.Since(msg.session.started)
		results := evaluateAssertions(msg.session.assertions, assertionResponse{status: http.StatusSwitchingProtocols, headers: msg.session.headers, body: []byte(transcript), duration: elapsed, size: len(transcript)})
		meta := fmt.Sprintf("Socket.IO disconnected • %s • %s", elapsed.Round(time.Millisecond), formatByteCount(len(transcript)))
		if !isNormalSocketIOClose(msg.err) {
			meta = fmt.Sprintf("Socket.IO closed: %v • %s", msg.err, elapsed.Round(time.Millisecond))
		}
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				m.history[index].responseMeta, m.history[index].responseTests = meta, formatAssertionResults(results)
				break
			}
		}
		if msg.requestID == m.requestId {
			updates := successfulVariableUpdates(msg.session.assertions, results)
			if len(updates) > 0 {
				m.variablesInput.SetEntries(mergeHeaderEntries(m.variablesInput.Entries(), updates))
				m.syncActiveEnvironment()
			}
			m.responseMeta, m.assertionResults, m.responseTests = meta, results, formatAssertionResults(results)
			m.responseTestsModel.SetContent(m.responseTests)
			m.socketIO, m.cancelRequest, m.requestContext = nil, nil, nil
			if m.socketIOCancel != nil {
				m.socketIOCancel()
				m.socketIOCancel = nil
			}
		}
		m.saveWorkspaceWithStatus()

	case mqttConnectedMsg:
		if msg.requestID != m.requestId {
			terminateMQTTSession(msg.session)
			break
		}
		if msg.err != nil {
			m.response = msg.err.Error()
			m.responseRaw = ""
			m.responseRawAvailable = false
			m.responseMeta = "MQTT connection failed"
			m.responseModel.SetContent(m.response)
			m.cancelRequest = nil
			m.requestContext = nil
			if m.mqttCancel != nil {
				m.mqttCancel()
				m.mqttCancel = nil
			}
			for index := range m.history {
				if m.history[index].requestID == msg.requestID {
					m.history[index].responseBody = m.response
					m.history[index].responseMeta = m.responseMeta
					break
				}
			}
			m.saveWorkspaceWithStatus()
			break
		}
		m.mqtt = msg.session
		m.cancelRequest = func() { terminateMQTTSession(msg.session) }
		m.responseHeaders = mqttConnectionSummary(msg.session.config)
		m.responseHeadersModel.SetContent(m.responseHeaders)
		m.responseMeta = fmt.Sprintf("MQTT %s connected • %s", msg.session.config.version, msg.duration.Round(time.Millisecond))
		m.responseStatusCode = http.StatusOK
		_ = m.appendMQTTEntry(msg.requestID, "CONNECTED "+msg.session.url+"\n")
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				m.history[index].responseHeaders = m.responseHeaders
				m.history[index].responseMeta = m.responseMeta
				break
			}
		}
		cmds = append(cmds, waitMQTTEvent(msg.session))

	case mqttSentMsg:
		if msg.requestID != m.requestId {
			break
		}
		if msg.err != nil {
			m.responseMeta = "MQTT publish failed: " + msg.err.Error()
			break
		}
		if err := m.appendMQTTEntry(msg.requestID, formatMQTTTranscript("→", msg.topic, msg.qos, msg.retained, msg.payload)); err != nil {
			m.responseMeta = err.Error()
			terminateMQTTSession(m.mqtt)
		}

	case mqttEventMsg:
		if msg.err == nil {
			if err := m.appendMQTTEntry(msg.requestID, formatMQTTTranscript("←", msg.topic, msg.qos, msg.retained, msg.payload)); err != nil {
				if msg.requestID == m.requestId {
					m.responseMeta = err.Error()
				}
				terminateMQTTSession(msg.session)
			}
			if msg.events != nil {
				cmds = append(cmds, waitMQTTEvent(msg.session))
			}
			break
		}
		transcript := ""
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				transcript = m.history[index].responseRaw
				break
			}
		}
		elapsed := time.Since(msg.session.started)
		headers := make(http.Header)
		headers.Set("MQTT-Version", msg.session.config.version)
		results := evaluateAssertions(msg.session.assertions, assertionResponse{status: http.StatusOK, headers: headers, body: []byte(transcript), duration: elapsed, size: len(transcript)})
		meta := fmt.Sprintf("MQTT disconnected • %s • %s", elapsed.Round(time.Millisecond), formatByteCount(len(transcript)))
		if !isNormalMQTTClose(msg.err) {
			meta = fmt.Sprintf("MQTT closed: %v • %s", msg.err, elapsed.Round(time.Millisecond))
		}
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				m.history[index].responseMeta = meta
				m.history[index].responseTests = formatAssertionResults(results)
				break
			}
		}
		if msg.requestID == m.requestId {
			updates := successfulVariableUpdates(msg.session.assertions, results)
			if len(updates) > 0 {
				m.variablesInput.SetEntries(mergeHeaderEntries(m.variablesInput.Entries(), updates))
				m.syncActiveEnvironment()
			}
			m.responseMeta = meta
			m.assertionResults = results
			m.responseTests = formatAssertionResults(results)
			m.responseTestsModel.SetContent(m.responseTests)
			m.mqtt = nil
			m.cancelRequest = nil
			m.requestContext = nil
			if m.mqttCancel != nil {
				m.mqttCancel()
				m.mqttCancel = nil
			}
		}
		m.saveWorkspaceWithStatus()

	case webSocketConnectedMsg:
		if msg.requestID != m.requestId {
			closeWebSocketSession(msg.session)
			break
		}
		if msg.err != nil {
			m.response = msg.err.Error()
			m.responseRaw = ""
			m.responseRawAvailable = false
			m.responseMeta = "WebSocket connection failed"
			m.responseModel.SetContent(m.response)
			m.cancelRequest = nil
			m.requestContext = nil
			if m.webSocketCancel != nil {
				m.webSocketCancel()
				m.webSocketCancel = nil
			}
			for index := range m.history {
				if m.history[index].requestID == msg.requestID {
					m.history[index].responseBody = m.response
					m.history[index].responseMeta = m.responseMeta
					break
				}
			}
			m.saveWorkspaceWithStatus()
			break
		}
		m.webSocket = msg.session
		cancel := m.webSocketCancel
		m.cancelRequest = func() {
			if cancel != nil {
				cancel()
			}
			closeWebSocketSession(msg.session)
		}
		m.responseHeaders = msg.headers
		m.responseHeadersModel.SetContent(msg.headers)
		m.responseMeta = fmt.Sprintf("101 Switching Protocols • connected • %s", msg.duration.Round(time.Millisecond))
		m.responseStatusCode = http.StatusSwitchingProtocols
		_ = m.appendWebSocketEntry(msg.requestID, "CONNECTED "+msg.session.url+"\n")
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				m.history[index].responseHeaders = msg.headers
				m.history[index].responseMeta = m.responseMeta
				break
			}
		}
		cmds = append(cmds, waitWebSocketEvent(msg.events))

	case webSocketSentMsg:
		if msg.requestID != m.requestId {
			break
		}
		if msg.err != nil {
			m.responseMeta = "WebSocket send failed: " + msg.err.Error()
			break
		}
		if err := m.appendWebSocketEntry(msg.requestID, formatWebSocketTranscript("→", msg.messageType, msg.payload)); err != nil {
			m.responseMeta = err.Error()
			closeWebSocketSession(m.webSocket)
		}

	case webSocketEventMsg:
		if msg.err == nil {
			if err := m.appendWebSocketEntry(msg.requestID, formatWebSocketTranscript("←", msg.messageType, msg.payload)); err != nil {
				if msg.requestID == m.requestId {
					m.responseMeta = err.Error()
				}
				closeWebSocketSession(msg.session)
			}
			if msg.events != nil {
				cmds = append(cmds, waitWebSocketEvent(msg.events))
			}
			break
		}
		transcript := ""
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				transcript = m.history[index].responseRaw
				break
			}
		}
		elapsed := time.Since(msg.session.started)
		results := evaluateAssertions(msg.session.assertions, assertionResponse{status: http.StatusSwitchingProtocols, headers: msg.session.headers, body: []byte(transcript), duration: elapsed, size: len(transcript)})
		meta := fmt.Sprintf("WebSocket disconnected • %s • %s", elapsed.Round(time.Millisecond), formatByteCount(len(transcript)))
		if !isNormalWebSocketClose(msg.err) {
			meta = fmt.Sprintf("WebSocket closed: %v • %s", msg.err, elapsed.Round(time.Millisecond))
		}
		for index := range m.history {
			if m.history[index].requestID == msg.requestID {
				m.history[index].responseMeta = meta
				m.history[index].responseTests = formatAssertionResults(results)
				break
			}
		}
		if msg.requestID == m.requestId {
			updates := successfulVariableUpdates(msg.session.assertions, results)
			if len(updates) > 0 {
				m.variablesInput.SetEntries(mergeHeaderEntries(m.variablesInput.Entries(), updates))
				m.syncActiveEnvironment()
			}
			m.responseMeta = meta
			m.assertionResults = results
			m.responseTests = formatAssertionResults(results)
			m.responseTestsModel.SetContent(m.responseTests)
			m.webSocket = nil
			m.cancelRequest = nil
			m.requestContext = nil
			if m.webSocketCancel != nil {
				m.webSocketCancel()
				m.webSocketCancel = nil
			}
		}
		m.saveWorkspaceWithStatus()

	case responseMsg:
		if msg.stream != nil {
			cmds = append(cmds, waitResponseStream(msg.stream))
		}
		historyUpdated := false
		for i := range m.history {
			if m.history[i].requestID == msg.requestID {
				m.history[i].responseBody = msg.responseBody
				m.history[i].responseRaw = msg.responseRaw
				m.history[i].responseRawAvailable = msg.responseRawAvailable
				m.history[i].responseHeaders = msg.responseHeaders
				m.history[i].responseMeta = msg.responseMeta
				m.history[i].responseTests = formatAssertionResults(msg.assertionResults)
				historyUpdated = true
				break
			}
		}
		// A slower, older request must not replace the currently selected response.
		if msg.requestID == m.requestId {
			m.clearResponseFilter()
			if len(msg.variableUpdates) > 0 {
				m.variablesInput.SetEntries(mergeHeaderEntries(m.variablesInput.Entries(), msg.variableUpdates))
				m.syncActiveEnvironment()
			}
			m.responseModel.SetContent(msg.responseBody)
			m.response = msg.responseBody
			m.responseRaw = msg.responseRaw
			m.responseRawAvailable = msg.responseRawAvailable
			m.responseHeadersModel.SetContent(msg.responseHeaders)
			m.responseHeaders = msg.responseHeaders
			m.responseMeta = msg.responseMeta
			m.responseStatusCode = msg.statusCode
			m.assertionResults = msg.assertionResults
			m.responseTests = formatAssertionResults(msg.assertionResults)
			m.responseTestsModel.SetContent(m.responseTests)
			if msg.stream == nil {
				m.cancelRequest = nil
				m.requestContext = nil
			}
			m.resetResponseSearchMatches()
			if len(msg.variableUpdates) > 0 {
				m.saveWorkspaceWithStatus()
			}
		}
		if historyUpdated && msg.stream == nil {
			m.saveWorkspaceWithStatus()
		}

	case responseStreamMsg:
		if msg.final != nil {
			return m.Update(*msg.final)
		}
		for i := range m.history {
			if m.history[i].requestID == msg.requestID {
				m.history[i].responseBody += msg.chunk
				m.history[i].responseRaw += msg.chunk
				m.history[i].responseRawAvailable = true
				break
			}
		}
		if msg.requestID == m.requestId {
			wasAtBottom := m.responseModel.AtBottom()
			m.response += msg.chunk
			m.responseRaw += msg.chunk
			m.responseRawAvailable = true
			m.responseModel.SetContent(m.response)
			if wasAtBottom {
				m.responseModel.GotoBottom()
			}
		}
		if msg.stream != nil {
			cmds = append(cmds, waitResponseStream(msg.stream))
		}

	case tea.KeyMsg:
		inInsert := m.focus == paneRequest && m.inputMode == modeInsert

		switch {
		case key.Matches(msg, m.keymap.quit):
			if m.socketIO != nil {
				closeSocketIOSession(m.socketIO)
			}
			if m.mqtt != nil {
				terminateMQTTSession(m.mqtt)
			}
			if m.cancelRequest != nil {
				m.cancelRequest()
			}
			if err := m.SaveWorkspace(); err != nil {
				m.responseMeta = "Workspace save failed"
				m.responseModel.SetContent(err.Error())
				return m, nil
			}
			return m, tea.Quit

		case m.methodEditOpen:
			switch msg.String() {
			case "esc":
				m.methodEditOpen = false
				m.methodInput.Blur()
			case "enter":
				method := strings.TrimSpace(m.methodInput.Value())
				if !validHTTPMethod(method) {
					m.responseMeta = "HTTP method must be a non-empty token"
					break
				}
				m.setHTTPMethod(method)
				m.methodEditOpen = false
				m.methodInput.Blur()
				m.responseMeta = "HTTP method set to " + method
			default:
				var cmd tea.Cmd
				m.methodInput, cmd = m.methodInput.Update(msg)
				cmds = append(cmds, cmd)
			}

		case key.Matches(msg, m.keymap.connect):
			if m.socketIO != nil {
				m.responseMeta = "Disconnecting Socket.IO..."
				return m, disconnectSocketIOSession(m.socketIO)
			} else if m.socketIOCancel != nil {
				m.socketIOCancel()
				m.socketIOCancel, m.cancelRequest = nil, nil
				m.responseMeta = "Socket.IO connection cancelled"
			} else if isSocketIOURL(m.urlInput.Value()) {
				return m, m.beginSocketIOConnection()
			} else if m.mqtt != nil {
				m.responseMeta = "Disconnecting MQTT..."
				return m, disconnectMQTTSession(m.mqtt)
			} else if m.mqttCancel != nil {
				m.mqttCancel()
				m.mqttCancel = nil
				m.cancelRequest = nil
				m.responseMeta = "MQTT connection cancelled"
			} else if isMQTTURL(m.urlInput.Value()) {
				return m, m.beginMQTTConnection()
			} else if m.webSocket != nil {
				m.responseMeta = "Disconnecting WebSocket..."
				closeWebSocketSession(m.webSocket)
			} else if m.webSocketCancel != nil {
				m.webSocketCancel()
				m.webSocketCancel = nil
				m.cancelRequest = nil
				m.responseMeta = "WebSocket connection cancelled"
			} else if isWebSocketURL(m.urlInput.Value()) {
				return m, m.beginWebSocketConnection()
			} else {
				m.responseMeta = "Connect supports WebSocket, Socket.IO, and MQTT URLs"
			}

		case m.collectionRenameOpen:
			switch msg.String() {
			case "esc":
				m.collectionRenameOpen = false
				m.collectionRenameInput.Blur()
			case "enter":
				name := strings.TrimSpace(m.collectionRenameInput.Value())
				m.collectionRenameOpen = false
				m.collectionRenameInput.Blur()
				if name != "" && m.sidebarMode == sidebarExamples {
					refs := m.savedExampleRefs()
					if m.examplePos >= 0 && m.examplePos < len(refs) {
						ref := refs[m.examplePos]
						m.savedRequests[ref.requestIndex].examples[ref.exampleIndex].name = name
						m.responseMeta = "Renamed saved example"
						m.saveWorkspaceWithStatus()
					}
				} else if name != "" && m.collectionPos >= 0 && m.collectionPos < len(m.savedRequests) {
					m.savedRequests[m.collectionPos].name = name
					m.activeSavedIndex = m.collectionPos
					m.responseMeta = "Renamed saved request"
					m.saveWorkspaceWithStatus()
				}
			default:
				var cmd tea.Cmd
				m.collectionRenameInput, cmd = m.collectionRenameInput.Update(msg)
				cmds = append(cmds, cmd)
			}

		case m.environmentNameOpen:
			switch msg.String() {
			case "esc":
				m.environmentNameOpen = false
				m.environmentCreating = false
				m.environmentNameInput.Blur()
			case "enter":
				name := strings.TrimSpace(m.environmentNameInput.Value())
				except := m.environmentPos
				if m.environmentCreating {
					except = -1
				}
				if !m.environmentNameAvailable(name, except) {
					m.responseMeta = "Environment name must be non-empty and unique"
					break
				}
				if m.environmentCreating {
					m.syncActiveEnvironment()
					m.environments = append(m.environments, environmentProfile{name: name})
					m.environmentPos = len(m.environments) - 1
					m.variablesInput.SetEntries(nil)
					m.responseMeta = "Created environment " + name
				} else {
					m.environments[m.environmentPos].name = name
					m.responseMeta = "Renamed environment to " + name
				}
				m.environmentNameOpen = false
				m.environmentCreating = false
				m.environmentNameInput.Blur()
				m.saveWorkspaceWithStatus()
			default:
				var cmd tea.Cmd
				m.environmentNameInput, cmd = m.environmentNameInput.Update(msg)
				cmds = append(cmds, cmd)
			}

		case m.responseSearchOpen:
			switch msg.String() {
			case "esc":
				m.responseSearchOpen = false
				m.responseSearchInput.Blur()
			case "enter":
				m.responseSearchOpen = false
				m.responseSearchInput.Blur()
				m.executeResponseSearch()
			default:
				var cmd tea.Cmd
				m.responseSearchInput, cmd = m.responseSearchInput.Update(msg)
				cmds = append(cmds, cmd)
			}

		case m.responseFilterOpen:
			switch msg.String() {
			case "esc":
				m.responseFilterOpen = false
				m.responseFilterInput.Blur()
			case "enter":
				m.responseFilterOpen = false
				m.responseFilterInput.Blur()
				m.executeResponseFilter()
			default:
				var cmd tea.Cmd
				m.responseFilterInput, cmd = m.responseFilterInput.Update(msg)
				cmds = append(cmds, cmd)
			}

		case m.responseSaveOpen:
			switch msg.String() {
			case "esc":
				m.responseSaveOpen = false
				m.responseSaveInput.Blur()
			case "enter":
				path := m.responseSaveInput.Value()
				written, err := m.saveActiveResponse(path)
				m.responseSaveOpen = false
				m.responseSaveInput.Blur()
				if err != nil {
					m.responseSearchStatus = "Save failed: " + err.Error()
				} else {
					m.responseSearchStatus = fmt.Sprintf("Saved %s (%s)", path, formatByteCount(written))
				}
			default:
				var cmd tea.Cmd
				m.responseSaveInput, cmd = m.responseSaveInput.Update(msg)
				cmds = append(cmds, cmd)
			}

		case key.Matches(msg, m.keymap.settings):
			m.settingsOpen = !m.settingsOpen
			m.environmentOpen = false
			m.environmentNameOpen = false
			m.inputMode = modeNormal
			m.settings.Blur()
			m.variablesInput.Blur()
			if m.settingsOpen {
				m.setFocus(paneRequest)
			}

		case key.Matches(msg, m.keymap.environment):
			wasOpen := m.environmentOpen
			m.environmentOpen = !m.environmentOpen
			m.settingsOpen = false
			m.inputMode = modeNormal
			m.settings.Blur()
			m.variablesInput.Blur()
			m.environmentNameOpen = false
			m.environmentNameInput.Blur()
			m.environmentPendingD = false
			if m.environmentOpen {
				m.setFocus(paneRequest)
				m.variablesInput.Focus()
			} else if wasOpen {
				m.saveWorkspaceWithStatus()
			}

		case key.Matches(msg, m.keymap.save):
			if m.urlInput.Value() != "" {
				request := m.captureCurrentRequest()
				if m.activeSavedIndex >= 0 && m.activeSavedIndex < len(m.savedRequests) {
					request.name = m.savedRequests[m.activeSavedIndex].name
					request.examples = m.savedRequests[m.activeSavedIndex].examples
					m.savedRequests[m.activeSavedIndex] = request
					m.collectionPos = m.activeSavedIndex
					m.responseMeta = "Updated saved request"
				} else {
					m.savedRequests = append(m.savedRequests, request)
					m.collectionPos = len(m.savedRequests) - 1
					m.activeSavedIndex = m.collectionPos
					m.responseMeta = "Saved request locally"
				}
				m.sidebarMode = sidebarCollections
				m.saveWorkspaceWithStatus()
			}

		case key.Matches(msg, m.keymap.saveExample):
			if err := m.saveCurrentResponseExample(); err != nil {
				m.responseSearchStatus = "Example not saved: " + err.Error()
			}

		case key.Matches(msg, m.keymap.exportCurl):
			if m.urlInput.Value() != "" {
				m.clearResponseFilter()
				m.response = CurlCommand(m.captureCurrentRequest())
				m.responseRaw = ""
				m.responseRawAvailable = false
				m.responseStatusCode = 0
				m.responseModel.SetContent(m.response)
				m.responseHeaders = ""
				m.responseHeadersModel.SetContent("")
				m.responseTests = ""
				m.responseTestsModel.SetContent("")
				m.assertionResults = nil
				m.responseMeta = "cURL command generated"
				m.responseTab = responseTabBody
				m.resetActiveResponseXOffset()
				m.setFocus(paneResponse)
			}

		case key.Matches(msg, m.keymap.exportResponse):
			if m.activeResponseExportContent() != "" {
				m.setFocus(paneResponse)
				m.responseSearchOpen = false
				m.responseSearchInput.Blur()
				m.responseSaveOpen = true
				m.responseSaveInput.SetValue("")
				cmds = append(cmds, m.responseSaveInput.Focus())
			}

		case key.Matches(msg, m.keymap.collections):
			m.sidebarMode = (m.sidebarMode + 1) % sidebarModeCount
			m.setFocus(paneHistory)

		case key.Matches(msg, m.keymap.cancel):
			if m.socketIO != nil {
				m.responseMeta = "Disconnecting Socket.IO..."
				return m, disconnectSocketIOSession(m.socketIO)
			} else if m.mqtt != nil {
				m.responseMeta = "Disconnecting MQTT..."
				return m, disconnectMQTTSession(m.mqtt)
			} else if m.webSocket != nil {
				m.responseMeta = "Disconnecting WebSocket..."
				closeWebSocketSession(m.webSocket)
			} else if m.cancelRequest != nil {
				m.cancelRequest()
				m.responseMeta = "Cancelling..."
			}

		case m.settingsOpen:
			if m.inputMode == modeInsert {
				if msg.String() == "esc" {
					m.inputMode = modeNormal
					m.settings.Blur()
				} else {
					cmds = append(cmds, m.settings.UpdateInput(msg))
				}
			} else if msg.String() == "i" && m.settings.Editable() {
				m.inputMode = modeInsert
				cmds = append(cmds, m.settings.FocusCurrent())
			} else {
				m.settings.UpdateNormal(msg.String())
			}

		case m.environmentOpen:
			if m.inputMode == modeInsert {
				if msg.String() == "esc" {
					m.inputMode = modeNormal
					m.variablesInput.blurAll()
				} else {
					cmds = append(cmds, m.variablesInput.UpdateInsert(msg))
				}
			} else if msg.String() == "i" {
				m.inputMode = modeInsert
				cmds = append(cmds, m.variablesInput.FocusCurrent())
			} else {
				keyStr := msg.String()
				switch keyStr {
				case "p":
					m.environmentPendingD = false
					m.activateEnvironmentIndex((m.environmentPos + 1) % len(m.environments))
					m.responseMeta = "Environment: " + m.activeEnvironmentName()
					m.saveWorkspaceWithStatus()
				case "n":
					m.environmentPendingD = false
					m.environmentCreating = true
					m.environmentNameOpen = true
					m.environmentNameInput.SetValue(m.nextEnvironmentName())
					cmds = append(cmds, m.environmentNameInput.Focus())
				case "r":
					m.environmentPendingD = false
					m.environmentCreating = false
					m.environmentNameOpen = true
					m.environmentNameInput.SetValue(m.activeEnvironmentName())
					cmds = append(cmds, m.environmentNameInput.Focus())
				case "d":
					if len(m.environments) == 1 {
						m.environmentPendingD = false
						m.responseMeta = "Cannot delete the only environment"
					} else if !m.environmentPendingD {
						m.environmentPendingD = true
						m.responseMeta = "Press d again to delete " + m.activeEnvironmentName()
					} else {
						deleted := m.activeEnvironmentName()
						m.environments = append(m.environments[:m.environmentPos], m.environments[m.environmentPos+1:]...)
						if m.environmentPos >= len(m.environments) {
							m.environmentPos = len(m.environments) - 1
						}
						m.variablesInput.SetEntries(m.environments[m.environmentPos].values)
						m.environmentPendingD = false
						m.responseMeta = "Deleted environment " + deleted
						m.saveWorkspaceWithStatus()
					}
				default:
					m.environmentPendingD = false
					m.variablesInput.UpdateNormal(keyStr)
				}
			}

		case !inInsert && key.Matches(msg, m.keymap.next):
			m.setFocus((m.focus + 1) % paneCount)

		case !inInsert && key.Matches(msg, m.keymap.prev):
			m.setFocus((m.focus - 1 + paneCount) % paneCount)

		case !inInsert && key.Matches(msg, m.keymap.cycleMethod):
			m.customMethod = ""
			m.methodIdx = (m.methodIdx + 1) % len(methods)

		case !inInsert && key.Matches(msg, m.keymap.editMethod):
			if isGRPCURL(m.urlInput.Value()) || isWebSocketURL(m.urlInput.Value()) || isSocketIOURL(m.urlInput.Value()) || isMQTTURL(m.urlInput.Value()) {
				m.responseMeta = "Custom methods apply to HTTP requests"
				break
			}
			m.methodEditOpen = true
			m.methodInput.SetValue(m.displayedMethod())
			m.methodInput.CursorEnd()
			cmds = append(cmds, m.methodInput.Focus())

		case !inInsert && key.Matches(msg, m.keymap.send) && (msg.String() != "enter" || m.focus == paneURL) && isWebSocketURL(m.urlInput.Value()):
			if m.webSocket == nil {
				return m, m.beginWebSocketConnection()
			}
			messageType, payload, err := m.webSocketPayload()
			if err != nil {
				m.responseMeta = "WebSocket message failed: " + err.Error()
				break
			}
			return m, sendWebSocketMessage(m.webSocket, messageType, payload)

		case !inInsert && key.Matches(msg, m.keymap.send) && (msg.String() != "enter" || m.focus == paneURL) && isMQTTURL(m.urlInput.Value()):
			if m.mqtt == nil {
				return m, m.beginMQTTConnection()
			}
			_, payload, err := m.webSocketPayload()
			if err != nil {
				m.responseMeta = "MQTT message failed: " + err.Error()
				break
			}
			return m, sendMQTTMessage(m.mqtt, payload)

		case !inInsert && key.Matches(msg, m.keymap.send) && (msg.String() != "enter" || m.focus == paneURL) && isSocketIOURL(m.urlInput.Value()):
			if m.socketIO == nil {
				return m, m.beginSocketIOConnection()
			}
			_, payload, err := m.webSocketPayload()
			if err != nil {
				m.responseMeta = "Socket.IO event failed: " + err.Error()
				break
			}
			return m, sendSocketIOEvent(m.socketIO, m.socketIO.config.event, socketIOArgs(payload))

		case !inInsert && key.Matches(msg, m.keymap.send) && (msg.String() != "enter" || m.focus == paneURL):
			m.clearResponseFilter()
			if m.socketIO != nil {
				closeSocketIOSession(m.socketIO)
				m.socketIO = nil
			}
			if m.mqtt != nil {
				terminateMQTTSession(m.mqtt)
				m.mqtt = nil
			}
			if m.webSocket != nil {
				closeWebSocketSession(m.webSocket)
			}
			method := m.displayedMethod()
			url := m.urlInput.Value()
			if url != "" {
				m.response = fmt.Sprintf("Sending request %s %s ...", method, url)
				m.responseModel.SetContent(m.response)
				m.responseHeaders = ""
				m.responseHeadersModel.SetContent("")
				m.responseTests = ""
				m.responseTestsModel.SetContent("")
				m.assertionResults = nil
				requestBody := m.bodyInput.Value()
				requestId := uuid.New()
				requestContext, cancelRequest := context.WithCancel(context.Background())
				m.requestId = requestId
				m.requestContext = requestContext
				m.cancelRequest = cancelRequest
				m.historyPos = 0
				m.responseMeta = "Sending..."
				m.responseRaw = ""
				m.responseRawAvailable = false
				m.responseStatusCode = 0
				m.history = append([]historyItem{{
					createdAt:         time.Now().UTC(),
					method:            method,
					url:               url,
					requestBody:       requestBody,
					requestHeaders:    m.headersInput.Entries(),
					requestParams:     m.paramsInput.Entries(),
					requestAuth:       m.authInput.Config(),
					requestBodyConfig: m.bodyConfig(),
					requestCookies:    m.cookiesInput.Entries(),
					requestTests:      m.testsInput.Entries(),
					requestID:         requestId,
				}}, m.history...)
				m.history = trimHistory(m.history)
				return m, m.DoRequest()
			}

		default:
			switch m.focus {
			case paneURL:
				var cmd tea.Cmd
				m.urlInput, cmd = m.urlInput.Update(msg)
				cmds = append(cmds, cmd)
			case paneRequest:
				keyStr := msg.String()
				if m.inputMode == modeInsert {
					if keyStr == "esc" {
						m.inputMode = modeNormal
						m.bodyInput.Blur()
						m.headersInput.blurAll()
						m.paramsInput.blurAll()
						m.authInput.blurAll()
						m.blurBodyEditor()
						m.cookiesInput.blurAll()
						m.testsInput.blurAll()
					} else if m.requestTab == requestTabHeaders {
						cmds = append(cmds, m.headersInput.UpdateInsert(msg))
					} else if m.requestTab == requestTabParams {
						cmds = append(cmds, m.paramsInput.UpdateInsert(msg))
					} else if m.requestTab == requestTabAuth {
						cmds = append(cmds, m.authInput.UpdateInsert(msg))
					} else if m.requestTab == requestTabBody {
						cmds = append(cmds, m.updateBodyInsert(msg))
					} else if m.requestTab == requestTabCookies {
						cmds = append(cmds, m.cookiesInput.UpdateInsert(msg))
					} else if m.requestTab == requestTabTests {
						cmds = append(cmds, m.testsInput.UpdateInsert(msg))
					}
				} else {
					// Normal mode
					switch keyStr {
					case "i":
						if m.requestTab == requestTabAuth && !m.authInput.Editable() {
							break
						}
						if m.requestTab == requestTabBody && !m.bodyEditable() {
							break
						}
						m.inputMode = modeInsert
						switch m.requestTab {
						case requestTabHeaders:
							cmd := m.headersInput.FocusCurrent()
							cmds = append(cmds, cmd)
						case requestTabParams:
							cmds = append(cmds, m.paramsInput.FocusCurrent())
						case requestTabAuth:
							cmds = append(cmds, m.authInput.FocusCurrent())
						case requestTabBody:
							cmds = append(cmds, m.focusBodyInput())
						case requestTabCookies:
							cmds = append(cmds, m.cookiesInput.FocusCurrent())
						case requestTabTests:
							cmds = append(cmds, m.testsInput.FocusCurrent())
						}
					case "left", "right":
						m.handleRequestKeys(keyStr)
						m.syncRequestTabFocus()
					default:
						switch m.requestTab {
						case requestTabHeaders:
							m.headersInput.UpdateNormal(keyStr)
						case requestTabParams:
							m.paramsInput.UpdateNormal(keyStr)
						case requestTabAuth:
							if keyStr == "g" {
								if m.authInput.Config().typeID != authOAuth2AuthorizationCode {
									m.responseMeta = "Token login is available for OAuth 2 Authorization Code"
									break
								}
								if m.cancelRequest != nil {
									m.cancelRequest()
								}
								ctx, cancel := context.WithCancel(context.Background())
								id := uuid.New()
								m.oauthLoginID = id
								m.requestContext, m.cancelRequest = ctx, cancel
								m.responseMeta = "Opening OAuth 2 authorization in the system browser..."
								cmds = append(cmds, m.loginOAuth2AuthorizationCode(ctx, id))
							} else {
								m.authInput.UpdateNormal(keyStr)
							}
						case requestTabBody:
							m.updateBodyNormal(keyStr)
						case requestTabCookies:
							m.cookiesInput.UpdateNormal(keyStr)
						case requestTabTests:
							m.testsInput.UpdateNormal(keyStr)
						}
					}
				}
			case paneHistory:
				m.handleHistoryKeys(msg.String())
			case paneResponse:
				keyStr := msg.String()
				switch keyStr {
				case "/":
					m.responseSaveOpen = false
					m.responseSaveInput.Blur()
					m.responseSearchOpen = true
					cmds = append(cmds, m.responseSearchInput.Focus())
				case "n":
					m.navigateResponseMatch(1)
				case "N":
					m.navigateResponseMatch(-1)
				case "f":
					m.responseSearchOpen = false
					m.responseSearchInput.Blur()
					m.responseSaveOpen = false
					m.responseSaveInput.Blur()
					m.responseTab = responseTabBody
					m.responseFilterOpen = true
					cmds = append(cmds, m.responseFilterInput.Focus())
				case "F":
					m.clearResponseFilter()
				default:
					if m.handleResponseKeys(keyStr) {
						break
					}
					var cmd tea.Cmd
					switch m.responseTab {
					case responseTabHeaders:
						m.responseHeadersModel, cmd = m.responseHeadersModel.Update(msg)
					case responseTabTests:
						m.responseTestsModel, cmd = m.responseTestsModel.Update(msg)
					default:
						m.responseModel, cmd = m.responseModel.Update(msg)
					}
					cmds = append(cmds, cmd)
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sizeComponents()

	case tea.MouseReleaseMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}

		if zone.Get("method").InBounds(msg) {
			m.setFocus(paneURL)
			m.customMethod = ""
			m.methodIdx = (m.methodIdx + 1) % len(methods)
		} else if zone.Get("url").InBounds(msg) {
			m.setFocus(paneURL)
		} else if zone.Get("bodyTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabBody
			m.syncRequestTabFocus()
		} else if zone.Get("headersTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabHeaders
			m.syncRequestTabFocus()
		} else if zone.Get("authTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabAuth
			m.syncRequestTabFocus()
		} else if zone.Get("paramsTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabParams
			m.syncRequestTabFocus()
		} else if zone.Get("cookiesTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabCookies
			m.syncRequestTabFocus()
		} else if zone.Get("testsTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabTests
			m.syncRequestTabFocus()
		} else if zone.Get("request").InBounds(msg) {
			m.setFocus(paneRequest)
			m.syncRequestTabFocus()
			// TODO: Ability to click individual history items
		} else if zone.Get("history").InBounds(msg) {
			m.setFocus(paneHistory)
		} else if zone.Get("responseTabBody").InBounds(msg) {
			m.setFocus(paneResponse)
			m.responseTab = responseTabBody
			m.resetActiveResponseXOffset()
		} else if zone.Get("responseTabHeaders").InBounds(msg) {
			m.setFocus(paneResponse)
			m.responseTab = responseTabHeaders
			m.resetActiveResponseXOffset()
		} else if zone.Get("responseTabTests").InBounds(msg) {
			m.setFocus(paneResponse)
			m.responseTab = responseTabTests
			m.resetActiveResponseXOffset()
		} else if zone.Get("response").InBounds(msg) {
			m.setFocus(paneResponse)
		}

		return m, nil
	}

	// Forward non-key messages (like blink ticks) to focused input.
	if _, isKey := msg.(tea.KeyMsg); !isKey {
		if m.collectionRenameOpen {
			var cmd tea.Cmd
			m.collectionRenameInput, cmd = m.collectionRenameInput.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.environmentNameOpen {
			var cmd tea.Cmd
			m.environmentNameInput, cmd = m.environmentNameInput.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.focus == paneURL {
			var cmd tea.Cmd
			m.urlInput, cmd = m.urlInput.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.focus == paneRequest && m.inputMode == modeInsert {
			if m.environmentOpen {
				cmds = append(cmds, m.variablesInput.UpdateInsert(msg))
			} else if m.settingsOpen {
				cmds = append(cmds, m.settings.UpdateInput(msg))
			} else {
				switch m.requestTab {
				case requestTabBody:
					cmds = append(cmds, m.updateBodyInsert(msg))
				case requestTabHeaders:
					cmd := m.headersInput.UpdateInsert(msg)
					cmds = append(cmds, cmd)
				case requestTabParams:
					cmds = append(cmds, m.paramsInput.UpdateInsert(msg))
				case requestTabAuth:
					cmds = append(cmds, m.authInput.UpdateInsert(msg))
				case requestTabCookies:
					cmds = append(cmds, m.cookiesInput.UpdateInsert(msg))
				case requestTabTests:
					cmds = append(cmds, m.testsInput.UpdateInsert(msg))
				}
			}
		}
		if m.responseSearchOpen {
			var cmd tea.Cmd
			m.responseSearchInput, cmd = m.responseSearchInput.Update(msg)
			cmds = append(cmds, cmd)
		} else if m.responseFilterOpen {
			var cmd tea.Cmd
			m.responseFilterInput, cmd = m.responseFilterInput.Update(msg)
			cmds = append(cmds, cmd)
		} else if m.responseSaveOpen {
			var cmd tea.Cmd
			m.responseSaveInput, cmd = m.responseSaveInput.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *model) setFocus(p pane) {
	m.focus = p
	m.inputMode = modeNormal

	if p == paneURL {
		m.urlInput.Focus()
	} else {
		m.urlInput.Blur()
	}

	// When entering request pane, start in normal mode (nothing focused).
	// When leaving, blur everything.
	m.bodyInput.Blur()
	m.headersInput.Blur()
	m.paramsInput.Blur()
	m.authInput.Blur()
	m.blurBodyEditor()
	m.cookiesInput.Blur()
	m.testsInput.Blur()
	m.variablesInput.Blur()

	if p == paneRequest {
		m.syncRequestTabFocus()
	}
}

func (m *model) syncRequestTabFocus() {
	m.headersInput.Blur()
	m.paramsInput.Blur()
	m.authInput.Blur()
	m.blurBodyEditor()
	m.cookiesInput.Blur()
	m.testsInput.Blur()
	switch m.requestTab {
	case requestTabHeaders:
		m.headersInput.Focus()
	case requestTabParams:
		m.paramsInput.Focus()
	case requestTabAuth:
		m.authInput.Focus()
	case requestTabBody:
		m.focusBodyEditor()
	case requestTabCookies:
		m.cookiesInput.Focus()
	case requestTabTests:
		m.testsInput.Focus()
	}
}

func (m *model) sizeComponents() {
	mainWidth, _, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)

	m.urlInput.SetWidth(mainWidth - methodWidth - 4)

	m.bodyInput.SetWidth(mainWidth - 2)
	m.bodyInput.SetHeight(bodyHeight)
	m.formInput.SetWidth(mainWidth - 4)
	m.formInput.SetHeight(bodyHeight - 1)
	m.multipartInput.SetWidth(mainWidth - 4)
	m.multipartInput.SetHeight(bodyHeight - 1)
	m.binaryPathInput.SetWidth(max(10, mainWidth-12))
	m.graphqlQueryInput.SetWidth(max(10, mainWidth-4))
	m.graphqlVariablesInput.SetWidth(max(10, mainWidth-4))
	m.graphqlOperationInput.SetWidth(max(10, mainWidth-18))
	graphqlHeight := max(1, (bodyHeight-5)/2)
	m.graphqlQueryInput.SetHeight(graphqlHeight)
	m.graphqlVariablesInput.SetHeight(graphqlHeight)
	m.headersInput.SetWidth(mainWidth - 4)
	m.headersInput.SetHeight(bodyHeight)
	m.paramsInput.SetWidth(mainWidth - 4)
	m.paramsInput.SetHeight(bodyHeight)
	m.authInput.SetWidth(mainWidth - 4)
	m.cookiesInput.SetWidth(mainWidth - 4)
	m.cookiesInput.SetHeight(bodyHeight)
	m.testsInput.SetWidth(mainWidth - 4)
	m.testsInput.SetHeight(bodyHeight)
	m.settings.SetWidth(mainWidth - 4)
	m.variablesInput.SetWidth(mainWidth - 4)
	m.variablesInput.SetHeight(bodyHeight)

	// Persist viewport geometry on the model. View has a value receiver, so
	// sizing these inside viewResponse only changes a temporary copy and causes
	// scrolling to calculate offsets with a zero-height viewport.
	viewportHeight := responseHeight - 4 // border frame, tab bar, and top padding
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	viewportWidth := mainWidth - 3 // border frame and left content padding
	if viewportWidth < 1 {
		viewportWidth = 1
	}
	m.responseModel.SetWidth(viewportWidth)
	m.responseModel.SetHeight(viewportHeight)
	m.responseHeadersModel.SetWidth(viewportWidth)
	m.responseHeadersModel.SetHeight(viewportHeight)
	m.responseTestsModel.SetWidth(viewportWidth)
	m.responseTestsModel.SetHeight(viewportHeight)
	m.responseSearchInput.SetWidth(max(10, innerPromptWidth(mainWidth)))
	m.responseSaveInput.SetWidth(max(10, innerPromptWidth(mainWidth)))
	m.collectionRenameInput.SetWidth(max(10, historyWidth-11))
	m.environmentNameInput.SetWidth(max(10, mainWidth-28))
}

func layoutDimensions(width, height int) (mainWidth, contentHeight, bodyHeight, responseHeight int) {
	// Keep the composed layout one column narrower than the terminal. Drawing
	// the right border in the terminal's final column can trigger autowrap and
	// create a phantom row, which makes the response bottom appear one row above
	// the history bottom even though Lip Gloss reports equal heights.
	mainWidth = width - historyWidth - 5
	if mainWidth < 20 {
		mainWidth = 20
	}

	contentHeight = height - helpHeight - 2
	bodyHeight = (contentHeight - urlBarHeight - 2) / 2
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	requestSectionHeight := bodyHeight + 2 // bordered content plus custom bottom border
	responseHeight = contentHeight - urlBarHeight - requestSectionHeight
	if responseHeight < 3 {
		responseHeight = 3
	}
	return mainWidth, contentHeight, bodyHeight, responseHeight
}

func (m model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.ReportFocus = true
	v.MouseMode = tea.MouseModeCellMotion

	if m.width == 0 {
		v.SetContent("Loading...")
		return v
	}

	mainWidth, contentHeight, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)

	// Render each pane via its own file's method
	urlSection := zone.Mark("url", m.viewURL(mainWidth))

	requestSection := m.viewRequest(mainWidth, bodyHeight)
	responseSection := m.viewResponse(mainWidth, responseHeight)

	rightCol := lipgloss.JoinVertical(lipgloss.Left, urlSection, requestSection, responseSection)
	historySection := m.viewHistory(contentHeight)

	layout := lipgloss.JoinHorizontal(lipgloss.Top, historySection, rightCol)

	helpView := helpStyle.Render(m.help.ShortHelpView([]key.Binding{
		m.keymap.next,
		m.keymap.prev,
		m.keymap.cycleMethod,
		m.keymap.editMethod,
		m.keymap.send,
		m.keymap.cancel,
		m.keymap.connect,
		m.keymap.save,
		m.keymap.saveExample,
		m.keymap.exportCurl,
		m.keymap.exportResponse,
		m.keymap.collections,
		m.keymap.settings,
		m.keymap.environment,
		m.keymap.quit,
	}))

	v.SetContent(zone.Scan(layout + "\n" + helpView))
	return v
}

func formatHeaders(h http.Header) string {
	if len(h) == 0 {
		return "(no headers)"
	}

	// Sort keys for consistent output
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	return b.String()
}

package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	uuid "github.com/google/uuid"
)

const (
	workspaceVersion       = 1
	maxHistoryEntries      = 100
	maxHistoryStorageBytes = 25 << 20
)

type savedRequest struct {
	name     string
	method   string
	url      string
	headers  []headerEntry
	params   []headerEntry
	auth     authConfig
	body     bodyConfig
	cookies  []headerEntry
	tests    []headerEntry
	examples []savedExample
}

type savedExample struct {
	name                 string
	statusCode           int
	responseBody         string
	responseRaw          string
	responseRawAvailable bool
	responseHeaders      string
	responseMeta         string
}

type workspaceFile struct {
	Version           int                     `json:"version"`
	Environment       []workspaceEntry        `json:"environment,omitempty"`
	Environments      []workspaceEnvironment  `json:"environments,omitempty"`
	ActiveEnvironment string                  `json:"active_environment,omitempty"`
	Requests          []workspaceSavedRequest `json:"requests,omitempty"`
	History           []workspaceHistoryItem  `json:"history,omitempty"`
	Settings          *workspaceSettings      `json:"settings,omitempty"`
	Cookies           []storedCookie          `json:"cookies,omitempty"`
}

type workspaceEnvironment struct {
	Name   string           `json:"name"`
	Values []workspaceEntry `json:"values,omitempty"`
}

type workspaceHistoryItem struct {
	ID                   string                `json:"id"`
	CreatedAt            string                `json:"created_at,omitempty"`
	Request              workspaceSavedRequest `json:"request"`
	ResponseBody         string                `json:"response_body,omitempty"`
	ResponseRaw          string                `json:"response_raw,omitempty"`
	ResponseRawAvailable bool                  `json:"response_raw_available,omitempty"`
	ResponseHeaders      string                `json:"response_headers,omitempty"`
	ResponseMeta         string                `json:"response_meta,omitempty"`
	ResponseTests        string                `json:"response_tests,omitempty"`
}

type workspaceSettings struct {
	FollowRedirects   *bool  `json:"follow_redirects,omitempty"`
	HTTPVersion       string `json:"http_version,omitempty"`
	SkipTLSVerify     bool   `json:"skip_tls_verify,omitempty"`
	Timeout           string `json:"timeout,omitempty"`
	ProxyURL          string `json:"proxy_url,omitempty"`
	ProxyBypass       string `json:"proxy_bypass,omitempty"`
	CACertPath        string `json:"ca_cert_path,omitempty"`
	ClientCertPath    string `json:"client_cert_path,omitempty"`
	ClientKeyPath     string `json:"client_key_path,omitempty"`
	ClientPFXPath     string `json:"client_pfx_path,omitempty"`
	ClientPFXPassword string `json:"client_pfx_password,omitempty"`
}

type workspaceEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type workspaceSavedRequest struct {
	Name     string             `json:"name"`
	Method   string             `json:"method"`
	URL      string             `json:"url"`
	Headers  []workspaceEntry   `json:"headers,omitempty"`
	Query    []workspaceEntry   `json:"query,omitempty"`
	Cookies  []workspaceEntry   `json:"cookies,omitempty"`
	Tests    []workspaceEntry   `json:"tests,omitempty"`
	Auth     workspaceAuth      `json:"auth"`
	Body     workspaceBody      `json:"body"`
	Examples []workspaceExample `json:"examples,omitempty"`
}

type workspaceExample struct {
	Name                 string `json:"name"`
	StatusCode           int    `json:"status_code,omitempty"`
	ResponseBody         string `json:"response_body,omitempty"`
	ResponseRaw          string `json:"response_raw,omitempty"`
	ResponseRawAvailable bool   `json:"response_raw_available,omitempty"`
	ResponseHeaders      string `json:"response_headers,omitempty"`
	ResponseMeta         string `json:"response_meta,omitempty"`
}

type workspaceAuth struct {
	Type                   string `json:"type"`
	BearerToken            string `json:"bearer_token,omitempty"`
	JWTAlgorithm           string `json:"jwt_algorithm,omitempty"`
	JWTKey                 string `json:"jwt_key,omitempty"`
	JWTSecretBase64        bool   `json:"jwt_secret_base64,omitempty"`
	JWTPayload             string `json:"jwt_payload,omitempty"`
	JWTHeaders             string `json:"jwt_headers,omitempty"`
	JWTPrefix              string `json:"jwt_prefix,omitempty"`
	JWTLocation            string `json:"jwt_location,omitempty"`
	JWTQueryName           string `json:"jwt_query_name,omitempty"`
	Username               string `json:"username,omitempty"`
	Password               string `json:"password,omitempty"`
	APIKeyName             string `json:"api_key_name,omitempty"`
	APIKeyValue            string `json:"api_key_value,omitempty"`
	APIKeyLocation         string `json:"api_key_location,omitempty"`
	OAuthTokenURL          string `json:"oauth_token_url,omitempty"`
	OAuthAuthorizationURL  string `json:"oauth_authorization_url,omitempty"`
	OAuthClientID          string `json:"oauth_client_id,omitempty"`
	OAuthClientSecret      string `json:"oauth_client_secret,omitempty"`
	OAuthScope             string `json:"oauth_scope,omitempty"`
	OAuthRefreshToken      string `json:"oauth_refresh_token,omitempty"`
	OAuthCallbackURL       string `json:"oauth_callback_url,omitempty"`
	OAuthAccessToken       string `json:"oauth_access_token,omitempty"`
	OAuthTokenType         string `json:"oauth_token_type,omitempty"`
	OAuthAccessTokenExpiry int64  `json:"oauth_access_token_expiry,omitempty"`
	OAuthClientAuth        string `json:"oauth_client_auth,omitempty"`
	OAuthPKCE              bool   `json:"oauth_pkce,omitempty"`
	HawkID                 string `json:"hawk_id,omitempty"`
	HawkKey                string `json:"hawk_key,omitempty"`
	HawkAlgorithm          string `json:"hawk_algorithm,omitempty"`
	HawkExt                string `json:"hawk_ext,omitempty"`
	NTLMDomain             string `json:"ntlm_domain,omitempty"`
	NTLMWorkstation        string `json:"ntlm_workstation,omitempty"`
	OAuth1ConsumerKey      string `json:"oauth1_consumer_key,omitempty"`
	OAuth1ConsumerSecret   string `json:"oauth1_consumer_secret,omitempty"`
	OAuth1Token            string `json:"oauth1_token,omitempty"`
	OAuth1TokenSecret      string `json:"oauth1_token_secret,omitempty"`
	OAuth1PrivateKey       string `json:"oauth1_private_key,omitempty"`
	OAuth1SignatureMethod  string `json:"oauth1_signature_method,omitempty"`
	OAuth1Realm            string `json:"oauth1_realm,omitempty"`
	OAuth1Callback         string `json:"oauth1_callback,omitempty"`
	OAuth1Verifier         string `json:"oauth1_verifier,omitempty"`
	OAuth1IncludeBodyHash  bool   `json:"oauth1_include_body_hash,omitempty"`
	OAuth1Location         string `json:"oauth1_location,omitempty"`
	AWSAccessKey           string `json:"aws_access_key,omitempty"`
	AWSSecretKey           string `json:"aws_secret_key,omitempty"`
	AWSRegion              string `json:"aws_region,omitempty"`
	AWSService             string `json:"aws_service,omitempty"`
	AWSSessionToken        string `json:"aws_session_token,omitempty"`
}

type workspaceBody struct {
	Mode                 string           `json:"mode"`
	RawType              string           `json:"raw_type,omitempty"`
	Raw                  string           `json:"raw,omitempty"`
	Form                 []workspaceEntry `json:"form,omitempty"`
	Multipart            []workspaceEntry `json:"multipart,omitempty"`
	BinaryPath           string           `json:"binary_path,omitempty"`
	GraphQLQuery         string           `json:"graphql_query,omitempty"`
	GraphQLVariables     string           `json:"graphql_variables,omitempty"`
	GraphQLOperationName string           `json:"graphql_operation_name,omitempty"`
}

func DefaultWorkspacePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	return filepath.Join(configDir, "courier", "workspace.json"), nil
}

func NewModelWithWorkspace(path string) (model, error) {
	m := NewModel()
	if err := m.LoadWorkspace(path); err != nil {
		return m, err
	}
	return m, nil
}

func (m *model) LoadWorkspace(path string) error {
	m.workspacePath = path
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data, snapshotErr := m.workspaceData()
		if snapshotErr != nil {
			return fmt.Errorf("snapshot new workspace: %w", snapshotErr)
		}
		rememberModelWorkspaceSnapshot(m, path, workspaceDiskSnapshot{}, data)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read workspace: %w", err)
	}

	var file workspaceFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("decode workspace: %w", err)
	}
	if err := m.applyWorkspaceFile(file); err != nil {
		return err
	}
	stateData, err := m.workspaceData()
	if err != nil {
		return fmt.Errorf("snapshot loaded workspace: %w", err)
	}
	rememberModelWorkspaceSnapshot(m, path, workspaceSnapshotForData(data), stateData)
	return nil
}

func (m *model) applyWorkspaceFile(file workspaceFile) error {
	if file.Version != workspaceVersion {
		return fmt.Errorf("unsupported workspace version %d", file.Version)
	}
	if err := m.loadWorkspaceEnvironments(file); err != nil {
		return err
	}
	if file.Settings != nil {
		if err := m.applyWorkspaceSettings(*file.Settings); err != nil {
			return err
		}
	}
	if jar := m.persistentJar(); jar != nil {
		jar.Clear()
		jar.Restore(file.Cookies)
	}
	m.savedRequests = make([]savedRequest, 0, len(file.Requests))
	for _, request := range file.Requests {
		m.savedRequests = append(m.savedRequests, request.fromWorkspace())
	}
	m.history = make([]historyItem, 0, len(file.History))
	for index, item := range file.History {
		history, historyErr := item.fromWorkspace()
		if historyErr != nil {
			return fmt.Errorf("decode workspace history item %d: %w", index+1, historyErr)
		}
		m.history = append(m.history, history)
	}
	m.history = trimHistory(m.history)
	m.historyPos = 0
	return nil
}

func (m *model) SaveWorkspace() error {
	if m.workspacePath == "" {
		return nil
	}
	return withWorkspaceLock(m.workspacePath, func() error {
		current, err := workspaceSnapshotOnDisk(m.workspacePath)
		if err != nil {
			return fmt.Errorf("read workspace before saving: %w", err)
		}
		if err := validateModelWorkspaceSnapshot(m, m.workspacePath, current); err != nil {
			return err
		}
		data, err := m.workspaceData()
		if err != nil {
			return err
		}
		if err := writeWorkspaceAtomically(m.workspacePath, data); err != nil {
			return err
		}
		rememberModelWorkspaceSnapshot(m, m.workspacePath, workspaceSnapshotForData(data), data)
		return nil
	})
}

func (m *model) workspaceData() ([]byte, error) {
	m.settings.syncConfig()
	m.syncActiveEnvironment()
	m.history = trimHistory(m.history)
	followRedirects := m.settings.config.followRedirects
	file := workspaceFile{
		Version:           workspaceVersion,
		Environment:       toWorkspaceEntries(m.environments[m.environmentPos].values),
		Environments:      make([]workspaceEnvironment, 0, len(m.environments)),
		ActiveEnvironment: m.environments[m.environmentPos].name,
		Requests:          make([]workspaceSavedRequest, 0, len(m.savedRequests)),
		History:           make([]workspaceHistoryItem, 0, len(m.history)),
		Settings: &workspaceSettings{
			FollowRedirects:   &followRedirects,
			HTTPVersion:       workspaceHTTPVersion(m.settings.config.httpVersion),
			SkipTLSVerify:     m.settings.config.skipTLSVerify,
			Timeout:           m.settings.config.timeout.String(),
			ProxyURL:          m.settings.config.proxyURL,
			ProxyBypass:       m.settings.config.proxyBypass,
			CACertPath:        m.settings.config.caCertPath,
			ClientCertPath:    m.settings.config.clientCertPath,
			ClientKeyPath:     m.settings.config.clientKeyPath,
			ClientPFXPath:     m.settings.config.clientPFXPath,
			ClientPFXPassword: m.settings.config.clientPFXPassword,
		},
	}
	if jar := m.persistentJar(); jar != nil {
		file.Cookies = jar.Snapshot()
	}
	for _, environment := range m.environments {
		file.Environments = append(file.Environments, workspaceEnvironment{Name: environment.name, Values: toWorkspaceEntries(environment.values)})
	}
	for _, request := range m.savedRequests {
		file.Requests = append(file.Requests, request.toWorkspace())
	}
	for _, item := range m.history {
		file.History = append(file.History, item.toWorkspace())
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode workspace: %w", err)
	}
	data = append(data, '\n')
	return data, nil
}

func (m *model) loadWorkspaceEnvironments(file workspaceFile) error {
	if len(file.Environments) == 0 {
		m.environments = []environmentProfile{{name: defaultEnvironmentName, values: fromWorkspaceEntries(file.Environment)}}
		m.environmentPos = 0
		m.variablesInput.SetEntries(m.environments[0].values)
		return nil
	}
	m.environments = make([]environmentProfile, 0, len(file.Environments))
	seen := make(map[string]struct{}, len(file.Environments))
	for index, environment := range file.Environments {
		name := strings.TrimSpace(environment.Name)
		if name == "" {
			return fmt.Errorf("decode workspace environment %d: name is empty", index+1)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("decode workspace environment %d: duplicate name %q", index+1, name)
		}
		seen[name] = struct{}{}
		m.environments = append(m.environments, environmentProfile{name: name, values: fromWorkspaceEntries(environment.Values)})
	}
	m.environmentPos = 0
	if file.ActiveEnvironment != "" {
		for index := range m.environments {
			if m.environments[index].name == file.ActiveEnvironment {
				m.environmentPos = index
				break
			}
		}
	}
	m.variablesInput.SetEntries(m.environments[m.environmentPos].values)
	return nil
}

func (m *model) applyWorkspaceSettings(settings workspaceSettings) error {
	config := m.settings.config
	if settings.FollowRedirects != nil {
		config.followRedirects = *settings.FollowRedirects
	}
	config.skipTLSVerify = settings.SkipTLSVerify
	switch settings.HTTPVersion {
	case "", "auto":
		config.httpVersion = httpVersionAuto
	case "http1":
		config.httpVersion = httpVersion1
	case "http2":
		config.httpVersion = httpVersion2
	default:
		return fmt.Errorf("decode workspace HTTP version %q", settings.HTTPVersion)
	}
	if settings.Timeout != "" {
		timeout, err := time.ParseDuration(settings.Timeout)
		if err != nil || timeout < 0 {
			return fmt.Errorf("decode workspace settings timeout %q", settings.Timeout)
		}
		config.timeout = timeout
	}
	config.proxyURL = settings.ProxyURL
	config.proxyBypass = settings.ProxyBypass
	config.caCertPath = settings.CACertPath
	config.clientCertPath = settings.ClientCertPath
	config.clientKeyPath = settings.ClientKeyPath
	config.clientPFXPath = settings.ClientPFXPath
	config.clientPFXPassword = settings.ClientPFXPassword
	m.settings.SetConfig(config)
	return nil
}

func workspaceHTTPVersion(version httpVersion) string {
	switch version {
	case httpVersion1:
		return "http1"
	case httpVersion2:
		return "http2"
	default:
		return "auto"
	}
}

func (m *model) restoreRememberedWorkspace() error {
	data, ok := modelWorkspaceSnapshotData(m, m.workspacePath)
	if !ok {
		return fmt.Errorf("no loaded workspace snapshot is available")
	}
	var file workspaceFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("decode loaded workspace snapshot: %w", err)
	}

	historyPos := m.historyPos
	collectionPos := m.collectionPos
	examplePos := m.examplePos
	cookiePos := m.cookiePos
	settingsPageIndex := m.settings.page
	settingsCursor := m.settings.cursor
	environmentCursorRow := m.variablesInput.cursorRow
	environmentCursorCol := m.variablesInput.cursorCol
	if err := m.applyWorkspaceFile(file); err != nil {
		return fmt.Errorf("restore loaded workspace snapshot: %w", err)
	}
	m.historyPos = clampWorkspacePosition(historyPos, len(m.history))
	m.collectionPos = clampWorkspacePosition(collectionPos, len(m.savedRequests))
	m.examplePos = clampWorkspacePosition(examplePos, len(m.savedExampleRefs()))
	m.cookiePos = clampWorkspacePosition(cookiePos, len(m.Cookies()))
	m.settings.page = settingsPageIndex
	if m.settings.page < 0 || m.settings.page >= settingsPageCount {
		m.settings.page = settingsNetwork
	}
	m.settings.cursor = clampWorkspacePosition(settingsCursor, m.settings.fieldCount())
	m.restoreEnvironmentCursor(environmentCursorRow, environmentCursorCol)
	if m.settingsOpen || m.environmentOpen {
		// Restoring Settings or Environment rebuilds and blurs its inputs, so
		// retaining insert mode would leave the UI claiming to edit an input
		// that can no longer receive keystrokes.
		m.inputMode = modeNormal
	}
	// Saved requests have no stable identity, so a stale numeric index cannot
	// safely be rebound after rollback—even when the list lengths happen to
	// match. Keep the current draft but detach it from the restored collection.
	m.activeSavedIndex = -1
	if !m.requestDraftDirty() {
		m.requestDraftBaseline = savedRequest{}
	}
	return nil
}

func clampWorkspacePosition(position, count int) int {
	if count == 0 || position < 0 {
		return 0
	}
	if position >= count {
		return count - 1
	}
	return position
}

func (m *model) restoreEnvironmentCursor(row, column int) {
	m.variablesInput.cursorRow = clampWorkspacePosition(row, len(m.variablesInput.rows))
	m.variablesInput.cursorCol = clampWorkspacePosition(column, 2)
}

type workspaceSaveResult uint8

const (
	workspaceSaveFailed workspaceSaveResult = iota
	workspaceSaveSucceeded
	workspaceSaveConflictHandled
)

func (result workspaceSaveResult) succeeded() bool {
	return result == workspaceSaveSucceeded
}

func (m *model) saveWorkspaceWithStatus() workspaceSaveResult {
	if err := m.SaveWorkspace(); err != nil {
		result := workspaceSaveFailed
		if errors.Is(err, ErrWorkspaceConflict) {
			if restoreErr := m.restoreRememberedWorkspace(); restoreErr != nil {
				err = fmt.Errorf("%v; failed to roll back rejected changes: %w", err, restoreErr)
			} else {
				result = workspaceSaveConflictHandled
			}
		}
		m.workspaceSaveStatus = "Workspace save failed: " + err.Error()
		return result
	}
	m.workspaceSaveStatus = ""
	return workspaceSaveSucceeded
}

func (m *model) captureCurrentRequest() savedRequest {
	method := m.displayedMethod()
	urlValue := m.urlInput.Value()
	return savedRequest{
		name:    strings.TrimSpace(urlValue),
		method:  method,
		url:     urlValue,
		headers: m.headersInput.Entries(),
		params:  m.paramsInput.Entries(),
		auth:    m.authInput.Config(),
		body:    m.bodyConfig(),
		cookies: m.cookiesInput.Entries(),
		tests:   m.testsInput.Entries(),
	}
}

func (r savedRequest) displayName() string {
	name := strings.TrimSpace(r.name)
	if name == "" {
		name = r.url
	}
	return strings.TrimPrefix(name, r.method+" ")
}

func (m *model) applySavedRequest(request savedRequest) {
	m.clearResponseFilter()
	if m.cancelRequest != nil {
		m.cancelRequest()
	}
	m.cancelRequest = nil
	m.requestContext = nil
	m.requestId = uuid.New()
	m.oauthLoginID = uuid.Nil
	if m.socketIO != nil {
		closeSocketIOSession(m.socketIO)
		m.socketIO = nil
	}
	if m.socketIOCancel != nil {
		m.socketIOCancel()
		m.socketIOCancel = nil
	}
	if m.mqtt != nil {
		terminateMQTTSession(m.mqtt)
		m.mqtt = nil
	}
	if m.mqttCancel != nil {
		m.mqttCancel()
		m.mqttCancel = nil
	}
	if m.webSocket != nil {
		closeWebSocketSession(m.webSocket)
		m.webSocket = nil
	}
	if m.webSocketCancel != nil {
		m.webSocketCancel()
		m.webSocketCancel = nil
	}
	m.urlInput.SetValue(request.url)
	m.headersInput.SetEntries(request.headers)
	m.paramsInput.SetEntries(request.params)
	m.authInput.SetConfig(request.auth)
	m.setBodyConfig(request.body)
	m.cookiesInput.SetEntries(request.cookies)
	m.testsInput.SetEntries(request.tests)
	m.response = ""
	m.responseRaw = ""
	m.responseRawAvailable = false
	m.responseStatusCode = 0
	m.responseHeaders = ""
	m.responseMeta = ""
	m.responseModel.SetContent("")
	m.responseHeadersModel.SetContent("")
	m.responseTests = ""
	m.responseTestsModel.SetContent("")
	m.assertionResults = nil
	m.setMethodForURL(request.method, request.url)
	m.markRequestDraftClean()
}

func toWorkspaceEntries(entries []headerEntry) []workspaceEntry {
	result := make([]workspaceEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, workspaceEntry{Key: entry.key, Value: entry.value})
	}
	return result
}

func fromWorkspaceEntries(entries []workspaceEntry) []headerEntry {
	result := make([]headerEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, headerEntry{key: entry.Key, value: entry.Value})
	}
	return result
}

func (r savedRequest) toWorkspace() workspaceSavedRequest {
	result := workspaceSavedRequest{
		Name: r.name, Method: r.method, URL: r.url,
		Headers: toWorkspaceEntries(r.headers), Query: toWorkspaceEntries(r.params), Cookies: toWorkspaceEntries(r.cookies), Tests: toWorkspaceEntries(r.tests),
		Auth: r.auth.toWorkspace(), Body: r.body.toWorkspace(),
	}
	for _, example := range r.examples {
		result.Examples = append(result.Examples, workspaceExample{Name: example.name, StatusCode: example.statusCode, ResponseBody: example.responseBody, ResponseRaw: example.responseRaw, ResponseRawAvailable: example.responseRawAvailable, ResponseHeaders: example.responseHeaders, ResponseMeta: example.responseMeta})
	}
	return result
}

func (r workspaceSavedRequest) fromWorkspace() savedRequest {
	result := savedRequest{
		name: r.Name, method: r.Method, url: r.URL,
		headers: fromWorkspaceEntries(r.Headers), params: fromWorkspaceEntries(r.Query), cookies: fromWorkspaceEntries(r.Cookies), tests: fromWorkspaceEntries(r.Tests),
		auth: r.Auth.fromWorkspace(), body: r.Body.fromWorkspace(),
	}
	for _, example := range r.Examples {
		result.examples = append(result.examples, savedExample{name: example.Name, statusCode: example.StatusCode, responseBody: example.ResponseBody, responseRaw: example.ResponseRaw, responseRawAvailable: example.ResponseRawAvailable, responseHeaders: example.ResponseHeaders, responseMeta: example.ResponseMeta})
	}
	return result
}

func (item historyItem) toWorkspace() workspaceHistoryItem {
	createdAt := ""
	if !item.createdAt.IsZero() {
		createdAt = item.createdAt.UTC().Format(time.RFC3339Nano)
	}
	request := savedRequest{
		method: item.method, url: item.url,
		headers: item.requestHeaders, params: item.requestParams, auth: item.requestAuth,
		body: item.requestBodyConfig, cookies: item.requestCookies, tests: item.requestTests,
	}
	return workspaceHistoryItem{
		ID: item.requestID.String(), CreatedAt: createdAt, Request: request.toWorkspace(),
		ResponseBody: item.responseBody, ResponseRaw: item.responseRaw, ResponseRawAvailable: item.responseRawAvailable,
		ResponseHeaders: item.responseHeaders, ResponseMeta: item.responseMeta, ResponseTests: item.responseTests,
	}
}

func (item workspaceHistoryItem) fromWorkspace() (historyItem, error) {
	requestID, err := uuid.Parse(item.ID)
	if err != nil {
		return historyItem{}, fmt.Errorf("invalid request ID %q", item.ID)
	}
	var createdAt time.Time
	if item.CreatedAt != "" {
		createdAt, err = time.Parse(time.RFC3339Nano, item.CreatedAt)
		if err != nil {
			return historyItem{}, fmt.Errorf("invalid creation time %q", item.CreatedAt)
		}
	}
	request := item.Request.fromWorkspace()
	return historyItem{
		createdAt: createdAt, method: request.method, url: request.url,
		requestBody: request.body.raw, requestHeaders: request.headers, requestParams: request.params,
		requestAuth: request.auth, requestBodyConfig: request.body, requestCookies: request.cookies, requestTests: request.tests,
		responseBody: item.ResponseBody, responseRaw: item.ResponseRaw, responseRawAvailable: item.ResponseRawAvailable,
		responseHeaders: item.ResponseHeaders, responseMeta: item.ResponseMeta, responseTests: item.ResponseTests,
		requestID: requestID,
	}, nil
}

func trimHistory(history []historyItem) []historyItem {
	trimmed := make([]historyItem, 0, min(len(history), maxHistoryEntries))
	totalBytes := 0
	for _, item := range history {
		if len(trimmed) >= maxHistoryEntries {
			break
		}
		encoded, err := json.Marshal(item.toWorkspace())
		if err != nil {
			continue
		}
		if len(trimmed) > 0 && totalBytes+len(encoded) > maxHistoryStorageBytes {
			break
		}
		trimmed = append(trimmed, item)
		totalBytes += len(encoded)
	}
	return trimmed
}

func (c authConfig) toWorkspace() workspaceAuth {
	types := map[authType]string{authNone: "none", authBearer: "bearer", authJWTBearer: "jwt_bearer", authBasic: "basic", authDigest: "digest", authAPIKey: "api_key", authAWSSignatureV4: "aws_sigv4", authOAuth2ClientCredentials: "oauth2_client_credentials", authOAuth2Password: "oauth2_password", authOAuth2RefreshToken: "oauth2_refresh_token", authOAuth2AuthorizationCode: "oauth2_authorization_code", authHawk: "hawk", authNTLM: "ntlm", authOAuth1: "oauth1"}
	location := "header"
	if c.apiKeyLocation == apiKeyQuery {
		location = "query"
	}
	jwtLocation := "header"
	if c.jwtLocation == apiKeyQuery {
		jwtLocation = "query"
	}
	oauth1Location := "header"
	if c.oauth1Location == apiKeyQuery {
		oauth1Location = "query"
	}
	return workspaceAuth{Type: types[c.typeID], BearerToken: c.bearerToken, JWTAlgorithm: c.jwtAlgorithm, JWTKey: c.jwtKey, JWTSecretBase64: c.jwtSecretBase64, JWTPayload: c.jwtPayload, JWTHeaders: c.jwtHeaders, JWTPrefix: c.jwtPrefix, JWTLocation: jwtLocation, JWTQueryName: c.jwtQueryName, Username: c.username, Password: c.password, APIKeyName: c.apiKeyName, APIKeyValue: c.apiKeyValue, APIKeyLocation: location, OAuthTokenURL: c.oauthTokenURL, OAuthAuthorizationURL: c.oauthAuthorizationURL, OAuthClientID: c.oauthClientID, OAuthClientSecret: c.oauthClientSecret, OAuthScope: c.oauthScope, OAuthRefreshToken: c.oauthRefreshToken, OAuthCallbackURL: c.oauthCallbackURL, OAuthAccessToken: c.oauthAccessToken, OAuthTokenType: c.oauthTokenType, OAuthAccessTokenExpiry: c.oauthAccessTokenExpiry, OAuthClientAuth: c.oauthClientAuth, OAuthPKCE: c.oauthPKCE, HawkID: c.hawkID, HawkKey: c.hawkKey, HawkAlgorithm: c.hawkAlgorithm, HawkExt: c.hawkExt, NTLMDomain: c.ntlmDomain, NTLMWorkstation: c.ntlmWorkstation, OAuth1ConsumerKey: c.oauth1ConsumerKey, OAuth1ConsumerSecret: c.oauth1ConsumerSecret, OAuth1Token: c.oauth1Token, OAuth1TokenSecret: c.oauth1TokenSecret, OAuth1PrivateKey: c.oauth1PrivateKey, OAuth1SignatureMethod: c.oauth1SignatureMethod, OAuth1Realm: c.oauth1Realm, OAuth1Callback: c.oauth1Callback, OAuth1Verifier: c.oauth1Verifier, OAuth1IncludeBodyHash: c.oauth1IncludeBodyHash, OAuth1Location: oauth1Location, AWSAccessKey: c.awsAccessKey, AWSSecretKey: c.awsSecretKey, AWSRegion: c.awsRegion, AWSService: c.awsService, AWSSessionToken: c.awsSessionToken}
}

func (c workspaceAuth) fromWorkspace() authConfig {
	types := map[string]authType{"none": authNone, "bearer": authBearer, "jwt_bearer": authJWTBearer, "basic": authBasic, "digest": authDigest, "api_key": authAPIKey, "aws_sigv4": authAWSSignatureV4, "oauth2_client_credentials": authOAuth2ClientCredentials, "oauth2_password": authOAuth2Password, "oauth2_refresh_token": authOAuth2RefreshToken, "oauth2_authorization_code": authOAuth2AuthorizationCode, "hawk": authHawk, "ntlm": authNTLM, "oauth1": authOAuth1}
	location := apiKeyHeader
	if c.APIKeyLocation == "query" {
		location = apiKeyQuery
	}
	jwtLocation := apiKeyHeader
	if c.JWTLocation == "query" {
		jwtLocation = apiKeyQuery
	}
	oauth1Location := apiKeyHeader
	if c.OAuth1Location == "query" {
		oauth1Location = apiKeyQuery
	}
	return authConfig{typeID: types[c.Type], bearerToken: c.BearerToken, jwtAlgorithm: c.JWTAlgorithm, jwtKey: c.JWTKey, jwtSecretBase64: c.JWTSecretBase64, jwtPayload: c.JWTPayload, jwtHeaders: c.JWTHeaders, jwtPrefix: c.JWTPrefix, jwtLocation: jwtLocation, jwtQueryName: c.JWTQueryName, username: c.Username, password: c.Password, apiKeyName: c.APIKeyName, apiKeyValue: c.APIKeyValue, apiKeyLocation: location, oauthTokenURL: c.OAuthTokenURL, oauthAuthorizationURL: c.OAuthAuthorizationURL, oauthClientID: c.OAuthClientID, oauthClientSecret: c.OAuthClientSecret, oauthScope: c.OAuthScope, oauthRefreshToken: c.OAuthRefreshToken, oauthCallbackURL: c.OAuthCallbackURL, oauthAccessToken: c.OAuthAccessToken, oauthTokenType: c.OAuthTokenType, oauthAccessTokenExpiry: c.OAuthAccessTokenExpiry, oauthClientAuth: c.OAuthClientAuth, oauthPKCE: c.OAuthPKCE, hawkID: c.HawkID, hawkKey: c.HawkKey, hawkAlgorithm: c.HawkAlgorithm, hawkExt: c.HawkExt, ntlmDomain: c.NTLMDomain, ntlmWorkstation: c.NTLMWorkstation, oauth1ConsumerKey: c.OAuth1ConsumerKey, oauth1ConsumerSecret: c.OAuth1ConsumerSecret, oauth1Token: c.OAuth1Token, oauth1TokenSecret: c.OAuth1TokenSecret, oauth1PrivateKey: c.OAuth1PrivateKey, oauth1SignatureMethod: c.OAuth1SignatureMethod, oauth1Realm: c.OAuth1Realm, oauth1Callback: c.OAuth1Callback, oauth1Verifier: c.OAuth1Verifier, oauth1IncludeBodyHash: c.OAuth1IncludeBodyHash, oauth1Location: oauth1Location, awsAccessKey: c.AWSAccessKey, awsSecretKey: c.AWSSecretKey, awsRegion: c.AWSRegion, awsService: c.AWSService, awsSessionToken: c.AWSSessionToken}
}

func (c bodyConfig) toWorkspace() workspaceBody {
	modes := map[bodyMode]string{bodyNone: "none", bodyRaw: "raw", bodyFormURLEncoded: "urlencoded", bodyMultipart: "multipart", bodyBinary: "binary", bodyGraphQL: "graphql"}
	rawTypes := map[rawBodyType]string{rawJSON: "json", rawText: "text", rawXML: "xml", rawHTML: "html"}
	return workspaceBody{Mode: modes[c.mode], RawType: rawTypes[c.rawType], Raw: c.raw, Form: toWorkspaceEntries(c.form), Multipart: toWorkspaceEntries(c.multipart), BinaryPath: c.binaryPath, GraphQLQuery: c.graphqlQuery, GraphQLVariables: c.graphqlVariables, GraphQLOperationName: c.graphqlOperationName}
}

func (c workspaceBody) fromWorkspace() bodyConfig {
	modes := map[string]bodyMode{"none": bodyNone, "raw": bodyRaw, "urlencoded": bodyFormURLEncoded, "multipart": bodyMultipart, "binary": bodyBinary, "graphql": bodyGraphQL}
	rawTypes := map[string]rawBodyType{"json": rawJSON, "text": rawText, "xml": rawXML, "html": rawHTML}
	return bodyConfig{mode: modes[c.Mode], rawType: rawTypes[c.RawType], raw: c.Raw, form: fromWorkspaceEntries(c.Form), multipart: fromWorkspaceEntries(c.Multipart), binaryPath: c.BinaryPath, graphqlQuery: c.GraphQLQuery, graphqlVariables: c.GraphQLVariables, graphqlOperationName: c.GraphQLOperationName}
}

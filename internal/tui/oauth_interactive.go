package tui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uuid "github.com/google/uuid"
)

type oauthLoginMsg struct {
	id    uuid.UUID
	token oauth2IssuedToken
	err   error
}

type oauth2IssuedToken struct {
	accessToken  string
	tokenType    string
	refreshToken string
	expiresAt    int64
}

type oauthAuthorizationResult struct {
	code  string
	state string
	err   error
}

func (m model) loginOAuth2AuthorizationCode(ctx context.Context, id uuid.UUID) tea.Cmd {
	return func() tea.Msg {
		resolver := newVariableResolver(m.variablesInput.Entries())
		config := m.authInput.Config().resolved(resolver)
		if config.typeID != authOAuth2AuthorizationCode {
			return oauthLoginMsg{id: id, err: fmt.Errorf("select OAuth 2 Authorization Code first")}
		}
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
			return oauthLoginMsg{id: id, err: fmt.Errorf("configure OAuth 2 client: %w", err)}
		}
		token, err := acquireOAuthAuthorizationCode(ctx, client, config, openSystemBrowser)
		return oauthLoginMsg{id: id, token: token, err: err}
	}
}

func acquireOAuthAuthorizationCode(ctx context.Context, client *http.Client, config authConfig, opener func(string) error) (oauth2IssuedToken, error) {
	if strings.TrimSpace(config.oauthAuthorizationURL) == "" {
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 authorization URL is required")
	}
	if strings.TrimSpace(config.oauthTokenURL) == "" {
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 token URL is required")
	}
	if strings.TrimSpace(config.oauthClientID) == "" {
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 client ID is required")
	}
	callback, listener, err := oauthCallbackListener(config.oauthCallbackURL)
	if err != nil {
		return oauth2IssuedToken{}, err
	}
	defer listener.Close() //nolint:errcheck

	state, err := oauthRandomValue(32)
	if err != nil {
		return oauth2IssuedToken{}, fmt.Errorf("generate OAuth 2 state: %w", err)
	}
	verifier, err := oauthRandomValue(32)
	if err != nil {
		return oauth2IssuedToken{}, fmt.Errorf("generate OAuth 2 PKCE verifier: %w", err)
	}
	authorizationURL, err := url.Parse(config.oauthAuthorizationURL)
	if err != nil || authorizationURL.Host == "" || (authorizationURL.Scheme != "http" && authorizationURL.Scheme != "https") {
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 authorization URL must be an absolute http:// or https:// URL")
	}
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", config.oauthClientID)
	query.Set("redirect_uri", callback.String())
	query.Set("state", state)
	if scope := strings.TrimSpace(config.oauthScope); scope != "" {
		query.Set("scope", scope)
	}
	if config.oauthPKCE {
		challenge := sha256.Sum256([]byte(verifier))
		query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
		query.Set("code_challenge_method", "S256")
	}
	authorizationURL.RawQuery = query.Encode()

	result := make(chan oauthAuthorizationResult, 1)
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	server.Handler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != callback.Path {
			http.NotFound(response, request)
			return
		}
		values := request.URL.Query()
		callbackResult := oauthAuthorizationResult{code: values.Get("code"), state: values.Get("state")}
		if oauthError := strings.TrimSpace(values.Get("error")); oauthError != "" {
			callbackResult.err = fmt.Errorf("authorization server returned %s: %s", oauthError, strings.TrimSpace(values.Get("error_description")))
		} else if callbackResult.state != state {
			callbackResult.err = fmt.Errorf("OAuth 2 callback state did not match")
		} else if callbackResult.code == "" {
			callbackResult.err = fmt.Errorf("OAuth 2 callback did not include an authorization code")
		}
		select {
		case result <- callbackResult:
		default:
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(response, "<!doctype html><title>Courier OAuth</title><p>Authorization received. You can return to Courier.</p>")
	})
	serveDone := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(serveDone)
	}()
	if err := opener(authorizationURL.String()); err != nil {
		_ = server.Close()
		<-serveDone
		return oauth2IssuedToken{}, fmt.Errorf("open OAuth 2 authorization URL: %w", err)
	}

	var callbackResult oauthAuthorizationResult
	select {
	case callbackResult = <-result:
	case <-ctx.Done():
		_ = server.Close()
		<-serveDone
		return oauth2IssuedToken{}, ctx.Err()
	}
	_ = server.Close()
	<-serveDone
	if callbackResult.err != nil {
		return oauth2IssuedToken{}, callbackResult.err
	}
	values := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {callbackResult.code},
		"redirect_uri": {callback.String()},
	}
	if config.oauthPKCE {
		values.Set("code_verifier", verifier)
	}
	return requestOAuth2Token(ctx, client, config, values)
}

func oauthCallbackListener(rawURL string) (*url.URL, net.Listener, error) {
	callback, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || callback.Scheme != "http" || callback.Hostname() == "" {
		return nil, nil, fmt.Errorf("OAuth 2 callback must be an absolute loopback http:// URL")
	}
	hostname := callback.Hostname()
	ip := net.ParseIP(hostname)
	if !strings.EqualFold(hostname, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil, nil, fmt.Errorf("OAuth 2 callback host must be localhost or a loopback IP address")
	}
	if callback.RawQuery != "" || callback.Fragment != "" {
		return nil, nil, fmt.Errorf("OAuth 2 callback URL must not contain a query or fragment")
	}
	port := callback.Port()
	if port == "" {
		port = "80"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(hostname, port))
	if err != nil {
		return nil, nil, fmt.Errorf("listen for OAuth 2 callback: %w", err)
	}
	actualPort := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	callback.Host = net.JoinHostPort(hostname, actualPort)
	if callback.Path == "" {
		callback.Path = "/"
	}
	return callback, listener, nil
}

func requestOAuth2Token(ctx context.Context, client *http.Client, config authConfig, values url.Values) (oauth2IssuedToken, error) {
	tokenURL, err := url.Parse(strings.TrimSpace(config.oauthTokenURL))
	if err != nil || tokenURL.Host == "" || (tokenURL.Scheme != "http" && tokenURL.Scheme != "https") {
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 token URL must be an absolute http:// or https:// URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return oauth2IssuedToken{}, fmt.Errorf("create OAuth 2 token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	switch config.oauthClientAuth {
	case "", "basic":
		request.SetBasicAuth(config.oauthClientID, config.oauthClientSecret)
	case "body":
		values.Set("client_id", config.oauthClientID)
		values.Set("client_secret", config.oauthClientSecret)
		request.Body = io.NopCloser(strings.NewReader(values.Encode()))
		request.ContentLength = int64(len(values.Encode()))
	case "none":
		values.Set("client_id", config.oauthClientID)
		request.Body = io.NopCloser(strings.NewReader(values.Encode()))
		request.ContentLength = int64(len(values.Encode()))
	default:
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 client authentication must be basic, body, or none")
	}
	response, err := client.Do(request)
	if err != nil {
		return oauth2IssuedToken{}, fmt.Errorf("request OAuth 2 token: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthTokenResponse+1))
	if err != nil {
		return oauth2IssuedToken{}, fmt.Errorf("read OAuth 2 token response: %w", err)
	}
	if len(body) > maxOAuthTokenResponse {
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 token response exceeds 1 MiB")
	}
	var payload struct {
		AccessToken      string          `json:"access_token"`
		TokenType        string          `json:"token_type"`
		RefreshToken     string          `json:"refresh_token"`
		ExpiresIn        json.RawMessage `json:"expires_in"`
		Error            string          `json:"error"`
		ErrorDescription string          `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return oauth2IssuedToken{}, fmt.Errorf("decode OAuth 2 token response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := strings.Trim(strings.TrimSpace(payload.Error)+": "+strings.TrimSpace(payload.ErrorDescription), ": ")
		if detail == "" {
			detail = response.Status
		}
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 token request failed: %s", detail)
	}
	if strings.TrimSpace(payload.AccessToken) == "" || strings.ContainsAny(payload.AccessToken, "\r\n") {
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 token response did not include a valid access_token")
	}
	tokenType := strings.TrimSpace(payload.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	if strings.ContainsAny(tokenType, "\r\n") {
		return oauth2IssuedToken{}, fmt.Errorf("OAuth 2 token response contained an invalid token_type")
	}
	expiresIn := parseOAuthExpiresIn(payload.ExpiresIn)
	issued := oauth2IssuedToken{accessToken: payload.AccessToken, tokenType: tokenType, refreshToken: payload.RefreshToken}
	if expiresIn > 0 {
		issued.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).Unix()
	}
	return issued, nil
}

func parseOAuthExpiresIn(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		value, _ := number.Int64()
		return value
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, _ := strconv.ParseInt(text, 10, 64)
		return value
	}
	return 0
}

func oauthRandomValue(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func openSystemBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target) //nolint:gosec // Fixed executable; URL is passed as one argument without a shell.
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target) //nolint:gosec // Fixed executable and argument structure.
	default:
		command = exec.Command("xdg-open", target) //nolint:gosec // Fixed executable; URL is passed as one argument without a shell.
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDoRequestOAuth2ClientCredentials(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			username, password, ok := r.BasicAuth()
			if !ok || username != "client-123" || password != "secret-456" {
				http.Error(w, "bad client credentials", http.StatusUnauthorized)
				return
			}
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "read write" {
				http.Error(w, "bad token form", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"issued-token","token_type":"Bearer"}`))
		case "/resource":
			if r.Header.Get("Authorization") != "Bearer issued-token" {
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("authorized"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := NewModel()
	m.bodyMode = bodyNone
	m.urlInput.SetValue("{{origin}}/resource")
	m.variablesInput.SetEntries([]headerEntry{{key: "origin", value: server.URL}, {key: "clientId", value: "client-123"}, {key: "secret", value: "secret-456"}})
	m.authInput.SetConfig(authConfig{
		typeID: authOAuth2ClientCredentials, oauthTokenURL: "{{origin}}/oauth/token",
		oauthClientID: "{{clientId}}", oauthClientSecret: "{{secret}}", oauthScope: "read write",
	})
	response := m.DoRequest()().(responseMsg)
	if response.statusCode != http.StatusOK || !strings.Contains(stripANSI(response.responseBody), "authorized") {
		t.Fatalf("OAuth resource response = %#v", response)
	}
}

func TestDoRequestOAuth2PasswordCredentials(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			clientID, secret, ok := r.BasicAuth()
			if !ok || clientID != "terminal-client" || secret != "client-secret" {
				http.Error(w, "bad client", http.StatusUnauthorized)
				return
			}
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "password" || r.Form.Get("username") != "alice" || r.Form.Get("password") != "wonderland" || r.Form.Get("scope") != "read" {
				http.Error(w, "bad token form", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"password-token","token_type":"Bearer"}`))
		case "/resource":
			if r.Header.Get("Authorization") != "Bearer password-token" {
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("authorized"))
		}
	}))
	defer server.Close()

	m := NewModel()
	m.bodyMode = bodyNone
	m.urlInput.SetValue(server.URL + "/resource")
	m.authInput.SetConfig(authConfig{
		typeID: authOAuth2Password, oauthTokenURL: server.URL + "/token", oauthClientID: "terminal-client", oauthClientSecret: "client-secret",
		oauthScope: "read", username: "alice", password: "wonderland",
	})
	response := m.DoRequest()().(responseMsg)
	if response.statusCode != http.StatusOK || !strings.Contains(stripANSI(response.responseBody), "authorized") {
		t.Fatalf("OAuth password resource response = %#v", response)
	}
}

func TestOAuth2PasswordWorkspaceAndPostmanRoundTrip(t *testing.T) {
	config := authConfig{typeID: authOAuth2Password, oauthTokenURL: "https://id.example/token", oauthClientID: "client", oauthClientSecret: "secret", oauthScope: "read", username: "alice", password: "pass"}
	restored := config.toWorkspace().fromWorkspace()
	if restored != config {
		t.Fatalf("workspace auth = %#v", restored)
	}
	exported := exportPostmanAuth(config)
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var imported postmanAuth
	if err := json.Unmarshal(encoded, &imported); err != nil {
		t.Fatal(err)
	}
	if got := postmanAuthConfig(&imported); got != config {
		t.Fatalf("Postman auth = %#v", got)
	}
}

func TestOAuth2RefreshToken(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-123" || r.Form.Get("scope") != "read write" {
			http.Error(w, "bad refresh form", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"renewed","token_type":"DPoP"}`))
	}))
	defer server.Close()
	config := authConfig{typeID: authOAuth2RefreshToken, oauthTokenURL: server.URL, oauthClientID: "client", oauthClientSecret: "secret", oauthScope: "read write", oauthRefreshToken: "refresh-123"}
	token, tokenType, err := config.fetchOAuth2Token(context.Background(), http.DefaultClient)
	if err != nil || token != "renewed" || tokenType != "DPoP" {
		t.Fatalf("refresh token = %q %q %v", token, tokenType, err)
	}
	restored := config.toWorkspace().fromWorkspace()
	if restored != config {
		t.Fatalf("workspace auth = %#v", restored)
	}
	exported := exportPostmanAuth(config)
	encoded, _ := json.Marshal(exported)
	var imported postmanAuth
	if err := json.Unmarshal(encoded, &imported); err != nil {
		t.Fatal(err)
	}
	if got := postmanAuthConfig(&imported); got != config {
		t.Fatalf("Postman auth = %#v", got)
	}
}

func TestOAuth2TokenErrors(t *testing.T) {
	tests := []struct {
		name    string
		config  authConfig
		handler http.HandlerFunc
		want    string
	}{
		{name: "missing URL", config: authConfig{oauthClientID: "id"}, want: "token URL is required"},
		{name: "missing client ID", config: authConfig{oauthTokenURL: "server"}, want: "client ID is required"},
		{name: "oauth error", config: authConfig{oauthClientID: "id"}, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"credentials rejected"}`))
		}, want: "invalid_client"},
		{name: "missing token", config: authConfig{oauthClientID: "id"}, handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"token_type":"Bearer"}`))
		}, want: "did not include access_token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := test.config
			var server *httptest.Server
			if test.handler != nil {
				server = httptest.NewServer(test.handler)
				defer server.Close()
				config.oauthTokenURL = server.URL
			}
			_, _, err := config.fetchOAuth2ClientCredentialsToken(context.Background(), http.DefaultClient)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("token error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestOAuth2AuthPaneFields(t *testing.T) {
	pane := newAuthPane()
	pane.SetWidth(70)
	pane.SetConfig(authConfig{typeID: authOAuth2ClientCredentials, oauthTokenURL: "https://id.example/token", oauthClientID: "client", oauthClientSecret: "secret", oauthScope: "read"})
	pane.Focus()
	view := stripANSI(pane.View())
	for _, label := range []string{"OAuth 2 Client Credentials", "Token URL", "Client ID", "Secret", "Scope"} {
		if !strings.Contains(view, label) {
			t.Fatalf("OAuth pane does not contain %q:\n%s", label, view)
		}
	}
	for range 3 {
		pane.UpdateNormal("j")
	}
	if pane.cursor != 3 || pane.currentInput() != &pane.oauthScope {
		t.Fatalf("OAuth field navigation ended at %d", pane.cursor)
	}
	if got := pane.Config().oauthClientSecret; got != "secret" {
		t.Fatalf("OAuth secret = %q", got)
	}
}

func TestOAuth2AuthorizationCodePKCE(t *testing.T) {
	t.Parallel()
	challenge := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			if r.URL.Query().Get("response_type") != "code" || r.URL.Query().Get("client_id") != "terminal-client" || r.URL.Query().Get("code_challenge_method") != "S256" {
				http.Error(w, "bad authorization request", http.StatusBadRequest)
				return
			}
			challenge <- r.URL.Query().Get("code_challenge")
			callback, err := url.Parse(r.URL.Query().Get("redirect_uri"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			query := callback.Query()
			query.Set("code", "issued-code")
			query.Set("state", r.URL.Query().Get("state"))
			callback.RawQuery = query.Encode()
			http.Redirect(w, r, callback.String(), http.StatusFound)
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "issued-code" || r.Form.Get("client_id") != "terminal-client" || r.Form.Get("client_secret") != "secret" {
				http.Error(w, "bad token request", http.StatusBadRequest)
				return
			}
			proof := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(proof[:]) != <-challenge {
				http.Error(w, "bad PKCE verifier", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"browser-token","token_type":"Bearer","refresh_token":"refresh-token","expires_in":"3600"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	config := authConfig{
		typeID: authOAuth2AuthorizationCode, oauthAuthorizationURL: server.URL + "/authorize", oauthTokenURL: server.URL + "/token",
		oauthClientID: "terminal-client", oauthClientSecret: "secret", oauthScope: "read write", oauthCallbackURL: "http://127.0.0.1:0/callback",
		oauthClientAuth: "body", oauthPKCE: true,
	}
	opener := func(target string) error {
		response, err := http.Get(target) //nolint:gosec,noctx // Test drives the complete local redirect flow.
		if response != nil {
			_ = response.Body.Close()
		}
		return err
	}
	token, err := acquireOAuthAuthorizationCode(context.Background(), http.DefaultClient, config, opener)
	if err != nil {
		t.Fatal(err)
	}
	if token.accessToken != "browser-token" || token.tokenType != "Bearer" || token.refreshToken != "refresh-token" || token.expiresAt <= time.Now().Unix() {
		t.Fatalf("issued token = %#v", token)
	}
	config.oauthAccessToken, config.oauthTokenType, config.oauthAccessTokenExpiry = token.accessToken, token.tokenType, token.expiresAt
	request := httptest.NewRequest(http.MethodGet, "https://api.example.test", nil)
	if err := config.authorize(context.Background(), http.DefaultClient, request, nil); err != nil || request.Header.Get("Authorization") != "Bearer browser-token" {
		t.Fatalf("cached authorization = %q, %v", request.Header.Get("Authorization"), err)
	}
}

func TestOAuth2AuthorizationCodeValidationAndRoundTrip(t *testing.T) {
	if _, listener, err := oauthCallbackListener("https://127.0.0.1/callback"); err == nil {
		_ = listener.Close()
		t.Fatal("expected HTTPS callback rejection")
	}
	if _, listener, err := oauthCallbackListener("http://example.test/callback"); err == nil {
		_ = listener.Close()
		t.Fatal("expected non-loopback callback rejection")
	}
	config := authConfig{
		typeID: authOAuth2AuthorizationCode, oauthAuthorizationURL: "https://id.example/authorize", oauthTokenURL: "https://id.example/token",
		oauthClientID: "client", oauthClientSecret: "secret", oauthScope: "read", oauthCallbackURL: "http://127.0.0.1:8085/callback",
		oauthAccessToken: "token", oauthTokenType: "Bearer", oauthRefreshToken: "refresh", oauthAccessTokenExpiry: 123456,
		oauthClientAuth: "none", oauthPKCE: true,
	}
	if restored := config.toWorkspace().fromWorkspace(); restored != config {
		t.Fatalf("workspace auth = %#v", restored)
	}
	exported := exportPostmanAuth(config)
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var imported postmanAuth
	if err := json.Unmarshal(encoded, &imported); err != nil {
		t.Fatal(err)
	}
	got := postmanAuthConfig(&imported)
	got.oauthAccessTokenExpiry = config.oauthAccessTokenExpiry // Postman does not persist Courier's absolute cache expiry.
	if got != config {
		t.Fatalf("Postman auth = %#v", got)
	}
}

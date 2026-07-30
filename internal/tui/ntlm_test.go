package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDoRequestNTLMNegotiationDoesNotLeakBasicCredentials(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		authorization := r.Header.Get("Authorization")
		if strings.HasPrefix(authorization, "Basic ") {
			t.Errorf("Basic credentials leaked on call %d", call)
		}
		if call == 1 {
			if authorization != "" {
				t.Errorf("initial authorization = %q", authorization)
			}
			w.Header().Set("WWW-Authenticate", "NTLM")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(authorization, "NTLM TlRMTVNTUAAB") {
			http.Error(w, "missing NTLM negotiate", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("authorized"))
	}))
	defer server.Close()

	m := NewModel()
	m.bodyMode = bodyNone
	m.urlInput.SetValue(server.URL)
	m.authInput.SetConfig(authConfig{typeID: authNTLM, username: "alice", password: "secret", ntlmDomain: "EXAMPLE", ntlmWorkstation: "COURIER"})
	response := m.DoRequest()().(responseMsg)
	if response.statusCode != http.StatusOK || !strings.Contains(stripANSI(response.responseBody), "authorized") || calls.Load() != 2 {
		t.Fatalf("NTLM response = %#v calls=%d", response, calls.Load())
	}
}

func TestNTLMWorkspacePostmanAndCurlRoundTrip(t *testing.T) {
	config := authConfig{typeID: authNTLM, username: "alice", password: "secret", ntlmDomain: "EXAMPLE", ntlmWorkstation: "COURIER"}
	if got := config.toWorkspace().fromWorkspace(); got != config {
		t.Fatalf("workspace NTLM = %#v", got)
	}
	exported := exportPostmanAuth(config)
	encoded, _ := json.Marshal(exported)
	var imported postmanAuth
	if err := json.Unmarshal(encoded, &imported); err != nil {
		t.Fatal(err)
	}
	if got := postmanAuthConfig(&imported); got != config {
		t.Fatalf("Postman NTLM = %#v", got)
	}
	command := CurlCommand(savedRequest{method: "GET", url: "https://example.com", auth: config, body: bodyConfig{mode: bodyNone}})
	parsed, err := ParseCurlCommand(command)
	if err != nil || parsed.auth.typeID != authNTLM || parsed.auth.ntlmDomain != "EXAMPLE" || parsed.auth.username != "alice" || parsed.auth.password != "secret" {
		t.Fatalf("cURL NTLM = %q %#v %v", command, parsed.auth, err)
	}
}

func TestNTLMRequiresUsername(t *testing.T) {
	if _, err := configureNTLMClient(http.DefaultClient, authConfig{typeID: authNTLM}); err == nil || !strings.Contains(err.Error(), "username") {
		t.Fatalf("missing username error = %v", err)
	}
}

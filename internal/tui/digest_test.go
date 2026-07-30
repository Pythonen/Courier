package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDoRequest_DigestAuthenticationRetriesWithBody(t *testing.T) {
	const (
		username = "courier-user"
		password = "courier-password"
		realm    = "Courier API"
		nonce    = "fixed-server-nonce"
		payload  = `{"message":"digest body"}`
	)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != payload {
			http.Error(response, "request body was not replayed", http.StatusBadRequest)
			return
		}
		authorization := request.Header.Get("Authorization")
		if authorization == "" {
			response.Header().Set("WWW-Authenticate", `Basic realm="fallback", Digest realm="`+realm+`", nonce="`+nonce+`", algorithm=SHA-256, qop="auth,auth-int", opaque="opaque-value"`)
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(authorization, "Digest ") {
			http.Error(response, "wrong authorization scheme", http.StatusUnauthorized)
			return
		}
		parameters, err := parseDigestParameters(strings.TrimPrefix(authorization, "Digest "))
		if err != nil {
			http.Error(response, err.Error(), http.StatusUnauthorized)
			return
		}
		hash, _, _ := digestHash("SHA-256")
		ha1 := hash(username + ":" + realm + ":" + password)
		ha2 := hash(request.Method + ":" + request.URL.RequestURI())
		expected := hash(ha1 + ":" + nonce + ":" + parameters["nc"] + ":" + parameters["cnonce"] + ":auth:" + ha2)
		if parameters["username"] != username || parameters["realm"] != realm || parameters["uri"] != "/resource?expand=true" || parameters["opaque"] != "opaque-value" || parameters["qop"] != "auth" || parameters["response"] != expected {
			http.Error(response, "invalid digest response", http.StatusUnauthorized)
			return
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("digest accepted"))
	}))
	defer server.Close()

	m := NewModel()
	m.methodIdx = methodIndex(t, http.MethodPost)
	m.urlInput.SetValue(server.URL + "/resource")
	m.paramsInput.SetEntries([]headerEntry{{key: "expand", value: "true"}})
	m.bodyMode = bodyRaw
	m.rawBodyType = rawJSON
	m.bodyInput.SetValue(payload)
	m.variablesInput.SetEntries([]headerEntry{{key: "digestUser", value: username}, {key: "digestPassword", value: password}})
	m.authInput.SetConfig(authConfig{typeID: authDigest, username: "{{digestUser}}", password: "{{digestPassword}}"})

	result := m.DoRequest()().(responseMsg)
	if result.statusCode != http.StatusOK || !strings.Contains(stripANSI(result.responseBody), "digest accepted") || calls.Load() != 2 {
		t.Fatalf("Digest result = status %d body %q metadata %q calls %d", result.statusCode, result.responseBody, result.responseMeta, calls.Load())
	}
}

func TestDigestAlgorithmsAndChallengeErrors(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://example.test/resource?view=full", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, algorithm := range []string{"MD5", "MD5-SESS", "SHA-256", "SHA-256-SESS", "SHA-512-256", "SHA-512-256-SESS"} {
		hash, session, hashErr := digestHash(algorithm)
		if hashErr != nil || hash("courier") == "" {
			t.Fatalf("algorithm %s did not produce a hash: err=%v", algorithm, hashErr)
		}
		authorization, authErr := digestAuthorization(request, authConfig{username: "user", password: "password"}, []string{`Digest realm="test", nonce="nonce", algorithm=` + algorithm + `, qop="auth"`})
		if authErr != nil {
			t.Fatalf("algorithm %s authorization: %v", algorithm, authErr)
		}
		parameters, parseErr := parseDigestParameters(strings.TrimPrefix(authorization, "Digest "))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ha1 := hash("user:test:password")
		if session {
			ha1 = hash(ha1 + ":nonce:" + parameters["cnonce"])
		}
		ha2 := hash("GET:/resource?view=full")
		expected := hash(ha1 + ":nonce:00000001:" + parameters["cnonce"] + ":auth:" + ha2)
		if parameters["response"] != expected || parameters["algorithm"] != algorithm {
			t.Fatalf("algorithm %s response parameters = %#v", algorithm, parameters)
		}
	}
	_, err = digestAuthorization(request, authConfig{username: "user", password: "password"}, []string{`Digest realm="test", nonce="value", algorithm=SHA-1, qop="auth"`})
	if err == nil || !strings.Contains(err.Error(), "unsupported Digest algorithm") {
		t.Fatalf("unsupported algorithm error = %v", err)
	}
	_, err = digestAuthorization(request, authConfig{username: "user", password: "password"}, []string{`Digest realm="test", nonce="value", algorithm=SHA-256, qop="auth-int"`})
	if err == nil || !strings.Contains(err.Error(), "qop auth") {
		t.Fatalf("unsupported qop error = %v", err)
	}
}

func TestParseDigestChallengeHandlesQuotedEscapes(t *testing.T) {
	parameters, err := parseDigestChallenge([]string{`Digest realm="quoted \"realm\"", nonce="abc\\def", algorithm=MD5`})
	if err != nil {
		t.Fatal(err)
	}
	if parameters["realm"] != `quoted "realm"` || parameters["nonce"] != `abc\def` || parameters["algorithm"] != "MD5" {
		t.Fatalf("challenge parameters = %#v", parameters)
	}
}

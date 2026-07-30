package tui

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // Verifies the OAuth body-hash extension.
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeOAuth1RFC5849Vector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "http://example.com/request?b5=%3D%253D&a3=a&c%40=&a2=r%20b", strings.NewReader("c2&a3=2+q"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	config := authConfig{
		typeID: authOAuth1, oauth1ConsumerKey: "9djdj82h48djs9d2", oauth1ConsumerSecret: "j49sk3j29djd",
		oauth1Token: "kkk9d7dh3k39sjv7", oauth1TokenSecret: "dh893hdasih9", oauth1SignatureMethod: "HMAC-SHA1",
	}
	if err := authorizeOAuth1(request, []byte("c2&a3=2+q"), config, time.Unix(137131201, 0), "7d8f3e4a"); err != nil {
		t.Fatal(err)
	}
	header := request.Header.Get("Authorization")
	if !strings.Contains(header, `oauth_signature="r6%2FTJjbCOr97%2F%2BUU0NsvSne7s5g%3D"`) {
		t.Fatalf("RFC 5849 signature missing from %q", header)
	}
	if strings.Contains(header, "a3=") || strings.Contains(header, "b5=") {
		t.Fatalf("non-OAuth request parameters leaked into Authorization header: %q", header)
	}
}

func TestAuthorizeOAuth1QueryPlacement(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://example.test/resource?existing=value", nil)
	if err != nil {
		t.Fatal(err)
	}
	config := authConfig{
		typeID: authOAuth1, oauth1ConsumerKey: "consumer", oauth1ConsumerSecret: "secret",
		oauth1Token: "token", oauth1TokenSecret: "token-secret", oauth1SignatureMethod: "HMAC-SHA256", oauth1Location: apiKeyQuery,
	}
	if err := authorizeOAuth1(request, nil, config, time.Unix(1234, 0), "fixed-nonce"); err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("Authorization") != "" {
		t.Fatalf("query placement unexpectedly set Authorization: %q", request.Header.Get("Authorization"))
	}
	query := request.URL.Query()
	for key, expected := range map[string]string{
		"existing": "value", "oauth_consumer_key": "consumer", "oauth_token": "token", "oauth_nonce": "fixed-nonce",
		"oauth_timestamp": "1234", "oauth_signature_method": "HMAC-SHA256",
	} {
		if query.Get(key) != expected {
			t.Errorf("%s = %q, want %q", key, query.Get(key), expected)
		}
	}
	if _, err := base64.StdEncoding.DecodeString(query.Get("oauth_signature")); err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}
}

func TestAuthorizeOAuth1PlacesFormParametersInBody(t *testing.T) {
	payload := []byte("name=Ada")
	request, _ := http.NewRequest(http.MethodPost, "https://example.test/resource?existing=value", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	config := authConfig{
		typeID: authOAuth1, oauth1ConsumerKey: "consumer", oauth1ConsumerSecret: "secret", oauth1SignatureMethod: "HMAC-SHA256",
		oauth1Callback: "https://client.example/callback", oauth1Verifier: "verified", oauth1Location: apiKeyQuery,
	}
	if err := authorizeOAuth1(request, payload, config, time.Unix(1234, 0), "fixed"); err != nil {
		t.Fatal(err)
	}
	if request.URL.Query().Get("oauth_signature") != "" || request.Header.Get("Authorization") != "" {
		t.Fatalf("body placement leaked OAuth parameters: URL %q header %q", request.URL.String(), request.Header.Get("Authorization"))
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]string{
		"name": "Ada", "oauth_consumer_key": "consumer", "oauth_nonce": "fixed", "oauth_callback": "https://client.example/callback", "oauth_verifier": "verified",
	} {
		if values.Get(key) != expected {
			t.Errorf("form %s = %q, want %q", key, values.Get(key), expected)
		}
	}
	if values.Get("oauth_signature") == "" || request.ContentLength != int64(len(body)) || request.GetBody == nil {
		t.Fatalf("signed form replay metadata = signature %q length %d getBody %v", values.Get("oauth_signature"), request.ContentLength, request.GetBody != nil)
	}
}

func TestAuthorizeOAuth1IncludesOptionalBodyHash(t *testing.T) {
	payload := []byte(`{"name":"Ada"}`)
	request, _ := http.NewRequest(http.MethodPost, "https://example.test/resource", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	config := authConfig{typeID: authOAuth1, oauth1ConsumerKey: "consumer", oauth1ConsumerSecret: "secret", oauth1SignatureMethod: "HMAC-SHA1", oauth1IncludeBodyHash: true}
	if err := authorizeOAuth1(request, payload, config, time.Unix(1234, 0), "fixed"); err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum(payload) //nolint:gosec // OAuth body-hash compatibility.
	expected := oauth1Encode(base64.StdEncoding.EncodeToString(digest[:]))
	if !strings.Contains(request.Header.Get("Authorization"), `oauth_body_hash="`+expected+`"`) {
		t.Fatalf("OAuth body hash missing from %q", request.Header.Get("Authorization"))
	}
}

func TestAuthorizeOAuth1PlaintextAndRSA(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	plaintext := authConfig{typeID: authOAuth1, oauth1ConsumerKey: "key", oauth1ConsumerSecret: "s ecret", oauth1TokenSecret: "t/secret", oauth1SignatureMethod: "PLAINTEXT"}
	if err := authorizeOAuth1(request, nil, plaintext, time.Unix(1, 0), "nonce"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request.Header.Get("Authorization"), `oauth_signature="s%2520ecret%26t%252Fsecret"`) {
		t.Fatalf("unexpected PLAINTEXT signature: %q", request.Header.Get("Authorization"))
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustPKCS8(t, privateKey)})
	rsaRequest, _ := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	rsaConfig := authConfig{typeID: authOAuth1, oauth1ConsumerKey: "key", oauth1SignatureMethod: "RSA-SHA1", oauth1PrivateKey: string(keyPEM)}
	if err := authorizeOAuth1(rsaRequest, nil, rsaConfig, time.Unix(1, 0), "nonce"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rsaRequest.Header.Get("Authorization"), `oauth_signature_method="RSA-SHA1"`) {
		t.Fatalf("RSA-SHA1 header missing: %q", rsaRequest.Header.Get("Authorization"))
	}
}

func TestOAuth1UsesRequestHostAndReplacesManagedQueryFields(t *testing.T) {
	config := authConfig{typeID: authOAuth1, oauth1ConsumerKey: "consumer", oauth1ConsumerSecret: "secret", oauth1SignatureMethod: "HMAC-SHA256", oauth1Location: apiKeyQuery}
	first, _ := http.NewRequest(http.MethodGet, "http://ignored.test/resource?oauth_nonce=stale&oauth_signature=stale", nil)
	first.Host = "api.example.test:80"
	second, _ := http.NewRequest(http.MethodGet, "http://api.example.test/resource", nil)
	for _, request := range []*http.Request{first, second} {
		if err := authorizeOAuth1(request, nil, config, time.Unix(1234, 0), "fresh"); err != nil {
			t.Fatal(err)
		}
	}
	if first.URL.Query().Get("oauth_nonce") != "fresh" {
		t.Fatalf("managed OAuth query fields were not replaced: %q", first.URL.RawQuery)
	}
	if first.URL.Query().Get("oauth_signature") != second.URL.Query().Get("oauth_signature") {
		t.Fatalf("Host override/default port changed signature: %q != %q", first.URL.Query().Get("oauth_signature"), second.URL.Query().Get("oauth_signature"))
	}
}

func mustPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	data, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOAuth1PostmanRoundTrip(t *testing.T) {
	want := authConfig{
		typeID: authOAuth1, oauth1ConsumerKey: "consumer", oauth1ConsumerSecret: "secret", oauth1Token: "token",
		oauth1TokenSecret: "token-secret", oauth1SignatureMethod: "HMAC-SHA256", oauth1Realm: "example", oauth1Callback: "https://client.example/callback",
		oauth1Verifier: "verifier", oauth1IncludeBodyHash: true, oauth1Location: apiKeyQuery,
	}
	exported := exportPostmanAuth(want)
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var imported postmanAuth
	if err := json.Unmarshal(encoded, &imported); err != nil {
		t.Fatal(err)
	}
	got := postmanAuthConfig(&imported)
	if got != want {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestOAuth1GeneratedCommandsUseRuntimePlaceholders(t *testing.T) {
	request := savedRequest{method: http.MethodGet, url: "https://example.test/", auth: authConfig{typeID: authOAuth1, oauth1ConsumerKey: "consumer"}}
	if command := CurlCommand(request); !strings.Contains(command, "{{oauth1_authorization}}") {
		t.Fatalf("cURL command = %q", command)
	}
	command, err := HTTPieCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "{{oauth1_authorization}}") {
		t.Fatalf("HTTPie command = %q", command)
	}

	request.auth.oauth1Location = apiKeyQuery
	queryCommand := requestCommandURL(request)
	if !strings.Contains(queryCommand, "{{oauth1_parameters}}") {
		t.Fatalf("query placeholder URL = %q", queryCommand)
	}
	request.method = http.MethodPost
	request.body = bodyConfig{mode: bodyFormURLEncoded, form: []headerEntry{{key: "name", value: "Ada"}}}
	if command := CurlCommand(request); strings.Contains(command, "oauth1_parameters") || !strings.Contains(command, "oauth1_form_parameters") {
		t.Fatalf("form-placement cURL command = %q", command)
	}
}

package tui

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jwtSigningInputAndSignature(t *testing.T, token string) ([]byte, []byte) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts", len(parts))
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	return []byte(parts[0] + "." + parts[1]), signature
}

func TestGenerateJWTHMACAlgorithms(t *testing.T) {
	for _, algorithm := range []string{"HS256", "HS384", "HS512"} {
		t.Run(algorithm, func(t *testing.T) {
			config := authConfig{jwtAlgorithm: algorithm, jwtKey: base64.StdEncoding.EncodeToString([]byte("secret")), jwtSecretBase64: true, jwtPayload: `{"sub":"courier"}`, jwtHeaders: `{"kid":"one"}`}
			token, err := generateJWT(config)
			if err != nil {
				t.Fatal(err)
			}
			input, signature := jwtSigningInputAndSignature(t, token)
			_, hashFactory, _ := jwtHash(algorithm)
			mac := hmac.New(hashFactory, []byte("secret"))
			_, _ = mac.Write(input)
			if !hmac.Equal(signature, mac.Sum(nil)) {
				t.Fatalf("invalid %s signature", algorithm)
			}
			parts := strings.Split(token, ".")
			header, _ := base64.RawURLEncoding.DecodeString(parts[0])
			payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
			if !strings.Contains(string(header), `"alg":"`+algorithm+`"`) || !strings.Contains(string(header), `"kid":"one"`) || !strings.Contains(string(payload), `"sub":"courier"`) {
				t.Fatalf("JWT contents = header %s payload %s", header, payload)
			}
		})
	}
}

func TestGenerateJWTRSAAndPSSAlgorithms(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustMarshalPKCS8(t, key)})
	for _, algorithm := range []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512"} {
		t.Run(algorithm, func(t *testing.T) {
			token, err := generateJWT(authConfig{jwtAlgorithm: algorithm, jwtKey: string(keyPEM), jwtPayload: `{}`})
			if err != nil {
				t.Fatal(err)
			}
			input, signature := jwtSigningInputAndSignature(t, token)
			hashID, hashFactory, _ := jwtHash(algorithm)
			digest := hashFactory()
			_, _ = digest.Write(input)
			if strings.HasPrefix(algorithm, "PS") {
				err = rsa.VerifyPSS(&key.PublicKey, hashID, digest.Sum(nil), signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: hashID})
			} else {
				err = rsa.VerifyPKCS1v15(&key.PublicKey, hashID, digest.Sum(nil), signature)
			}
			if err != nil {
				t.Fatalf("verify %s: %v", algorithm, err)
			}
		})
	}
}

func TestGenerateJWTECDSAAlgorithms(t *testing.T) {
	for _, test := range []struct {
		algorithm string
		curve     elliptic.Curve
		hash      crypto.Hash
	}{
		{algorithm: "ES256", curve: elliptic.P256(), hash: crypto.SHA256},
		{algorithm: "ES384", curve: elliptic.P384(), hash: crypto.SHA384},
		{algorithm: "ES512", curve: elliptic.P521(), hash: crypto.SHA512},
	} {
		t.Run(test.algorithm, func(t *testing.T) {
			key, err := ecdsa.GenerateKey(test.curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			token, err := generateJWT(authConfig{jwtAlgorithm: test.algorithm, jwtKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustMarshalPKCS8(t, key)})), jwtPayload: `{}`})
			if err != nil {
				t.Fatal(err)
			}
			input, signature := jwtSigningInputAndSignature(t, token)
			partLength := (key.Curve.Params().BitSize + 7) / 8
			if len(signature) != partLength*2 {
				t.Fatalf("%s signature length = %d", test.algorithm, len(signature))
			}
			digest := test.hash.New()
			_, _ = digest.Write(input)
			if !ecdsa.Verify(&key.PublicKey, digest.Sum(nil), new(big.Int).SetBytes(signature[:partLength]), new(big.Int).SetBytes(signature[partLength:])) {
				t.Fatalf("invalid %s signature", test.algorithm)
			}
		})
	}
}

func mustMarshalPKCS8(t *testing.T, key any) []byte {
	t.Helper()
	data, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestJWTAuthHeaderQueryVariablesAndPrivateKeyPath(t *testing.T) {
	var authorizations, queryTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		queryTokens = append(queryTokens, request.URL.Query().Get("assertion"))
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	m := NewModel()
	m.urlInput.SetValue(server.URL)
	m.variablesInput.SetEntries([]headerEntry{{key: "subject", value: "alice"}, {key: "secret", value: "shared"}})
	m.authInput.SetConfig(authConfig{typeID: authJWTBearer, jwtAlgorithm: "HS256", jwtKey: "{{secret}}", jwtPayload: `{"sub":"{{subject}}"}`, jwtHeaders: `{}`, jwtPrefix: "JWT"})
	if response := m.DoRequest()().(responseMsg); response.statusCode != http.StatusNoContent {
		t.Fatalf("JWT header response = %#v", response)
	}
	if !strings.HasPrefix(authorizations[0], "JWT ") || queryTokens[0] != "" {
		t.Fatalf("JWT header auth = %q query=%q", authorizations[0], queryTokens[0])
	}

	m.authInput.SetConfig(authConfig{typeID: authJWTBearer, jwtAlgorithm: "HS256", jwtKey: "shared", jwtPayload: `{}`, jwtHeaders: `{}`, jwtLocation: apiKeyQuery, jwtQueryName: "assertion"})
	if response := m.DoRequest()().(responseMsg); response.statusCode != http.StatusNoContent {
		t.Fatalf("JWT query response = %#v", response)
	}
	if authorizations[1] != "" || strings.Count(queryTokens[1], ".") != 2 {
		t.Fatalf("JWT query auth = header %q query=%q", authorizations[1], queryTokens[1])
	}

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPath := filepath.Join(t.TempDir(), "jwt.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustMarshalPKCS8(t, key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := generateJWT(authConfig{jwtAlgorithm: "RS256", jwtKey: keyPath, jwtPayload: `{}`}); err != nil {
		t.Fatalf("JWT private key path: %v", err)
	}
}

func TestJWTConfigurationWorkspaceAndPostmanRoundTrip(t *testing.T) {
	config := authConfig{typeID: authJWTBearer, jwtAlgorithm: "HS384", jwtKey: "secret", jwtSecretBase64: true, jwtPayload: `{"aud":"api"}`, jwtHeaders: `{"kid":"one"}`, jwtPrefix: "Token", jwtLocation: apiKeyQuery, jwtQueryName: "assertion"}
	workspace := config.toWorkspace().fromWorkspace()
	if workspace != config {
		t.Fatalf("JWT workspace round trip = %#v", workspace)
	}

	exported := exportPostmanAuth(config)
	data, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var imported postmanAuth
	if err := json.Unmarshal(data, &imported); err != nil {
		t.Fatal(err)
	}
	if roundTrip := postmanAuthConfig(&imported); roundTrip != config {
		t.Fatalf("JWT Postman round trip = %#v\nJSON: %s", roundTrip, data)
	}
}

func TestJWTRejectsInvalidConfiguration(t *testing.T) {
	for _, config := range []authConfig{
		{jwtAlgorithm: "none", jwtKey: "secret", jwtPayload: `{}`},
		{jwtAlgorithm: "HS256", jwtKey: "", jwtPayload: `{}`},
		{jwtAlgorithm: "HS256", jwtKey: "secret", jwtPayload: `not-json`},
		{jwtAlgorithm: "HS256", jwtKey: "%%%", jwtSecretBase64: true, jwtPayload: `{}`},
	} {
		if _, err := generateJWT(config); err == nil {
			t.Fatalf("generateJWT(%#v) unexpectedly succeeded", config)
		}
	}
}

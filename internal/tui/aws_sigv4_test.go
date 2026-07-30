package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAWSSigV4MatchesOfficialS3Example(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/?lifecycle", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2013, time.May, 24, 0, 0, 0, 0, time.UTC)
	result, err := signAWSv4(request, nil, authConfig{
		awsAccessKey: "AKIAIOSFODNN7EXAMPLE",
		awsSecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		awsRegion:    "us-east-1",
		awsService:   "s3",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Signature != "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543" {
		t.Fatalf("signature = %s\ncanonical request:\n%s\nstring to sign:\n%s", result.Signature, result.CanonicalRequest, result.StringToSign)
	}
	wantAuthorization := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + result.Signature
	if request.Header.Get("Authorization") != wantAuthorization {
		t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
	}
}

func TestDoRequest_AWSSigV4SignsPayloadAndSessionToken(t *testing.T) {
	const payload = `{"signed":true}`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != payload {
			http.Error(response, "invalid body", http.StatusBadRequest)
			return
		}
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=TESTACCESS/") || !strings.Contains(authorization, "/us-west-2/execute-api/aws4_request") || !strings.Contains(authorization, "x-amz-security-token") {
			http.Error(response, "invalid authorization", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("X-Amz-Content-Sha256") != awsSHA256Hex([]byte(payload)) || request.Header.Get("X-Amz-Security-Token") != "temporary-token" || request.Header.Get("X-Amz-Date") == "" {
			http.Error(response, "missing signed headers", http.StatusUnauthorized)
			return
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("signed request accepted"))
	}))
	defer server.Close()

	m := NewModel()
	m.methodIdx = methodIndex(t, http.MethodPost)
	m.urlInput.SetValue(server.URL + "/resource")
	m.bodyMode = bodyRaw
	m.rawBodyType = rawJSON
	m.bodyInput.SetValue(payload)
	m.variablesInput.SetEntries([]headerEntry{
		{key: "awsAccess", value: "TESTACCESS"}, {key: "awsSecret", value: "TESTSECRET"},
		{key: "awsRegion", value: "us-west-2"}, {key: "awsToken", value: "temporary-token"},
	})
	m.authInput.SetConfig(authConfig{
		typeID: authAWSSignatureV4, awsAccessKey: "{{awsAccess}}", awsSecretKey: "{{awsSecret}}",
		awsRegion: "{{awsRegion}}", awsService: "execute-api", awsSessionToken: "{{awsToken}}",
	})
	result := m.DoRequest()().(responseMsg)
	if result.statusCode != http.StatusOK || !strings.Contains(stripANSI(result.responseBody), "signed request accepted") {
		t.Fatalf("AWS signed result = status %d body %q metadata %q", result.statusCode, result.responseBody, result.responseMeta)
	}
}

func TestAWSCanonicalEncodingAndRequiredFields(t *testing.T) {
	if got := awsCanonicalURI("/folder name/ü.txt"); got != "/folder%20name/%C3%BC.txt" {
		t.Fatalf("canonical URI = %q", got)
	}
	if got := awsCanonicalQuery(map[string][]string{"space": {"a b"}, "slash": {"a/b"}, "empty": {""}}); got != "empty=&slash=a%2Fb&space=a%20b" {
		t.Fatalf("canonical query = %q", got)
	}
	request, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = signAWSv4(request, nil, authConfig{awsSecretKey: "secret", awsRegion: "us-east-1", awsService: "execute-api"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "access key") {
		t.Fatalf("missing access key error = %v", err)
	}
}

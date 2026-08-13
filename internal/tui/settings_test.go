package tui

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

func TestConfiguredClientRejectsHTTPSDowngrade(t *testing.T) {
	t.Parallel()

	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationCalls.Add(1)
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	client, err := configuredClient(source.Client(), requestSettings{followRedirects: true, timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, source.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("X-Custom-API-Key", "secret")
	response, err := client.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "refusing insecure redirect") {
		t.Fatalf("HTTPS downgrade error = %v", err)
	}
	if destinationCalls.Load() != 0 {
		t.Fatalf("downgrade destination received %d requests", destinationCalls.Load())
	}
}

func TestConfiguredClientStripsCrossOriginHeaders(t *testing.T) {
	t.Parallel()

	received := make(chan http.Header, 1)
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received <- request.Header.Clone()
		response.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client, err := configuredClient(source.Client(), requestSettings{followRedirects: true, timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, source.URL, strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Cookie", "session=secret")
	request.Header.Set("X-Custom-API-Key", "secret")
	request.Header.Set("X-Trace-Secret", "secret")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	headers := <-received
	for _, name := range []string{"Authorization", "Cookie", "X-Custom-API-Key", "X-Trace-Secret", "Referer"} {
		if value := headers.Get(name); value != "" {
			t.Fatalf("cross-origin %s = %q", name, value)
		}
	}
	if headers.Get("Accept") != "application/json" || headers.Get("Content-Type") != "application/json" {
		t.Fatalf("safe redirect headers were not preserved: %#v", headers)
	}
}

func TestConfiguredClientPreservesSameOriginHeaders(t *testing.T) {
	t.Parallel()

	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			http.Redirect(response, request, "/final", http.StatusFound)
			return
		}
		received <- request.Header.Get("X-Custom-API-Key")
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := configuredClient(server.Client(), requestSettings{followRedirects: true, timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Custom-API-Key", "same-origin-secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if value := <-received; value != "same-origin-secret" {
		t.Fatalf("same-origin API key = %q", value)
	}
}

func TestDoRequest_CustomCAAndMutualTLS(t *testing.T) {
	tempDir := t.TempDir()
	ca, caKey, caPEM := testCertificateAuthority(t)
	serverCertificate, _, _ := testSignedCertificate(t, ca, caKey, "courier-server", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []net.IP{net.ParseIP("127.0.0.1")})
	clientCertificate, clientCertPEM, clientKeyPEM := testSignedCertificate(t, ca, caKey, "courier-client", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil)

	clientCAPool := x509.NewCertPool()
	clientCAPool.AddCert(ca)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if len(request.TLS.PeerCertificates) == 0 || request.TLS.PeerCertificates[0].Subject.CommonName != "courier-client" {
			http.Error(w, "missing client identity", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("mutual TLS response"))
	}))
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAPool,
	}
	server.StartTLS()
	defer server.Close()

	writeTestPEM(t, filepath.Join(tempDir, "root-ca.pem"), caPEM)
	writeTestPEM(t, filepath.Join(tempDir, "client.pem"), clientCertPEM)
	writeTestPEM(t, filepath.Join(tempDir, "client-key.pem"), clientKeyPEM)

	m := NewModel()
	m.bodyMode = bodyNone
	m.urlInput.SetValue(server.URL)
	m.variablesInput.SetEntries([]headerEntry{{key: "certDir", value: tempDir}})
	m.settings.SetConfig(requestSettings{
		followRedirects: true,
		timeout:         5 * time.Second,
		caCertPath:      "{{certDir}}/root-ca.pem",
		clientCertPath:  "{{certDir}}/client.pem",
		clientKeyPath:   "{{certDir}}/client-key.pem",
	})

	response := m.DoRequest()().(responseMsg)
	if response.statusCode != http.StatusOK || !strings.Contains(stripANSI(response.responseBody), "mutual TLS response") {
		t.Fatalf("mutual TLS request failed: status=%d body=%q metadata=%q", response.statusCode, response.responseBody, response.responseMeta)
	}

	leaf, err := x509.ParseCertificate(clientCertificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pfxData, err := pkcs12.Modern.Encode(clientCertificate.PrivateKey, leaf, []*x509.Certificate{ca}, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	pfxPath := filepath.Join(tempDir, "client.pfx")
	if err := os.WriteFile(pfxPath, pfxData, 0o600); err != nil {
		t.Fatal(err)
	}
	m.settings.SetConfig(requestSettings{followRedirects: true, timeout: 5 * time.Second, caCertPath: "{{certDir}}/root-ca.pem", clientPFXPath: "{{certDir}}/client.pfx", clientPFXPassword: "correct horse"})
	response = m.DoRequest()().(responseMsg)
	if response.statusCode != http.StatusOK || !strings.Contains(stripANSI(response.responseBody), "mutual TLS response") {
		t.Fatalf("PFX mutual TLS request failed: status=%d body=%q metadata=%q", response.statusCode, response.responseBody, response.responseMeta)
	}

	m.settings.SetConfig(requestSettings{followRedirects: true, timeout: 5 * time.Second, caCertPath: "{{certDir}}/root-ca.pem", clientPFXPath: "{{certDir}}/client.pfx", clientPFXPassword: "wrong"})
	response = m.DoRequest()().(responseMsg)
	if !strings.Contains(response.responseBody, "decode client PFX bundle") {
		t.Fatalf("wrong PFX passphrase error = %#v", response)
	}

	m.settings.SetConfig(requestSettings{followRedirects: true, timeout: 5 * time.Second, caCertPath: "{{certDir}}/root-ca.pem"})
	response = m.DoRequest()().(responseMsg)
	if !strings.Contains(response.responseMeta, "Request failed") {
		t.Fatalf("server requiring a client certificate accepted request: %#v", response)
	}
}

func TestConfiguredClientValidatesTLSFiles(t *testing.T) {
	baseSettings := requestSettings{followRedirects: true, timeout: time.Second}
	tests := []struct {
		name   string
		mutate func(*requestSettings)
		want   string
	}{
		{name: "certificate without key", mutate: func(settings *requestSettings) { settings.clientCertPath = "client.pem" }, want: "configured together"},
		{name: "key without certificate", mutate: func(settings *requestSettings) { settings.clientKeyPath = "client-key.pem" }, want: "configured together"},
		{name: "missing CA", mutate: func(settings *requestSettings) { settings.caCertPath = filepath.Join(t.TempDir(), "missing.pem") }, want: "read CA bundle"},
		{name: "PFX with PEM", mutate: func(settings *requestSettings) {
			settings.clientCertPath = "client.pem"
			settings.clientKeyPath = "key.pem"
			settings.clientPFXPath = "client.pfx"
		}, want: "either a client PFX"},
		{name: "PFX password without path", mutate: func(settings *requestSettings) { settings.clientPFXPassword = "secret" }, want: "requires a PFX path"},
		{name: "missing PFX", mutate: func(settings *requestSettings) { settings.clientPFXPath = filepath.Join(t.TempDir(), "missing.pfx") }, want: "read client PFX"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := baseSettings
			test.mutate(&settings)
			_, err := configuredClient(http.DefaultClient, settings)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("configuredClient error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestConfiguredClientForcesHTTPVersion(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = w.Write([]byte(request.Proto))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	for _, test := range []struct {
		name    string
		version httpVersion
		major   int
	}{
		{name: "HTTP 1", version: httpVersion1, major: 1},
		{name: "HTTP 2", version: httpVersion2, major: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := configuredClient(server.Client(), requestSettings{followRedirects: true, timeout: 5 * time.Second, httpVersion: test.version})
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.ProtoMajor != test.major {
				t.Fatalf("negotiated %s, want HTTP/%d", response.Proto, test.major)
			}
		})
	}
}

func TestConfiguredClientRejectsUnavailableHTTP2(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.EnableHTTP2 = false
	server.StartTLS()
	defer server.Close()
	client, err := configuredClient(server.Client(), requestSettings{followRedirects: true, timeout: 2 * time.Second, httpVersion: httpVersion2})
	if err != nil {
		t.Fatal(err)
	}
	if response, requestErr := client.Get(server.URL); requestErr == nil {
		_ = response.Body.Close()
		t.Fatalf("forced HTTP/2 unexpectedly negotiated %s", response.Proto)
	}
}

func TestSettingsCyclesHTTPVersion(t *testing.T) {
	settings := newSettingsPane()
	settings.page = settingsNetwork
	settings.cursor = 2
	settings.UpdateNormal("right")
	if settings.config.httpVersion != httpVersion1 || !strings.Contains(settings.View(), "HTTP/1.x") {
		t.Fatalf("HTTP version after right = %v, view %q", settings.config.httpVersion, settings.View())
	}
	settings.UpdateNormal("space")
	if settings.config.httpVersion != httpVersion2 {
		t.Fatalf("HTTP version after space = %v", settings.config.httpVersion)
	}
	settings.UpdateNormal("left")
	if settings.config.httpVersion != httpVersion1 {
		t.Fatalf("HTTP version after left = %v", settings.config.httpVersion)
	}
}

func TestSettingsMasksPFXPassphrase(t *testing.T) {
	settings := newSettingsPane()
	settings.SetConfig(requestSettings{clientPFXPath: "/tmp/client.pfx", clientPFXPassword: "top-secret"})
	settings.page = settingsTLS
	view := settings.View()
	if strings.Contains(view, "top-secret") || !strings.Contains(view, "/tmp/client.pfx") {
		t.Fatalf("PFX settings view exposed passphrase or hid path: %q", view)
	}
}

func testCertificateAuthority(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Courier test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func testSignedCertificate(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, commonName string, usages []x509.ExtKeyUsage, addresses []net.IP) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial := big.NewInt(2)
	if len(usages) > 0 && usages[0] == x509.ExtKeyUsageClientAuth {
		serial = big.NewInt(3)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  usages,
		IPAddresses:  addresses,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, certificatePEM, keyPEM
}

func writeTestPEM(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsAllowNoTimeoutForLongLivedStreams(t *testing.T) {
	settings := newSettingsPane()
	settings.page = settingsNetwork
	settings.cursor = 1
	settings.config.timeout = time.Second
	settings.UpdateNormal("left")
	if settings.config.timeout != 0 || !strings.Contains(settings.View(), "no limit") {
		t.Fatalf("no-timeout setting = %s view=%q", settings.config.timeout, settings.View())
	}
	client, err := configuredClient(&http.Client{Timeout: time.Second}, settings.config)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 0 {
		t.Fatalf("configured client timeout = %s", client.Timeout)
	}
	settings.UpdateNormal("right")
	if settings.config.timeout != time.Second {
		t.Fatalf("timeout after right = %s", settings.config.timeout)
	}
}

package tui

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfiguredClientSOCKS5HProxyAndBypass(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("through socks"))
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	targetURL.Host = net.JoinHostPort("localhost", targetURL.Port())

	proxyAddress, observed, accepted, closeProxy := startTestSOCKSProxy(t, 5, "proxy-user", "proxy-pass")
	defer closeProxy()
	client, err := configuredClient(http.DefaultClient, requestSettings{
		followRedirects: true, timeout: 3 * time.Second, proxyURL: "socks5h://proxy-user:proxy-pass@" + proxyAddress,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(targetURL.String())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != "through socks" {
		t.Fatalf("proxied response = %q", body)
	}
	select {
	case got := <-observed:
		if got != targetURL.Host {
			t.Fatalf("SOCKS5H target = %q, want %q", got, targetURL.Host)
		}
	case <-time.After(time.Second):
		t.Fatal("SOCKS5 proxy did not observe target")
	}

	bypassClient, err := configuredClient(http.DefaultClient, requestSettings{
		followRedirects: true, timeout: 3 * time.Second, proxyURL: "socks5h://" + proxyAddress, proxyBypass: "localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	bypassResponse, err := bypassClient.Get(targetURL.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = bypassResponse.Body.Close()
	if accepted.Load() != 1 {
		t.Fatalf("proxy accepted %d connections after bypassed request, want 1", accepted.Load())
	}
}

func TestConfiguredClientSOCKS4AProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("through socks4a"))
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	targetURL.Host = net.JoinHostPort("localhost", targetURL.Port())
	proxyAddress, observed, _, closeProxy := startTestSOCKSProxy(t, 4, "proxy-user", "")
	defer closeProxy()
	client, err := configuredClient(http.DefaultClient, requestSettings{followRedirects: true, timeout: 3 * time.Second, proxyURL: "socks4a://proxy-user@" + proxyAddress})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(targetURL.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := <-observed; got != targetURL.Host {
		t.Fatalf("SOCKS4A target = %q, want %q", got, targetURL.Host)
	}
}

func TestConfiguredClientRejectsInvalidProxySchemes(t *testing.T) {
	for _, proxyURL := range []string{"ftp://proxy.example:21", "socks4://user:password@proxy.example:1080", "socks5://"} {
		if _, err := configuredClient(http.DefaultClient, requestSettings{proxyURL: proxyURL}); err == nil {
			t.Fatalf("invalid proxy URL %q was accepted", proxyURL)
		}
	}
}

func startTestSOCKSProxy(t *testing.T, version int, expectedUser, expectedPassword string) (string, <-chan string, *atomic.Int32, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan string, 4)
	accepted := new(atomic.Int32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepted.Add(1)
			go serveTestSOCKSConnection(t, connection, version, expectedUser, expectedPassword, observed)
		}
	}()
	return listener.Addr().String(), observed, accepted, func() {
		_ = listener.Close()
		<-done
	}
}

func serveTestSOCKSConnection(t *testing.T, connection net.Conn, version int, expectedUser, expectedPassword string, observed chan<- string) {
	t.Helper()
	defer func() { _ = connection.Close() }()
	reader := bufio.NewReader(connection)
	var targetAddress string
	var err error
	if version == 5 {
		targetAddress, err = readTestSOCKS5Handshake(reader, connection, expectedUser, expectedPassword)
	} else {
		targetAddress, err = readTestSOCKS4Handshake(reader, connection, expectedUser)
	}
	if err != nil {
		t.Errorf("SOCKS%d handshake: %v", version, err)
		return
	}
	observed <- targetAddress
	upstream, err := net.DialTimeout("tcp", targetAddress, time.Second)
	if err != nil {
		t.Errorf("dial SOCKS target: %v", err)
		return
	}
	defer func() { _ = upstream.Close() }()
	if version == 5 {
		_, _ = connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	} else {
		_, _ = connection.Write([]byte{0, 0x5a, 0, 0, 0, 0, 0, 0})
	}
	go func() { _, _ = io.Copy(upstream, reader) }()
	_, _ = io.Copy(connection, upstream)
}

func readTestSOCKS5Handshake(reader *bufio.Reader, writer io.Writer, expectedUser, expectedPassword string) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 5 {
		return "", fmt.Errorf("read greeting: %v", err)
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return "", err
	}
	method := byte(0)
	if expectedUser != "" {
		method = 2
	}
	if _, err := writer.Write([]byte{5, method}); err != nil {
		return "", err
	}
	if method == 2 {
		authHeader := make([]byte, 2)
		if _, err := io.ReadFull(reader, authHeader); err != nil {
			return "", err
		}
		username := make([]byte, int(authHeader[1]))
		_, _ = io.ReadFull(reader, username)
		passwordLength, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		password := make([]byte, int(passwordLength))
		_, _ = io.ReadFull(reader, password)
		if string(username) != expectedUser || string(password) != expectedPassword {
			return "", fmt.Errorf("credentials %q/%q", username, password)
		}
		_, _ = writer.Write([]byte{1, 0})
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(reader, request); err != nil || request[0] != 5 || request[1] != 1 {
		return "", fmt.Errorf("read connect request: %v", err)
	}
	var host string
	switch request[3] {
	case 1:
		value := make([]byte, 4)
		_, _ = io.ReadFull(reader, value)
		host = net.IP(value).String()
	case 3:
		length, _ := reader.ReadByte()
		value := make([]byte, int(length))
		_, _ = io.ReadFull(reader, value)
		host = string(value)
	case 4:
		value := make([]byte, 16)
		_, _ = io.ReadFull(reader, value)
		host = net.IP(value).String()
	default:
		return "", fmt.Errorf("address type %d", request[3])
	}
	portBytes := make([]byte, 2)
	_, _ = io.ReadFull(reader, portBytes)
	return net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(portBytes))), nil
}

func readTestSOCKS4Handshake(reader *bufio.Reader, writer io.Writer, expectedUser string) (string, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 4 || header[1] != 1 {
		return "", fmt.Errorf("read request: %v", err)
	}
	user, err := reader.ReadString(0)
	if err != nil || strings.TrimSuffix(user, "\x00") != expectedUser {
		return "", fmt.Errorf("user ID %q: %v", user, err)
	}
	host := net.IP(header[4:8]).String()
	if header[4] == 0 && header[5] == 0 && header[6] == 0 && header[7] != 0 {
		domain, readErr := reader.ReadString(0)
		if readErr != nil {
			return "", readErr
		}
		host = strings.TrimSuffix(domain, "\x00")
	}
	_ = writer
	return net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(header[2:4]))), nil
}

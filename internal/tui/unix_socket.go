package tui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type unixSocketTarget struct {
	scheme     string
	socketPath string
	requestURL string
}

func parseUnixSocketURL(value string) (*unixSocketTarget, bool, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	scheme := "http"
	var remainder string
	switch {
	case strings.HasPrefix(lower, "unix:"):
		remainder = value[len("unix:"):]
	case strings.HasPrefix(lower, "http://unix:"):
		remainder = value[len("http://unix:"):]
	case strings.HasPrefix(lower, "https://unix:"):
		scheme = "https"
		remainder = value[len("https://unix:"):]
	default:
		return nil, false, nil
	}

	socketPath := remainder
	resource := "/"
	if delimiter := strings.Index(remainder, ":/"); delimiter >= 0 {
		socketPath = remainder[:delimiter]
		resource = remainder[delimiter+1:]
	}
	if socketPath == "" || !strings.HasPrefix(socketPath, "/") {
		return nil, true, fmt.Errorf("unix socket path must be absolute")
	}
	if resource == "" {
		resource = "/"
	}
	parsedResource, err := url.Parse(resource)
	if err != nil || !strings.HasPrefix(parsedResource.Path, "/") {
		return nil, true, fmt.Errorf("unix socket resource must begin with /")
	}
	parsedResource.Scheme = scheme
	parsedResource.Host = "localhost"
	return &unixSocketTarget{scheme: scheme, socketPath: socketPath, requestURL: parsedResource.String()}, true, nil
}

func (target *unixSocketTarget) displayURL(requestURL *url.URL) string {
	if target == nil || requestURL == nil {
		return ""
	}
	return target.scheme + "://unix:" + target.socketPath + ":" + requestURL.RequestURI()
}

func configureUnixSocketClient(client *http.Client, target *unixSocketTarget) (*http.Client, error) {
	if target == nil {
		return client, nil
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		return nil, fmt.Errorf("unix socket requests require an HTTP transport")
	}
	configured := *client
	transport = transport.Clone()
	transport.Proxy = nil
	transport.DialTLS = nil //nolint:staticcheck // Clear a legacy custom TLS dialer so the Unix DialContext is honored.
	transport.DialTLSContext = nil
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", target.socketPath)
	}
	configured.Transport = transport
	return &configured, nil
}

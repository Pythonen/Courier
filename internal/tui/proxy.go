package tui

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpproxy"
	xproxy "golang.org/x/net/proxy"
)

func configureProxyTransport(transport *http.Transport, rawURL, bypass string) error {
	proxyURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse proxy URL: %w", err)
	}
	if proxyURL.Hostname() == "" {
		return fmt.Errorf("proxy URL is missing a host")
	}
	scheme := strings.ToLower(proxyURL.Scheme)
	switch scheme {
	case "http", "https":
		config := &httpproxy.Config{HTTPProxy: proxyURL.String(), HTTPSProxy: proxyURL.String(), NoProxy: bypass}
		proxyFunc := config.ProxyFunc()
		transport.Proxy = func(request *http.Request) (*url.URL, error) { return proxyFunc(request.URL) }
		return nil
	case "socks4", "socks4a", "socks5", "socks5h":
	default:
		return fmt.Errorf("proxy URL must use http, https, socks4, socks4a, socks5, or socks5h")
	}

	proxyAddress := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddress = net.JoinHostPort(proxyURL.Hostname(), "1080")
	}
	baseDial := transport.DialContext
	if baseDial == nil {
		baseDial = (&net.Dialer{}).DialContext
	}
	var proxyDial func(context.Context, string, string) (net.Conn, error)
	if scheme == "socks4" || scheme == "socks4a" {
		user := ""
		if proxyURL.User != nil {
			user = proxyURL.User.Username()
			if password, present := proxyURL.User.Password(); present && password != "" {
				return fmt.Errorf("SOCKS4 proxy URLs support a user ID but not a password")
			}
		}
		dialer := socks4Dialer{proxyAddress: proxyAddress, user: user, remoteDNS: scheme == "socks4a", forward: baseDial}
		proxyDial = dialer.DialContext
	} else {
		var auth *xproxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		dialer, dialErr := xproxy.SOCKS5("tcp", proxyAddress, auth, xproxy.Direct)
		if dialErr != nil {
			return fmt.Errorf("configure SOCKS5 proxy: %w", dialErr)
		}
		proxyDial = func(ctx context.Context, network, address string) (net.Conn, error) {
			if scheme == "socks5" {
				resolved, resolveErr := resolveProxyTarget(ctx, address)
				if resolveErr != nil {
					return nil, resolveErr
				}
				address = resolved
			}
			if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
				return contextDialer.DialContext(ctx, network, address)
			}
			return dialer.Dial(network, address)
		}
	}
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if proxyAddressBypassed(bypass, address) {
			return baseDial(ctx, network, address)
		}
		return proxyDial(ctx, network, address)
	}
	return nil
}

func proxyAddressBypassed(bypass, address string) bool {
	if strings.TrimSpace(bypass) == "" {
		return false
	}
	config := &httpproxy.Config{HTTPProxy: "http://proxy.invalid", HTTPSProxy: "http://proxy.invalid", NoProxy: bypass}
	proxyURL, err := config.ProxyFunc()(&url.URL{Scheme: "http", Host: address})
	return err == nil && proxyURL == nil
}

func resolveProxyTarget(ctx context.Context, address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("parse proxy target %q: %w", address, err)
	}
	if net.ParseIP(host) != nil {
		return address, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("resolve proxy target %q: %w", host, err)
	}
	for _, candidate := range addresses {
		if candidate.IP != nil {
			return net.JoinHostPort(candidate.IP.String(), port), nil
		}
	}
	return "", fmt.Errorf("proxy target %q resolved to no addresses", host)
}

type socks4Dialer struct {
	proxyAddress string
	user         string
	remoteDNS    bool
	forward      func(context.Context, string, string) (net.Conn, error)
}

func (dialer socks4Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse SOCKS4 target %q: %w", address, err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("invalid SOCKS4 target port %q", portText)
	}
	var domain string
	ip := net.ParseIP(host).To4()
	if ip == nil {
		if dialer.remoteDNS {
			ip = net.IPv4(0, 0, 0, 1).To4()
			domain = host
		} else {
			resolved, resolveErr := resolveProxyTarget(ctx, address)
			if resolveErr != nil {
				return nil, resolveErr
			}
			resolvedHost, _, _ := net.SplitHostPort(resolved)
			ip = net.ParseIP(resolvedHost).To4()
			if ip == nil {
				return nil, fmt.Errorf("SOCKS4 requires an IPv4 target address")
			}
		}
	}
	connection, err := dialer.forward(ctx, "tcp", dialer.proxyAddress)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = connection.Close()
		}
	}()
	request := []byte{0x04, 0x01, byte(port >> 8), byte(port)}
	request = append(request, ip...)
	request = append(request, dialer.user...)
	request = append(request, 0)
	if domain != "" {
		request = append(request, domain...)
		request = append(request, 0)
	}
	if _, err := connection.Write(request); err != nil {
		return nil, fmt.Errorf("write SOCKS4 handshake: %w", err)
	}
	response := make([]byte, 8)
	if _, err := io.ReadFull(connection, response); err != nil {
		return nil, fmt.Errorf("read SOCKS4 handshake: %w", err)
	}
	if response[1] != 0x5a {
		return nil, fmt.Errorf("SOCKS4 proxy rejected connection with status 0x%02x", response[1])
	}
	success = true
	return connection, nil
}

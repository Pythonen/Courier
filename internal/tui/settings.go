package tui

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"software.sslmate.com/src/go-pkcs12"
)

type requestSettings struct {
	followRedirects   bool
	httpVersion       httpVersion
	skipTLSVerify     bool
	timeout           time.Duration
	proxyURL          string
	proxyBypass       string
	caCertPath        string
	clientCertPath    string
	clientKeyPath     string
	clientPFXPath     string
	clientPFXPassword string
}

type httpVersion int

const (
	httpVersionAuto httpVersion = iota
	httpVersion1
	httpVersion2
	httpVersionCount
)

func (version httpVersion) String() string {
	switch version {
	case httpVersion1:
		return "HTTP/1.x"
	case httpVersion2:
		return "HTTP/2"
	default:
		return "Auto"
	}
}

type settingsPage int

const (
	settingsNetwork settingsPage = iota
	settingsTLS
	settingsPageCount
)

type settingsPane struct {
	config                 requestSettings
	page                   settingsPage
	cursor                 int
	proxyInput             textinput.Model
	proxyBypassInput       textinput.Model
	caCertInput            textinput.Model
	clientCertInput        textinput.Model
	clientKeyInput         textinput.Model
	clientPFXInput         textinput.Model
	clientPFXPasswordInput textinput.Model
}

func newSettingsPane() settingsPane {
	newInput := func(placeholder string) textinput.Model {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholder
		input.CharLimit = 4096
		input.Blur()
		return input
	}
	pfxPassword := newInput("PFX passphrase (optional)")
	pfxPassword.EchoMode = textinput.EchoPassword
	pfxPassword.EchoCharacter = '•'
	return settingsPane{
		config:                 requestSettings{followRedirects: true, timeout: 30 * time.Second},
		proxyInput:             newInput("http://proxy.example:8080 (empty: environment)"),
		proxyBypassInput:       newInput("localhost,.example.test,10.0.0.0/8"),
		caCertInput:            newInput("/path/to/ca-bundle.pem (optional)"),
		clientCertInput:        newInput("/path/to/client-cert.pem (optional)"),
		clientKeyInput:         newInput("/path/to/client-key.pem (optional)"),
		clientPFXInput:         newInput("/path/to/client.pfx (optional)"),
		clientPFXPasswordInput: pfxPassword,
	}
}

func (s *settingsPane) syncConfig() {
	s.config.proxyURL = strings.TrimSpace(s.proxyInput.Value())
	s.config.proxyBypass = strings.TrimSpace(s.proxyBypassInput.Value())
	s.config.caCertPath = strings.TrimSpace(s.caCertInput.Value())
	s.config.clientCertPath = strings.TrimSpace(s.clientCertInput.Value())
	s.config.clientKeyPath = strings.TrimSpace(s.clientKeyInput.Value())
	s.config.clientPFXPath = strings.TrimSpace(s.clientPFXInput.Value())
	s.config.clientPFXPassword = s.clientPFXPasswordInput.Value()
}

func (s *settingsPane) SetConfig(config requestSettings) {
	s.config = config
	s.proxyInput.SetValue(config.proxyURL)
	s.proxyBypassInput.SetValue(config.proxyBypass)
	s.caCertInput.SetValue(config.caCertPath)
	s.clientCertInput.SetValue(config.clientCertPath)
	s.clientKeyInput.SetValue(config.clientKeyPath)
	s.clientPFXInput.SetValue(config.clientPFXPath)
	s.clientPFXPasswordInput.SetValue(config.clientPFXPassword)
	s.cursor = 0
	s.Blur()
}

func (s *settingsPane) fieldCount() int {
	if s.page == settingsTLS {
		return 6
	}
	return 5
}

func (s *settingsPane) Editable() bool { return s.currentInput() != nil }

func (s *settingsPane) currentInput() *textinput.Model {
	if s.page == settingsNetwork && s.cursor == 3 {
		return &s.proxyInput
	}
	if s.page == settingsNetwork && s.cursor == 4 {
		return &s.proxyBypassInput
	}
	if s.page == settingsTLS {
		switch s.cursor {
		case 1:
			return &s.caCertInput
		case 2:
			return &s.clientCertInput
		case 3:
			return &s.clientKeyInput
		case 4:
			return &s.clientPFXInput
		case 5:
			return &s.clientPFXPasswordInput
		}
	}
	return nil
}

func (s *settingsPane) SetWidth(width int) {
	inputWidth := max(10, width-16)
	for _, input := range []*textinput.Model{&s.proxyInput, &s.proxyBypassInput, &s.caCertInput, &s.clientCertInput, &s.clientKeyInput, &s.clientPFXInput, &s.clientPFXPasswordInput} {
		input.SetWidth(inputWidth)
	}
}

func (s *settingsPane) UpdateNormal(keyStr string) {
	switch keyStr {
	case "p":
		s.page = (s.page + 1) % settingsPageCount
		s.cursor = 0
		s.blurInputs()
	case "j", "down":
		if s.cursor < s.fieldCount()-1 {
			s.cursor++
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case " ", "space":
		if s.page == settingsNetwork && s.cursor == 0 {
			s.config.followRedirects = !s.config.followRedirects
		} else if s.page == settingsNetwork && s.cursor == 2 {
			s.config.httpVersion = (s.config.httpVersion + 1) % httpVersionCount
		} else if s.page == settingsTLS && s.cursor == 0 {
			s.config.skipTLSVerify = !s.config.skipTLSVerify
		}
	case "h", "left":
		if s.page == settingsNetwork && s.cursor == 1 && s.config.timeout >= time.Second {
			s.config.timeout -= time.Second
		} else if s.page == settingsNetwork && s.cursor == 2 {
			s.config.httpVersion = (s.config.httpVersion - 1 + httpVersionCount) % httpVersionCount
		}
	case "l", "right":
		if s.page == settingsNetwork && s.cursor == 1 && s.config.timeout < 10*time.Minute {
			s.config.timeout += time.Second
		} else if s.page == settingsNetwork && s.cursor == 2 {
			s.config.httpVersion = (s.config.httpVersion + 1) % httpVersionCount
		}
	}
}

func (s *settingsPane) FocusCurrent() tea.Cmd {
	s.blurInputs()
	if input := s.currentInput(); input != nil {
		return input.Focus()
	}
	return nil
}

func (s *settingsPane) UpdateInput(msg tea.Msg) tea.Cmd {
	input := s.currentInput()
	if input == nil {
		return nil
	}
	var cmd tea.Cmd
	*input, cmd = input.Update(msg)
	return cmd
}

func (s *settingsPane) blurInputs() {
	s.proxyInput.Blur()
	s.proxyBypassInput.Blur()
	s.caCertInput.Blur()
	s.clientCertInput.Blur()
	s.clientKeyInput.Blur()
	s.clientPFXInput.Blur()
	s.clientPFXPasswordInput.Blur()
}

func (s *settingsPane) Blur() {
	s.blurInputs()
	s.syncConfig()
}

func (s settingsPane) View() string {
	boolValue := func(value bool) string {
		if value {
			return activeCellStyle.Render("On")
		}
		return hintStyle.Render("Off")
	}
	pageName := "Network"
	var rows []string
	if s.page == settingsTLS {
		pageName = "TLS & certificates"
		rows = []string{
			"Skip TLS verify: " + boolValue(s.config.skipTLSVerify),
			"CA bundle:       " + s.caCertInput.View(),
			"Client cert:     " + s.clientCertInput.View(),
			"Client key:      " + s.clientKeyInput.View(),
			"Client PFX:      " + s.clientPFXInput.View(),
			"PFX passphrase:  " + s.clientPFXPasswordInput.View(),
		}
	} else {
		timeoutValue := s.config.timeout.String()
		if s.config.timeout == 0 {
			timeoutValue = "no limit"
		}
		rows = []string{
			"Follow redirects: " + boolValue(s.config.followRedirects),
			fmt.Sprintf("Timeout:          %s", timeoutValue),
			"HTTP version:     " + activeCellStyle.Render(s.config.httpVersion.String()),
			"Proxy:            " + s.proxyInput.View(),
			"Proxy bypass:     " + s.proxyBypassInput.View(),
		}
	}
	for index := range rows {
		prefix := "  "
		if index == s.cursor {
			prefix = activeCellStyle.Render("> ")
		}
		rows[index] = prefix + rows[index]
	}
	header := headerStyle.Render(pageName) + hintStyle.Render("  p:page")
	rows = append([]string{header}, rows...)
	rows = append(rows, hintStyle.Render(" jk:move  space:toggle  hl:adjust  i:edit  ctrl+t:close"))
	return strings.Join(rows, "\n")
}

func configuredClient(base *http.Client, settings requestSettings) (*http.Client, error) {
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	client.Timeout = settings.timeout
	baseRedirectPolicy := client.CheckRedirect
	if !settings.followRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	} else {
		client.CheckRedirect = secureRedirectPolicy(baseRedirectPolicy)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if existing, ok := base.Transport.(*http.Transport); ok && existing != nil {
		transport = existing.Clone()
	} else if base.Transport != nil {
		if settings.httpVersion != httpVersionAuto || settings.skipTLSVerify || settings.proxyURL != "" || settings.caCertPath != "" || settings.clientCertPath != "" || settings.clientKeyPath != "" || settings.clientPFXPath != "" {
			return nil, fmt.Errorf("TLS/proxy settings require an HTTP transport")
		}
		client.Transport = base.Transport
		return &client, nil
	}
	needsTLSConfig := settings.httpVersion != httpVersionAuto || settings.skipTLSVerify || settings.caCertPath != "" || settings.clientCertPath != "" || settings.clientKeyPath != "" || settings.clientPFXPath != ""
	if needsTLSConfig {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
	}
	if settings.httpVersion != httpVersionAuto {
		protocols := new(http.Protocols)
		if settings.httpVersion == httpVersion1 {
			protocols.SetHTTP1(true)
			transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
			transport.ForceAttemptHTTP2 = false
		} else {
			protocols.SetHTTP2(true)
			transport.TLSClientConfig.NextProtos = []string{"h2"}
			transport.ForceAttemptHTTP2 = true
		}
		transport.Protocols = protocols
	}
	if settings.skipTLSVerify {
		transport.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec // Explicit user-controlled API client setting.
	}
	if settings.caCertPath != "" {
		certificatePEM, err := os.ReadFile(settings.caCertPath)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle %q: %w", settings.caCertPath, err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(certificatePEM) {
			return nil, fmt.Errorf("CA bundle %q contains no certificates", settings.caCertPath)
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	if (settings.clientCertPath == "") != (settings.clientKeyPath == "") {
		return nil, fmt.Errorf("client certificate and key paths must be configured together")
	}
	if settings.clientPFXPath != "" && (settings.clientCertPath != "" || settings.clientKeyPath != "") {
		return nil, fmt.Errorf("configure either a client PFX bundle or a PEM certificate/key pair, not both")
	}
	if settings.clientPFXPath == "" && settings.clientPFXPassword != "" {
		return nil, fmt.Errorf("client PFX passphrase requires a PFX path")
	}
	if settings.clientCertPath != "" {
		certificate, err := tls.LoadX509KeyPair(settings.clientCertPath, settings.clientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("load client certificate/key: %w", err)
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{certificate}
	}
	if settings.clientPFXPath != "" {
		pfxData, err := os.ReadFile(settings.clientPFXPath)
		if err != nil {
			return nil, fmt.Errorf("read client PFX bundle %q: %w", settings.clientPFXPath, err)
		}
		privateKey, leaf, chain, err := pkcs12.DecodeChain(pfxData, settings.clientPFXPassword)
		if err != nil {
			return nil, fmt.Errorf("decode client PFX bundle %q: %w", settings.clientPFXPath, err)
		}
		certificate := tls.Certificate{PrivateKey: privateKey, Leaf: leaf, Certificate: make([][]byte, 0, len(chain)+1)}
		certificate.Certificate = append(certificate.Certificate, leaf.Raw)
		for _, authority := range chain {
			certificate.Certificate = append(certificate.Certificate, authority.Raw)
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{certificate}
	}
	if settings.proxyURL != "" {
		if err := configureProxyTransport(transport, settings.proxyURL, settings.proxyBypass); err != nil {
			return nil, err
		}
	}
	client.Transport = transport
	return &client, nil
}

func secureRedirectPolicy(basePolicy func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		previous := via[len(via)-1]
		if strings.EqualFold(previous.URL.Scheme, "https") && !strings.EqualFold(request.URL.Scheme, "https") {
			return fmt.Errorf("refusing insecure redirect from %s to %s", redirectOrigin(previous.URL), redirectOrigin(request.URL))
		}
		if redirectOrigin(previous.URL) != redirectOrigin(request.URL) {
			stripCrossOriginRedirectHeaders(request.Header)
		}
		if basePolicy != nil {
			return basePolicy(request, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
}

func redirectOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	scheme := strings.ToLower(value.Scheme)
	port := value.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(value.Hostname()), port)
}

func stripCrossOriginRedirectHeaders(headers http.Header) {
	// Cross-origin redirects receive only ordinary content-negotiation headers.
	// This protects arbitrary user-named API-key headers as well as conventional
	// Authorization and Cookie fields.
	allowed := map[string]bool{
		"Accept":          true,
		"Accept-Encoding": true,
		"Accept-Language": true,
		"Cache-Control":   true,
		"Content-Type":    true,
		"User-Agent":      true,
	}
	for name := range headers {
		if !allowed[http.CanonicalHeaderKey(name)] {
			headers.Del(name)
		}
	}
}

package tui

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/eclipse/paho.golang/packets"
	mqttv5 "github.com/eclipse/paho.golang/paho"
	mqttv3 "github.com/eclipse/paho.mqtt.golang"
	uuid "github.com/google/uuid"
)

type mqttSubscription struct {
	topic string
	qos   byte
}

type mqttConfig struct {
	secure         bool
	version        string
	target         string
	url            string
	topic          string
	qos            byte
	retain         bool
	clientID       string
	cleanStart     bool
	keepAlive      time.Duration
	subscriptions  []mqttSubscription
	username       string
	password       string
	userProperties []headerEntry
	willTopic      string
	willPayload    []byte
	willQoS        byte
	willRetain     bool
}

type mqttTransport interface {
	publish(context.Context, string, []byte, byte, bool, []headerEntry) error
	disconnect()
}

type mqttSession struct {
	transport  mqttTransport
	requestID  uuid.UUID
	url        string
	started    time.Time
	config     mqttConfig
	assertions []headerEntry
	events     <-chan mqttEventMsg
	context    context.Context
	cancel     context.CancelFunc
	closeOnce  sync.Once
}

type mqttConnectedMsg struct {
	requestID uuid.UUID
	session   *mqttSession
	duration  time.Duration
	err       error
}

type mqttEventMsg struct {
	requestID uuid.UUID
	topic     string
	payload   []byte
	qos       byte
	retained  bool
	err       error
	events    <-chan mqttEventMsg
	session   *mqttSession
}

type mqttSentMsg struct {
	requestID uuid.UUID
	topic     string
	payload   []byte
	qos       byte
	retained  bool
	err       error
}

func isMQTTURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (strings.EqualFold(parsed.Scheme, "mqtt") || strings.EqualFold(parsed.Scheme, "mqtts"))
}

func (m *model) beginMQTTConnection() tea.Cmd {
	if m.mqtt != nil {
		terminateMQTTSession(m.mqtt)
		m.mqtt = nil
	}
	if m.mqttCancel != nil {
		m.mqttCancel()
	}
	requestID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	m.requestId = requestID
	m.requestContext = ctx
	m.mqttCancel = cancel
	m.cancelRequest = cancel
	m.response = ""
	m.responseRaw = ""
	m.responseRawAvailable = true
	m.responseHeaders = ""
	m.responseTests = ""
	m.responseStatusCode = 0
	m.responseMeta = "Connecting MQTT..."
	m.responseModel.SetContent("")
	m.responseHeadersModel.SetContent("")
	m.responseTestsModel.SetContent("")
	m.historyPos = 0
	m.history = append([]historyItem{{
		createdAt: time.Now().UTC(), method: "MQTT", url: m.urlInput.Value(),
		requestBody: m.bodyInput.Value(), requestHeaders: m.headersInput.Entries(), requestParams: m.paramsInput.Entries(),
		requestAuth: m.authInput.Config(), requestBodyConfig: m.bodyConfig(), requestCookies: m.cookiesInput.Entries(), requestTests: m.testsInput.Entries(),
		requestID: requestID,
	}}, m.history...)
	m.history = trimHistory(m.history)
	return m.connectMQTT(ctx, cancel, requestID)
}

func (m model) connectMQTT(ctx context.Context, cancel context.CancelFunc, requestID uuid.UUID) tea.Cmd {
	return func() tea.Msg {
		started := time.Now()
		resolver := newVariableResolver(m.variablesInput.Entries())
		config, err := m.mqttConfig(resolver)
		if err != nil {
			return mqttConnectedMsg{requestID: requestID, err: err}
		}
		m.settings.syncConfig()
		settings := m.settings.config
		settings.proxyURL = resolver.Resolve(settings.proxyURL)
		settings.proxyBypass = resolver.Resolve(settings.proxyBypass)
		settings.caCertPath = resolver.Resolve(settings.caCertPath)
		settings.clientCertPath = resolver.Resolve(settings.clientCertPath)
		settings.clientKeyPath = resolver.Resolve(settings.clientKeyPath)
		settings.clientPFXPath = resolver.Resolve(settings.clientPFXPath)
		settings.clientPFXPassword = resolver.Resolve(settings.clientPFXPassword)
		if settings.proxyURL != "" {
			return mqttConnectedMsg{requestID: requestID, err: fmt.Errorf("MQTT over raw TCP does not support the HTTP proxy setting")}
		}
		connectCtx := ctx
		var timeoutCancel context.CancelFunc
		if settings.timeout > 0 {
			connectCtx, timeoutCancel = context.WithTimeout(ctx, settings.timeout)
			defer timeoutCancel()
		}
		tlsConfig, err := mqttTLSConfig(m.client, settings, config)
		if err != nil {
			return mqttConnectedMsg{requestID: requestID, err: fmt.Errorf("configure MQTT transport: %w", err)}
		}
		events := make(chan mqttEventMsg, 32)
		emit := func(event mqttEventMsg) {
			event.requestID = requestID
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}
		var transport mqttTransport
		if config.version == "3.1.1" {
			transport, err = connectMQTTV3(connectCtx, config, tlsConfig, emit)
		} else {
			transport, err = connectMQTTV5(connectCtx, config, tlsConfig, emit)
		}
		if err != nil {
			return mqttConnectedMsg{requestID: requestID, err: fmt.Errorf("connect MQTT %s: %w", config.version, err)}
		}
		session := &mqttSession{
			transport: transport, requestID: requestID, url: config.url, started: started, config: config,
			assertions: m.testsInput.Entries(), events: events, context: ctx, cancel: cancel,
		}
		return mqttConnectedMsg{requestID: requestID, session: session, duration: time.Since(started)}
	}
}

func (m model) mqttConfig(resolver *variableResolver) (mqttConfig, error) {
	value := resolver.Resolve(m.urlInput.Value())
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "mqtt" && parsed.Scheme != "mqtts") {
		return mqttConfig{}, fmt.Errorf("MQTT URL must use mqtt:// or mqtts://")
	}
	if parsed.Host == "" {
		return mqttConfig{}, fmt.Errorf("MQTT URL is missing a broker host")
	}
	secure := parsed.Scheme == "mqtts"
	target := parsed.Host
	if parsed.Port() == "" {
		if secure {
			target = net.JoinHostPort(parsed.Hostname(), "8883")
		} else {
			target = net.JoinHostPort(parsed.Hostname(), "1883")
		}
	}
	values := parsed.Query()
	for _, parameter := range m.paramsInput.Entries() {
		values.Add(strings.ToLower(strings.TrimSpace(resolver.Resolve(parameter.key))), resolver.Resolve(parameter.value))
	}
	version := strings.TrimSpace(values.Get("version"))
	switch version {
	case "", "5", "5.0":
		version = "5.0"
	case "3", "3.1.1", "4":
		version = "3.1.1"
	default:
		return mqttConfig{}, fmt.Errorf("MQTT version must be 3.1.1 or 5.0")
	}
	qos, err := parseMQTTQoS(values.Get("qos"), 0)
	if err != nil {
		return mqttConfig{}, err
	}
	retain, err := parseMQTTBool(values.Get("retain"), false, "retain")
	if err != nil {
		return mqttConfig{}, err
	}
	cleanStart, err := parseMQTTBool(values.Get("clean_start"), true, "clean_start")
	if err != nil {
		return mqttConfig{}, err
	}
	keepAlive := 30 * time.Second
	if raw := strings.TrimSpace(values.Get("keep_alive")); raw != "" {
		keepAlive, err = time.ParseDuration(raw)
		if err != nil || keepAlive < time.Second || keepAlive > time.Duration(^uint16(0))*time.Second {
			return mqttConfig{}, fmt.Errorf("MQTT keep_alive must be between 1s and %ds", ^uint16(0))
		}
	}
	clientID := strings.TrimSpace(values.Get("client_id"))
	if clientID == "" {
		clientID = "courier-" + uuid.NewString()
	}
	subscriptions := make([]mqttSubscription, 0)
	for _, raw := range values["subscribe"] {
		subscription, parseErr := parseMQTTSubscription(raw, qos)
		if parseErr != nil {
			return mqttConfig{}, parseErr
		}
		subscriptions = append(subscriptions, subscription)
	}
	auth := m.authInput.Config().resolved(resolver)
	if auth.typeID != authNone && auth.typeID != authBasic {
		return mqttConfig{}, fmt.Errorf("MQTT supports No Auth or Basic Auth username/password")
	}
	willQoS, err := parseMQTTQoS(values.Get("will_qos"), 0)
	if err != nil {
		return mqttConfig{}, err
	}
	willRetain, err := parseMQTTBool(values.Get("will_retain"), false, "will_retain")
	if err != nil {
		return mqttConfig{}, err
	}
	properties := make([]headerEntry, 0, len(m.headersInput.Entries()))
	for _, property := range m.headersInput.Entries() {
		properties = append(properties, headerEntry{key: resolver.Resolve(property.key), value: resolver.Resolve(property.value)})
	}
	topic, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil {
		return mqttConfig{}, fmt.Errorf("decode MQTT topic: %w", err)
	}
	parsed.RawQuery = values.Encode()
	return mqttConfig{
		secure: secure, version: version, target: target, url: parsed.String(), topic: topic, qos: qos, retain: retain,
		clientID: clientID, cleanStart: cleanStart, keepAlive: keepAlive, subscriptions: subscriptions,
		username: auth.username, password: auth.password, userProperties: properties,
		willTopic: values.Get("will_topic"), willPayload: []byte(values.Get("will_payload")), willQoS: willQoS, willRetain: willRetain,
	}, nil
}

func parseMQTTQoS(value string, fallback byte) (byte, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	qos, err := strconv.Atoi(value)
	if err != nil || qos < 0 || qos > 2 {
		return 0, fmt.Errorf("MQTT QoS must be 0, 1, or 2")
	}
	return byte(qos), nil
}

func parseMQTTBool(value string, fallback bool, name string) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("MQTT %s must be true or false", name)
	}
	return parsed, nil
}

func parseMQTTSubscription(value string, fallbackQoS byte) (mqttSubscription, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return mqttSubscription{}, fmt.Errorf("MQTT subscription topic cannot be empty")
	}
	topic := value
	qos := fallbackQoS
	if separator := strings.LastIndexByte(value, ':'); separator > 0 {
		candidate := value[separator+1:]
		if candidate == "0" || candidate == "1" || candidate == "2" {
			topic = value[:separator]
			qos = candidate[0] - '0'
		}
	}
	return mqttSubscription{topic: topic, qos: qos}, nil
}

func mqttTLSConfig(base *http.Client, settings requestSettings, config mqttConfig) (*tls.Config, error) {
	client, err := configuredClient(base, settings)
	if err != nil {
		return nil, err
	}
	if !config.secure {
		return nil, nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: strings.Trim(config.target, "[]")}
	if host, _, err := net.SplitHostPort(config.target); err == nil {
		tlsConfig.ServerName = host
	}
	if transport, ok := client.Transport.(*http.Transport); ok && transport.TLSClientConfig != nil {
		tlsConfig = transport.TLSClientConfig.Clone()
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = strings.Trim(config.target, "[]")
			if host, _, err := net.SplitHostPort(config.target); err == nil {
				tlsConfig.ServerName = host
			}
		}
	}
	return tlsConfig, nil
}

func dialMQTT(ctx context.Context, config mqttConfig, tlsConfig *tls.Config) (net.Conn, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", config.target)
	if err != nil {
		return nil, err
	}
	if tlsConfig == nil {
		return connection, nil
	}
	tlsConnection := tls.Client(connection, tlsConfig)
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return tlsConnection, nil
}

type mqttV3Transport struct{ client mqttv3.Client }

func connectMQTTV3(ctx context.Context, config mqttConfig, tlsConfig *tls.Config, emit func(mqttEventMsg)) (mqttTransport, error) {
	connection, err := dialMQTT(ctx, config, tlsConfig)
	if err != nil {
		return nil, err
	}
	options := mqttv3.NewClientOptions().AddBroker("tcp://" + config.target).
		SetClientID(config.clientID).SetCleanSession(config.cleanStart).SetAutoReconnect(false).
		SetKeepAlive(config.keepAlive).SetConnectTimeout(config.keepAlive).SetOrderMatters(false)
	options.SetCustomOpenConnectionFn(func(*url.URL, mqttv3.ClientOptions) (net.Conn, error) { return connection, nil })
	if config.username != "" || config.password != "" {
		options.SetUsername(config.username).SetPassword(config.password)
	}
	if config.willTopic != "" {
		options.SetBinaryWill(config.willTopic, config.willPayload, config.willQoS, config.willRetain)
	}
	options.SetDefaultPublishHandler(func(_ mqttv3.Client, message mqttv3.Message) {
		emit(mqttEventMsg{topic: message.Topic(), payload: append([]byte(nil), message.Payload()...), qos: message.Qos(), retained: message.Retained()})
	})
	options.SetConnectionLostHandler(func(_ mqttv3.Client, err error) { emit(mqttEventMsg{err: err}) })
	client := mqttv3.NewClient(options)
	token := client.Connect()
	select {
	case <-ctx.Done():
		client.Disconnect(0)
		return nil, ctx.Err()
	case <-token.Done():
		if err := token.Error(); err != nil {
			client.Disconnect(0)
			return nil, err
		}
	}
	if len(config.subscriptions) > 0 {
		filters := make(map[string]byte, len(config.subscriptions))
		for _, subscription := range config.subscriptions {
			filters[subscription.topic] = subscription.qos
		}
		token = client.SubscribeMultiple(filters, nil)
		select {
		case <-ctx.Done():
			client.Disconnect(0)
			return nil, ctx.Err()
		case <-token.Done():
			if err := token.Error(); err != nil {
				client.Disconnect(0)
				return nil, fmt.Errorf("subscribe: %w", err)
			}
		}
	}
	return &mqttV3Transport{client: client}, nil
}

func (transport *mqttV3Transport) publish(ctx context.Context, topic string, payload []byte, qos byte, retain bool, _ []headerEntry) error {
	token := transport.client.Publish(topic, qos, retain, payload)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
		return token.Error()
	}
}

func (transport *mqttV3Transport) disconnect() { transport.client.Disconnect(250) }

type mqttV5Transport struct{ client *mqttv5.Client }

func connectMQTTV5(ctx context.Context, config mqttConfig, tlsConfig *tls.Config, emit func(mqttEventMsg)) (mqttTransport, error) {
	connection, err := dialMQTT(ctx, config, tlsConfig)
	if err != nil {
		return nil, err
	}
	clientErrors := make(chan error, 1)
	client := mqttv5.NewClient(mqttv5.ClientConfig{
		ClientID: config.clientID, Conn: packets.NewThreadSafeConn(connection), PacketTimeout: config.keepAlive,
		OnPublishReceived: []func(mqttv5.PublishReceived) (bool, error){func(received mqttv5.PublishReceived) (bool, error) {
			emit(mqttEventMsg{topic: received.Packet.Topic, payload: append([]byte(nil), received.Packet.Payload...), qos: received.Packet.QoS, retained: received.Packet.Retain})
			return true, nil
		}},
		OnClientError: func(err error) {
			select {
			case clientErrors <- err:
			default:
			}
			emit(mqttEventMsg{err: err})
		},
	})
	properties := &mqttv5.ConnectProperties{}
	for _, property := range config.userProperties {
		properties.User.Add(property.key, property.value)
	}
	connect := &mqttv5.Connect{
		ClientID: config.clientID, CleanStart: config.cleanStart, KeepAlive: uint16(config.keepAlive / time.Second), Properties: properties,
		Username: config.username, Password: []byte(config.password), UsernameFlag: config.username != "", PasswordFlag: config.password != "",
	}
	if config.willTopic != "" {
		connect.WillMessage = &mqttv5.WillMessage{Topic: config.willTopic, Payload: config.willPayload, QoS: config.willQoS, Retain: config.willRetain}
		connect.WillProperties = &mqttv5.WillProperties{}
	}
	connack, err := client.Connect(ctx, connect)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	if connack.ReasonCode != 0 {
		_ = client.Disconnect(&mqttv5.Disconnect{ReasonCode: 0})
		return nil, fmt.Errorf("broker rejected connection with reason code 0x%02x", connack.ReasonCode)
	}
	if len(config.subscriptions) > 0 {
		subscribe := &mqttv5.Subscribe{}
		for _, subscription := range config.subscriptions {
			subscribe.Subscriptions = append(subscribe.Subscriptions, mqttv5.SubscribeOptions{Topic: subscription.topic, QoS: subscription.qos})
		}
		response, err := client.Subscribe(ctx, subscribe)
		if err != nil {
			_ = client.Disconnect(&mqttv5.Disconnect{ReasonCode: 0})
			select {
			case clientErr := <-clientErrors:
				return nil, fmt.Errorf("subscribe: %w (client: %v)", err, clientErr)
			default:
			}
			return nil, fmt.Errorf("subscribe: %w", err)
		}
		for _, reason := range response.Reasons {
			if reason >= 0x80 {
				_ = client.Disconnect(&mqttv5.Disconnect{ReasonCode: 0})
				return nil, fmt.Errorf("broker rejected subscription with reason code 0x%02x", reason)
			}
		}
	}
	return &mqttV5Transport{client: client}, nil
}

func (transport *mqttV5Transport) publish(ctx context.Context, topic string, payload []byte, qos byte, retain bool, properties []headerEntry) error {
	publishProperties := &mqttv5.PublishProperties{}
	for _, property := range properties {
		publishProperties.User.Add(property.key, property.value)
	}
	_, err := transport.client.Publish(ctx, &mqttv5.Publish{Topic: topic, Payload: payload, QoS: qos, Retain: retain, Properties: publishProperties})
	return err
}

func (transport *mqttV5Transport) disconnect() {
	_ = transport.client.Disconnect(&mqttv5.Disconnect{ReasonCode: 0})
}

func waitMQTTEvent(session *mqttSession) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-session.events
		if !ok {
			return nil
		}
		event.events = session.events
		event.session = session
		return event
	}
}

func sendMQTTMessage(session *mqttSession, payload []byte) tea.Cmd {
	return func() tea.Msg {
		if session.config.topic == "" {
			return mqttSentMsg{requestID: session.requestID, err: fmt.Errorf("MQTT URL path must contain a publish topic")}
		}
		err := session.transport.publish(session.context, session.config.topic, payload, session.config.qos, session.config.retain, session.config.userProperties)
		return mqttSentMsg{requestID: session.requestID, topic: session.config.topic, payload: payload, qos: session.config.qos, retained: session.config.retain, err: err}
	}
}

func terminateMQTTSession(session *mqttSession) {
	if session == nil {
		return
	}
	session.closeOnce.Do(func() {
		session.transport.disconnect()
		session.cancel()
	})
}

func disconnectMQTTSession(session *mqttSession) tea.Cmd {
	return func() tea.Msg {
		terminateMQTTSession(session)
		return mqttEventMsg{requestID: session.requestID, err: net.ErrClosed, session: session}
	}
}

func mqttConnectionSummary(config mqttConfig) string {
	lines := []string{"Protocol: MQTT " + config.version, "Client-ID: " + config.clientID, fmt.Sprintf("Clean start: %t", config.cleanStart)}
	for _, subscription := range config.subscriptions {
		lines = append(lines, fmt.Sprintf("Subscription: %s (QoS %d)", subscription.topic, subscription.qos))
	}
	return strings.Join(lines, "\n")
}

func formatMQTTTranscript(direction, topic string, qos byte, retained bool, payload []byte) string {
	kind := "TEXT"
	content := string(payload)
	if !mqttTextPayload(payload) {
		kind = "BINARY"
		content = base64.StdEncoding.EncodeToString(payload)
	}
	return fmt.Sprintf("%s PUBLISH topic=%q qos=%d retain=%t %s %s\n%s\n", direction, topic, qos, retained, kind, formatByteCount(len(payload)), content)
}

func mqttTextPayload(payload []byte) bool {
	if !utf8.Valid(payload) {
		return false
	}
	for _, char := range string(payload) {
		if unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t' {
			return false
		}
	}
	return true
}

func appendMQTTTranscript(existing, entry string) (string, error) {
	if len(existing)+len(entry) > maxResponseBody {
		return existing, fmt.Errorf("MQTT transcript exceeds the %s display limit", formatByteCount(maxResponseBody))
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return existing + entry, nil
}

func (m *model) appendMQTTEntry(requestID uuid.UUID, entry string) error {
	base := ""
	for index := range m.history {
		if m.history[index].requestID == requestID {
			base = m.history[index].responseRaw
			break
		}
	}
	if requestID == m.requestId {
		base = m.responseRaw
	}
	transcript, err := appendMQTTTranscript(base, entry)
	if err != nil {
		return err
	}
	display := sanitizeTerminalText(transcript)
	for index := range m.history {
		if m.history[index].requestID == requestID {
			m.history[index].responseBody = display
			m.history[index].responseRaw = transcript
			m.history[index].responseRawAvailable = true
			break
		}
	}
	if requestID == m.requestId {
		wasAtBottom := m.responseModel.AtBottom()
		m.response = display
		m.responseRaw = transcript
		m.responseRawAvailable = true
		m.responseModel.SetContent(display)
		if wasAtBottom {
			m.responseModel.GotoBottom()
		}
	}
	return nil
}

func isNormalMQTTClose(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled)
}

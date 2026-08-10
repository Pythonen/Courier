package tui

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
)

func TestMQTTConfigMapsRequestControls(t *testing.T) {
	m := NewModel()
	m.urlInput.SetValue("mqtts://broker.example/out%2Fdevices?version=4&qos=2&retain=true&clean_start=false&keep_alive=45s&client_id=courier-test&subscribe=in/one:1&subscribe=in/two&will_topic=offline&will_payload=gone&will_qos=1&will_retain=true")
	m.paramsInput.SetEntries([]headerEntry{{key: "subscribe", value: "extra:0"}})
	m.headersInput.SetEntries([]headerEntry{{key: "trace-id", value: "abc"}})
	m.authInput.SetConfig(authConfig{typeID: authBasic, username: "user", password: "pass"})

	config, err := m.mqttConfig(newVariableResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.version != "3.1.1" || !config.secure || config.target != "broker.example:8883" || config.topic != "out/devices" {
		t.Fatalf("endpoint mapping = %#v", config)
	}
	if config.qos != 2 || !config.retain || config.cleanStart || config.keepAlive != 45*time.Second || config.clientID != "courier-test" {
		t.Fatalf("connection controls = %#v", config)
	}
	if config.username != "user" || config.password != "pass" || len(config.userProperties) != 1 || len(config.subscriptions) != 3 {
		t.Fatalf("auth/properties/subscriptions = %#v", config)
	}
	if config.willTopic != "offline" || string(config.willPayload) != "gone" || config.willQoS != 1 || !config.willRetain {
		t.Fatalf("last will = %#v", config)
	}
}

func TestMQTTNativeSessions(t *testing.T) {
	for _, version := range []string{"3.1.1", "5.0"} {
		t.Run(version, func(t *testing.T) {
			address, published, closeBroker := startTestMQTTBroker(t, version)
			defer closeBroker()

			m := NewModel()
			m.settings.config.timeout = 2 * time.Second
			m.urlInput.SetValue(fmt.Sprintf("mqtt://%s/out?version=%s&client_id=courier-test&subscribe=in:0", address, version))
			connect := m.beginMQTTConnection()
			connected, ok := connect().(mqttConnectedMsg)
			if !ok || connected.err != nil || connected.session == nil {
				t.Fatalf("connect error = %v; message = %#v", connected.err, connected)
			}
			defer terminateMQTTSession(connected.session)
			select {
			case <-connected.session.context.Done():
				t.Fatal("session context was cancelled when the connection timeout ended")
			default:
			}

			event := waitMQTTEvent(connected.session)().(mqttEventMsg)
			if event.err != nil || event.topic != "in" || string(event.payload) != "from-broker" {
				t.Fatalf("incoming publish = %#v", event)
			}
			sent := sendMQTTMessage(connected.session, []byte("from-client"))().(mqttSentMsg)
			if sent.err != nil {
				t.Fatal(sent.err)
			}
			select {
			case got := <-published:
				if got != "out:from-client" {
					t.Fatalf("published = %q", got)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("broker did not receive publish")
			}
		})
	}
}

func TestMQTTRejectsUnsupportedAuthAndControls(t *testing.T) {
	m := NewModel()
	m.urlInput.SetValue("mqtt://localhost/topic?qos=9")
	if _, err := m.mqttConfig(newVariableResolver(nil)); err == nil || !strings.Contains(err.Error(), "QoS") {
		t.Fatalf("invalid QoS error = %v", err)
	}
	m.urlInput.SetValue("mqtt://localhost/topic")
	m.authInput.SetConfig(authConfig{typeID: authBearer, bearerToken: "token"})
	if _, err := m.mqttConfig(newVariableResolver(nil)); err == nil || !strings.Contains(err.Error(), "Basic Auth") {
		t.Fatalf("unsupported auth error = %v", err)
	}
}

func TestWaitMQTTEventStopsWhenSessionIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := &mqttSession{
		requestID: uuid.New(),
		events:    make(chan mqttEventMsg),
		context:   ctx,
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- waitMQTTEvent(session)() }()
	cancel()

	select {
	case message := <-result:
		event, ok := message.(mqttEventMsg)
		if !ok || !errors.Is(event.err, context.Canceled) || event.session != session {
			t.Fatalf("cancelled wait result = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("MQTT event wait remained blocked after session cancellation")
	}
}

func startTestMQTTBroker(t *testing.T, version string) (string, <-chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	published := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		reader := bufio.NewReader(connection)
		packetType, connectBody, readErr := readMQTTPacket(reader)
		if readErr != nil || packetType>>4 != 1 || len(connectBody) < 7 {
			t.Errorf("broker read CONNECT: type 0x%x, length %d, error %v", packetType, len(connectBody), readErr)
			return
		}
		level := connectBody[6]
		if (version == "5.0" && level != 5) || (version == "3.1.1" && level != 4) {
			t.Errorf("broker protocol level = %d for version %s", level, version)
			return
		}
		if level == 5 {
			_, _ = connection.Write([]byte{0x20, 0x03, 0x00, 0x00, 0x00})
		} else {
			_, _ = connection.Write([]byte{0x20, 0x02, 0x00, 0x00})
		}
		var subscribeBody []byte
		for {
			packetType, subscribeBody, readErr = readMQTTPacket(reader)
			if readErr != nil {
				t.Errorf("broker read SUBSCRIBE: %v", readErr)
				return
			}
			if packetType == 0xc0 {
				_, _ = connection.Write([]byte{0xd0, 0x00})
				continue
			}
			if packetType != 0x82 || len(subscribeBody) < 2 {
				t.Errorf("broker expected SUBSCRIBE, got type 0x%x with length %d", packetType, len(subscribeBody))
				return
			}
			break
		}
		packetID := subscribeBody[:2]
		if level == 5 {
			_, _ = connection.Write([]byte{0x90, 0x04, packetID[0], packetID[1], 0x00, 0x00})
		} else {
			_, _ = connection.Write([]byte{0x90, 0x03, packetID[0], packetID[1], 0x00})
		}
		body := appendMQTTString(nil, "in")
		if level == 5 {
			body = append(body, 0)
		}
		body = append(body, "from-broker"...)
		_ = writeMQTTPacket(connection, 0x30, body)
		for {
			packetType, body, readErr = readMQTTPacket(reader)
			if readErr != nil {
				return
			}
			if packetType == 0xc0 {
				_, _ = connection.Write([]byte{0xd0, 0x00})
				continue
			}
			if packetType>>4 != 3 || len(body) < 2 {
				continue
			}
			topicLength := int(binary.BigEndian.Uint16(body[:2]))
			if len(body) < 2+topicLength {
				return
			}
			topic := string(body[2 : 2+topicLength])
			payloadOffset := 2 + topicLength
			if level == 5 {
				propertiesLength, bytesRead, decodeErr := decodeMQTTLength(body[payloadOffset:])
				if decodeErr != nil || len(body) < payloadOffset+bytesRead+propertiesLength {
					return
				}
				payloadOffset += bytesRead + propertiesLength
			}
			select {
			case published <- topic + ":" + string(body[payloadOffset:]):
			case <-ctx.Done():
			}
			return
		}
	}()
	return listener.Addr().String(), published, func() {
		cancel()
		_ = listener.Close()
	}
}

func readMQTTPacket(reader *bufio.Reader) (byte, []byte, error) {
	header, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	remaining, _, err := readMQTTLength(reader)
	if err != nil {
		return 0, nil, err
	}
	body := make([]byte, remaining)
	_, err = io.ReadFull(reader, body)
	return header, body, err
}

func readMQTTLength(reader *bufio.Reader) (int, int, error) {
	value, multiplier := 0, 1
	for bytesRead := 1; bytesRead <= 4; bytesRead++ {
		encoded, err := reader.ReadByte()
		if err != nil {
			return 0, 0, err
		}
		value += int(encoded&127) * multiplier
		if encoded&128 == 0 {
			return value, bytesRead, nil
		}
		multiplier *= 128
	}
	return 0, 0, fmt.Errorf("malformed MQTT remaining length")
}

func decodeMQTTLength(value []byte) (int, int, error) {
	return readMQTTLength(bufio.NewReader(strings.NewReader(string(value))))
}

func writeMQTTPacket(writer io.Writer, header byte, body []byte) error {
	packet := []byte{header}
	remaining := len(body)
	for {
		encoded := byte(remaining % 128)
		remaining /= 128
		if remaining > 0 {
			encoded |= 128
		}
		packet = append(packet, encoded)
		if remaining == 0 {
			break
		}
	}
	packet = append(packet, body...)
	_, err := writer.Write(packet)
	return err
}

func appendMQTTString(target []byte, value string) []byte {
	target = binary.BigEndian.AppendUint16(target, uint16(len(value)))
	return append(target, value...)
}

package tui

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	grpc_testing "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
)

type courierGRPCTestServer struct {
	grpc_testing.UnimplementedTestServiceServer
	metadata chan metadata.MD
}

func (s *courierGRPCTestServer) EmptyCall(ctx context.Context, _ *grpc_testing.Empty) (*grpc_testing.Empty, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.metadata <- md
	_ = grpc.SetHeader(ctx, metadata.Pairs("x-server", "courier"))
	_ = grpc.SetTrailer(ctx, metadata.Pairs("x-finished", "yes"))
	return &grpc_testing.Empty{}, nil
}

func (s *courierGRPCTestServer) StreamingOutputCall(request *grpc_testing.StreamingOutputCallRequest, stream grpc.ServerStreamingServer[grpc_testing.StreamingOutputCallResponse]) error {
	if string(request.GetPayload().GetBody()) == "block" {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	if err := stream.SetHeader(metadata.Pairs("x-stream", "started")); err != nil {
		return err
	}
	for _, value := range []string{"one", "two"} {
		if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{Payload: &grpc_testing.Payload{Body: []byte(value)}}); err != nil {
			return err
		}
	}
	stream.SetTrailer(metadata.Pairs("x-stream-finished", "yes"))
	return nil
}

func (s *courierGRPCTestServer) StreamingInputCall(stream grpc.ClientStreamingServer[grpc_testing.StreamingInputCallRequest, grpc_testing.StreamingInputCallResponse]) error {
	var size int32
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&grpc_testing.StreamingInputCallResponse{AggregatedPayloadSize: size})
		}
		if err != nil {
			return err
		}
		size += int32(len(request.GetPayload().GetBody())) //nolint:gosec // Test payloads are tiny.
	}
}

func (s *courierGRPCTestServer) FullDuplexCall(stream grpc.BidiStreamingServer[grpc_testing.StreamingOutputCallRequest, grpc_testing.StreamingOutputCallResponse]) error {
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{Payload: request.GetPayload()}); err != nil {
			return err
		}
	}
}

func startGRPCTestServer(t *testing.T) (string, *courierGRPCTestServer) {
	return startGRPCTestServerWithReflection(t, true)
}

func startGRPCTestServerWithReflection(t *testing.T, enableReflection bool) (string, *courierGRPCTestServer) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &courierGRPCTestServer{metadata: make(chan metadata.MD, 1)}
	server := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(server, service)
	if enableReflection {
		reflection.Register(server)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String(), service
}

func TestGRPCUsesLocalProtoWhenServerReflectionIsDisabled(t *testing.T) {
	target, service := startGRPCTestServerWithReflection(t, false)
	protoPath := filepath.Join(t.TempDir(), "service.proto")
	definition := `syntax = "proto3";
package grpc.testing;
message Empty {}
service TestService { rpc EmptyCall(Empty) returns (Empty); }
`
	if err := os.WriteFile(protoPath, []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.urlInput.SetValue("grpc://" + target + "/grpc.testing.TestService/EmptyCall")
	m.paramsInput.SetEntries([]headerEntry{{key: "proto", value: "{{schema}}"}})
	m.variablesInput.SetEntries([]headerEntry{{key: "schema", value: protoPath}})
	m.bodyInput.SetValue(`{}`)

	response := m.DoRequest()().(responseMsg)
	if response.statusCode != 200 || !strings.Contains(response.responseMeta, "gRPC OK") {
		t.Fatalf("local-proto gRPC response = %#v", response)
	}
	select {
	case <-service.metadata:
	case <-time.After(time.Second):
		t.Fatal("local-proto request did not reach reflection-disabled server")
	}
}

func TestImportProtoSupportsImportPathsAndStreamingExamples(t *testing.T) {
	root := t.TempDir()
	imports := t.TempDir()
	if err := os.MkdirAll(filepath.Join(imports, "types"), 0o700); err != nil {
		t.Fatal(err)
	}
	common := `syntax = "proto3"; package courier.types; message Payload { string value = 1; }`
	if err := os.WriteFile(filepath.Join(imports, "types", "payload.proto"), []byte(common), 0o600); err != nil {
		t.Fatal(err)
	}
	definition := `syntax = "proto3";
package courier.test;
import "types/payload.proto";
service Example {
  rpc Unary(courier.types.Payload) returns (courier.types.Payload);
  rpc Upload(stream courier.types.Payload) returns (courier.types.Payload);
}`
	protoPath := filepath.Join(root, "example.proto")
	if err := os.WriteFile(protoPath, []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	count, err := m.ImportProto(protoPath, "grpcs://api.example.test:7443", []string{imports})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(m.savedRequests) != 2 {
		t.Fatalf("imported protobuf requests = %d, %#v", count, m.savedRequests)
	}
	for _, request := range m.savedRequests {
		parsed, parseErr := parseGRPCEndpoint(request.url)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if !parsed.secure || parsed.target != "api.example.test:7443" || parsed.protoFile != protoPath || len(parsed.protoPaths) != 1 || parsed.protoPaths[0] != imports {
			t.Fatalf("imported protobuf endpoint = %#v", parsed)
		}
		if request.method != "GRPC" || len(request.tests) != 1 || request.tests[0].value != "200" {
			t.Fatalf("imported protobuf request = %#v", request)
		}
		if parsed.method == "Upload" && !strings.HasPrefix(strings.TrimSpace(request.body.raw), "[") {
			t.Fatalf("client-streaming example is not an array: %q", request.body.raw)
		}
		if parsed.method == "Unary" && !strings.Contains(request.body.raw, `"value"`) {
			t.Fatalf("unary example does not include fields: %q", request.body.raw)
		}
	}
}

func TestImportProtoFailureIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.proto")
	if err := os.WriteFile(path, []byte(`syntax = "proto3"; service Broken { rpc Missing(`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.savedRequests = []savedRequest{{name: "existing", url: "https://example.test"}}
	if _, err := m.ImportProto(path, "grpc://localhost:50051", nil); err == nil {
		t.Fatal("invalid protobuf schema was accepted")
	}
	if len(m.savedRequests) != 1 || m.savedRequests[0].name != "existing" {
		t.Fatalf("failed protobuf import changed collection: %#v", m.savedRequests)
	}
}

func TestGRPCUnaryUsesReflectionJSONMetadataAuthAndTrailers(t *testing.T) {
	target, service := startGRPCTestServer(t)
	m := NewModel()
	m.urlInput.SetValue("grpc://" + target + "/grpc.testing.TestService/EmptyCall")
	m.bodyInput.SetValue(`{}`)
	m.headersInput.SetEntries([]headerEntry{{key: "X-Request", value: "{{request_value}}"}})
	m.cookiesInput.SetEntries([]headerEntry{{key: "session", value: "abc"}})
	m.variablesInput.SetEntries([]headerEntry{{key: "request_value", value: "resolved"}})
	m.authInput.SetConfig(authConfig{typeID: authBearer, bearerToken: "secret"})
	m.testsInput.SetEntries([]headerEntry{
		{key: "status", value: "200"},
		{key: "header.X-Server", value: "courier"},
		{key: "header.Trailer-X-Finished", value: "yes"},
	})

	response := m.DoRequest()().(responseMsg)
	if response.statusCode != 200 || response.responseRaw != "{}" || !strings.Contains(response.responseMeta, "gRPC OK") {
		t.Fatalf("gRPC unary response = %#v", response)
	}
	for _, result := range response.assertionResults {
		if !result.Passed {
			t.Fatalf("gRPC assertion failed: %#v", result)
		}
	}
	md := <-service.metadata
	if got := md.Get("x-request"); len(got) != 1 || got[0] != "resolved" {
		t.Fatalf("request metadata = %#v", md)
	}
	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer secret" {
		t.Fatalf("authorization metadata = %#v", md)
	}
	if got := md.Get("cookie"); len(got) != 1 || got[0] != "session=abc" {
		t.Fatalf("cookie metadata = %#v", md)
	}
}

func TestGRPCMetadataSupportsEveryOAuthTokenGrant(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		grant := request.Form.Get("grant_type")
		if grant == "" {
			http.Error(response, "missing grant", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"access_token":"` + grant + `-token","token_type":"Bearer"}`))
	}))
	defer tokenServer.Close()

	tests := []struct {
		name string
		auth authConfig
		want string
	}{
		{
			name: "client credentials",
			auth: authConfig{typeID: authOAuth2ClientCredentials, oauthTokenURL: tokenServer.URL, oauthClientID: "client"},
			want: "Bearer client_credentials-token",
		},
		{
			name: "password",
			auth: authConfig{typeID: authOAuth2Password, oauthTokenURL: tokenServer.URL, username: "user", password: "password"},
			want: "Bearer password-token",
		},
		{
			name: "refresh token",
			auth: authConfig{typeID: authOAuth2RefreshToken, oauthTokenURL: tokenServer.URL, oauthRefreshToken: "refresh"},
			want: "Bearer refresh_token-token",
		},
		{
			name: "authorization code",
			auth: authConfig{typeID: authOAuth2AuthorizationCode, oauthAccessToken: "cached-token", oauthTokenType: "Bearer"},
			want: "Bearer cached-token",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := NewModel()
			m.authInput.SetConfig(test.auth)
			md, err := m.grpcMetadata(context.Background(), newVariableResolver(nil), m.settings.config)
			if err != nil {
				t.Fatal(err)
			}
			if got := md.Get("authorization"); len(got) != 1 || got[0] != test.want {
				t.Fatalf("authorization metadata = %#v, want %q", got, test.want)
			}
		})
	}
}

func TestGRPCMetadataRejectsUnsupportedAuthentication(t *testing.T) {
	for _, authType := range []authType{authDigest, authAWSSignatureV4, authHawk, authNTLM, authOAuth1} {
		m := NewModel()
		m.authInput.SetConfig(authConfig{typeID: authType})
		if _, err := m.grpcMetadata(context.Background(), newVariableResolver(nil), m.settings.config); err == nil {
			t.Fatalf("gRPC authentication type %d was silently accepted", authType)
		}
	}
}

func TestGRPCUnaryUsesSOCKS5HProxy(t *testing.T) {
	target, _ := startGRPCTestServer(t)
	proxyAddress, observed, accepted, closeProxy := startTestSOCKSProxy(t, 5, "", "")
	defer closeProxy()
	m := NewModel()
	m.settings.SetConfig(requestSettings{
		followRedirects: true,
		timeout:         3 * time.Second,
		proxyURL:        "socks5h://" + proxyAddress,
	})
	m.urlInput.SetValue("grpc://" + target + "/grpc.testing.TestService/EmptyCall")
	m.bodyInput.SetValue(`{}`)

	response := m.DoRequest()().(responseMsg)
	if response.statusCode != 200 || !strings.Contains(response.responseMeta, "gRPC OK") {
		t.Fatalf("proxied gRPC response = %#v", response)
	}
	if got := <-observed; got != target {
		t.Fatalf("SOCKS5H gRPC target = %q, want %q", got, target)
	}
	if accepted.Load() == 0 {
		t.Fatal("SOCKS5H proxy accepted no gRPC connection")
	}
}

func TestGRPCServerStreamIsConsumedIncrementally(t *testing.T) {
	target, _ := startGRPCTestServer(t)
	m := NewModel()
	m.urlInput.SetValue("grpc://" + target + "/grpc.testing.TestService/StreamingOutputCall")
	m.bodyInput.SetValue(`{}`)
	m.testsInput.SetEntries([]headerEntry{{key: "status", value: "200"}, {key: "body.contains", value: "dHdv"}})

	initial := m.DoRequest()().(responseMsg)
	if initial.stream == nil || !strings.Contains(initial.responseMeta, "streaming") {
		t.Fatalf("initial gRPC stream = %#v", initial)
	}
	final := consumeResponseStream(initial)
	if final.statusCode != 200 || strings.Count(final.responseRaw, `"payload"`) != 2 || !strings.Contains(final.responseHeaders, "X-Stream-Finished: yes") {
		t.Fatalf("final gRPC stream = %#v", final)
	}
	for _, result := range final.assertionResults {
		if !result.Passed {
			t.Fatalf("gRPC stream assertion failed: %#v", result)
		}
	}
}

func TestCollectionRunnerExecutesGRPCRequests(t *testing.T) {
	target, _ := startGRPCTestServer(t)
	m := NewModel()
	m.savedRequests = []savedRequest{{
		name: "gRPC / Empty", method: "GRPC", url: "grpc://" + target + "/grpc.testing.TestService/EmptyCall",
		body:  bodyConfig{mode: bodyRaw, rawType: rawJSON, raw: `{}`},
		tests: []headerEntry{{key: "status", value: "200"}},
	}}
	report, err := m.RunCollection(context.Background(), RunOptions{Selector: "all", Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Passed != 1 || report.Results[0].Method != "GRPC" || report.Results[0].Status != 200 {
		t.Fatalf("gRPC runner report = %#v", report)
	}
}

func TestGRPCClientAndBidirectionalStreamsUseJSONArrayInput(t *testing.T) {
	target, _ := startGRPCTestServer(t)
	for _, test := range []struct {
		name, method, body, expected string
	}{
		{name: "client", method: "StreamingInputCall", body: `[{"payload":{"body":"b25l"}},{"payload":{"body":"dHdv"}}]`, expected: `"aggregatedPayloadSize"`},
		{name: "bidirectional", method: "FullDuplexCall", body: `[{"payload":{"body":"b25l"}},{"payload":{"body":"dHdv"}}]`, expected: `"dHdv"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := NewModel()
			m.urlInput.SetValue("grpc://" + target + "/grpc.testing.TestService/" + test.method)
			m.bodyInput.SetValue(test.body)
			initial := m.DoRequest()().(responseMsg)
			final := consumeResponseStream(initial)
			if final.statusCode != 200 || !strings.Contains(final.responseRaw, test.expected) {
				t.Fatalf("%s response = %#v", test.method, final)
			}
		})
	}
}

func TestGRPCClientStreamRequiresJSONArray(t *testing.T) {
	target, _ := startGRPCTestServer(t)
	m := NewModel()
	m.urlInput.SetValue("grpc://" + target + "/grpc.testing.TestService/StreamingInputCall")
	m.bodyInput.SetValue(`{"payload":{}}`)
	response := m.DoRequest()().(responseMsg)
	if response.statusCode != 0 || !strings.Contains(response.responseBody, "JSON array") {
		t.Fatalf("invalid client-streaming body response = %#v", response)
	}
}

func TestGRPCStreamCancellationTerminatesCall(t *testing.T) {
	target, _ := startGRPCTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	m := NewModel()
	m.requestContext = ctx
	m.urlInput.SetValue("grpc://" + target + "/grpc.testing.TestService/StreamingOutputCall")
	m.bodyInput.SetValue(`{"payload":{"body":"YmxvY2s="}}`)
	initial := m.DoRequest()().(responseMsg)
	cancel()
	final := consumeResponseStream(initial)
	if final.statusCode != 0 || !strings.Contains(final.responseMeta, "Canceled") {
		t.Fatalf("cancelled gRPC stream = %#v", final)
	}
}

func TestParseGRPCEndpointRejectsIncompleteMethod(t *testing.T) {
	for _, value := range []string{"https://localhost/service/method", "grpc://localhost/service", "grpc:///service/method"} {
		if _, err := parseGRPCEndpoint(value); err == nil {
			t.Fatalf("parseGRPCEndpoint(%q) unexpectedly succeeded", value)
		}
	}
}

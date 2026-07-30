package tui

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uuid "github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type grpcEndpoint struct {
	secure     bool
	target     string
	service    protoreflect.FullName
	method     protoreflect.Name
	fullMethod string
	protoFile  string
	protoPaths []string
}

func isGRPCURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (strings.EqualFold(parsed.Scheme, "grpc") || strings.EqualFold(parsed.Scheme, "grpcs"))
}

func parseGRPCEndpoint(value string) (grpcEndpoint, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return grpcEndpoint{}, fmt.Errorf("parse gRPC URL: %w", err)
	}
	if parsed.Scheme != "grpc" && parsed.Scheme != "grpcs" {
		return grpcEndpoint{}, fmt.Errorf("gRPC URL must use grpc:// or grpcs://")
	}
	if parsed.Host == "" {
		return grpcEndpoint{}, fmt.Errorf("gRPC URL is missing a host")
	}
	path := strings.Trim(strings.TrimSpace(parsed.EscapedPath()), "/")
	path, err = url.PathUnescape(path)
	if err != nil {
		return grpcEndpoint{}, fmt.Errorf("decode gRPC method path: %w", err)
	}
	separator := strings.LastIndexByte(path, '/')
	if separator <= 0 || separator == len(path)-1 {
		return grpcEndpoint{}, fmt.Errorf("gRPC URL path must be /package.Service/Method")
	}
	service, method := path[:separator], path[separator+1:]
	query := parsed.Query()
	return grpcEndpoint{
		secure: parsed.Scheme == "grpcs", target: parsed.Host,
		service: protoreflect.FullName(service), method: protoreflect.Name(method),
		fullMethod: "/" + service + "/" + method, protoFile: strings.TrimSpace(query.Get("proto")),
		protoPaths: query["proto_path"],
	}, nil
}

func (m model) DoGRPCRequest() tea.Cmd {
	return func() tea.Msg {
		requestID := m.requestId
		assertions := m.testsInput.Entries()
		resolver := newVariableResolver(m.variablesInput.Entries())
		resolvedURL, err := grpcURLWithParams(resolver.Resolve(m.urlInput.Value()), m.paramsInput.Entries(), resolver)
		if err != nil {
			return grpcFailure(requestID, m.urlInput.Value(), assertions, err, 0)
		}
		endpoint, err := parseGRPCEndpoint(resolvedURL)
		if err != nil {
			return grpcFailure(requestID, resolvedURL, assertions, err, 0)
		}

		baseContext := m.requestContext
		if baseContext == nil {
			baseContext = context.Background()
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
		options, err := grpcDialOptions(m.client, endpoint, settings)
		if err != nil {
			return grpcFailure(requestID, resolvedURL, assertions, fmt.Errorf("configure gRPC transport: %w", err), 0)
		}
		conn, err := grpc.NewClient(endpoint.target, options...)
		if err != nil {
			return grpcFailure(requestID, resolvedURL, assertions, fmt.Errorf("create gRPC client: %w", err), 0)
		}

		started := time.Now()
		authContext, cancelAuth := grpcRequestContext(baseContext, settings.timeout)
		outgoing, err := m.grpcMetadata(authContext, resolver, settings)
		cancelAuth()
		if err != nil {
			_ = conn.Close()
			return grpcFailure(requestID, resolvedURL, assertions, err, time.Since(started))
		}
		var method protoreflect.MethodDescriptor
		var files *protoregistry.Files
		if endpoint.protoFile != "" {
			method, files, err = localProtoMethod(baseContext, endpoint)
		} else {
			reflectionContext, cancelReflection := grpcRequestContext(metadata.NewOutgoingContext(baseContext, outgoing), settings.timeout)
			method, files, err = reflectedMethod(reflectionContext, conn, endpoint)
			cancelReflection()
		}
		if err != nil {
			_ = conn.Close()
			return grpcFailure(requestID, resolvedURL, assertions, err, time.Since(started))
		}
		payload := strings.TrimSpace(resolver.Resolve(m.bodyInput.Value()))
		if payload == "" {
			payload = "{}"
		}
		types := dynamicpb.NewTypes(files)
		inputs, err := grpcInputMessages(payload, method, types)
		if err != nil {
			_ = conn.Close()
			return grpcFailure(requestID, resolvedURL, assertions, err, time.Since(started))
		}
		ctx, cancelCall := grpcRequestContext(metadata.NewOutgoingContext(baseContext, outgoing), settings.timeout)
		if method.IsStreamingClient() || method.IsStreamingServer() {
			return startGRPCStream(ctx, cancelCall, requestID, conn, endpoint, method, types, inputs, assertions, started)
		}
		defer cancelCall()
		defer conn.Close() //nolint:errcheck
		return invokeUnaryGRPC(ctx, requestID, conn, endpoint, method, types, inputs[0], assertions, started)
	}
}

func grpcRequestContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func grpcInputMessages(payload string, method protoreflect.MethodDescriptor, types *dynamicpb.Types) ([]*dynamicpb.Message, error) {
	rawMessages := []json.RawMessage{json.RawMessage(payload)}
	if method.IsStreamingClient() {
		if err := json.Unmarshal([]byte(payload), &rawMessages); err != nil {
			return nil, fmt.Errorf("decode client-streaming gRPC request: body must be a JSON array of messages: %w", err)
		}
		if len(rawMessages) == 0 {
			return nil, fmt.Errorf("client-streaming gRPC request requires at least one message")
		}
	}
	inputs := make([]*dynamicpb.Message, 0, len(rawMessages))
	for index, raw := range rawMessages {
		input := dynamicpb.NewMessage(method.Input())
		if err := (protojson.UnmarshalOptions{Resolver: types}).Unmarshal(raw, input); err != nil {
			if method.IsStreamingClient() {
				return nil, fmt.Errorf("decode gRPC request message %d: %w", index+1, err)
			}
			return nil, fmt.Errorf("decode gRPC request JSON: %w", err)
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func grpcDialOptions(base *http.Client, endpoint grpcEndpoint, settings requestSettings) ([]grpc.DialOption, error) {
	client, err := configuredClient(base, settings)
	if err != nil {
		return nil, err
	}
	var tlsConfig *tls.Config
	if transport, ok := client.Transport.(*http.Transport); ok && transport.TLSClientConfig != nil {
		tlsConfig = transport.TLSClientConfig.Clone()
	}
	options := make([]grpc.DialOption, 0, 2)
	if endpoint.secure {
		if tlsConfig == nil {
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		options = append(options, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if strings.TrimSpace(settings.proxyURL) != "" {
		proxyURL, err := url.Parse(settings.proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL: %w", err)
		}
		switch strings.ToLower(proxyURL.Scheme) {
		case "socks4", "socks4a", "socks5", "socks5h":
			transport, ok := client.Transport.(*http.Transport)
			if !ok || transport.DialContext == nil {
				return nil, fmt.Errorf("configured SOCKS proxy has no dialer")
			}
			options = append(options, grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
				return transport.DialContext(ctx, "tcp", address)
			}))
		default:
			proxyDial := httpConnectDialer(proxyURL)
			options = append(options, grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
				if proxyAddressBypassed(settings.proxyBypass, address) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", address)
				}
				return proxyDial(ctx, address)
			}))
		}
	}
	return options, nil
}

func httpConnectDialer(proxyURL *url.URL) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, address string) (net.Conn, error) {
		if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
			return nil, fmt.Errorf("gRPC proxy must use http:// or https://")
		}
		proxyAddress := proxyURL.Host
		if !strings.Contains(proxyAddress, ":") {
			if proxyURL.Scheme == "https" {
				proxyAddress += ":443"
			} else {
				proxyAddress += ":80"
			}
		}
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddress)
		if err != nil {
			return nil, err
		}
		if proxyURL.Scheme == "https" {
			tlsConnection := tls.Client(connection, &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
			if err := tlsConnection.HandshakeContext(ctx); err != nil {
				_ = connection.Close()
				return nil, err
			}
			connection = tlsConnection
		}
		request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username()+":"+password)))
		}
		if err := request.Write(connection); err != nil {
			_ = connection.Close()
			return nil, err
		}
		reader := bufio.NewReader(connection)
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			_ = connection.Close()
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_ = response.Body.Close()
			_ = connection.Close()
			return nil, fmt.Errorf("proxy CONNECT returned %s", response.Status)
		}
		return &bufferedConn{Conn: connection, reader: reader}, nil
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(payload []byte) (int, error) { return c.reader.Read(payload) }

func (m model) grpcMetadata(ctx context.Context, resolver *variableResolver, settings requestSettings) (metadata.MD, error) {
	md := metadata.MD{}
	for _, header := range m.headersInput.Entries() {
		name := strings.ToLower(strings.TrimSpace(resolver.Resolve(header.key)))
		if name == "" || strings.HasPrefix(name, ":") || name == "content-type" || name == "te" {
			continue
		}
		md.Append(name, resolver.Resolve(header.value))
	}
	cookies := make([]string, 0, len(m.cookiesInput.Entries()))
	for _, cookie := range m.cookiesInput.Entries() {
		cookies = append(cookies, resolver.Resolve(cookie.key)+"="+resolver.Resolve(cookie.value))
	}
	if len(cookies) > 0 {
		md.Append("cookie", strings.Join(cookies, "; "))
	}
	auth := m.authInput.Config().resolved(resolver)
	switch auth.typeID {
	case authNone:
	case authBearer:
		md.Set("authorization", "Bearer "+auth.bearerToken)
	case authJWTBearer:
		if auth.jwtLocation != apiKeyHeader {
			return nil, fmt.Errorf("gRPC JWT auth must use header location")
		}
		token, err := generateJWT(auth)
		if err != nil {
			return nil, fmt.Errorf("generate gRPC JWT: %w", err)
		}
		prefix := strings.TrimSpace(auth.jwtPrefix)
		if prefix != "" {
			token = prefix + " " + token
		}
		md.Set("authorization", token)
	case authBasic:
		md.Set("authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth.username+":"+auth.password)))
	case authAPIKey:
		if auth.apiKeyLocation != apiKeyHeader {
			return nil, fmt.Errorf("gRPC API keys must use header location")
		}
		md.Set(strings.ToLower(auth.apiKeyName), auth.apiKeyValue)
	case authOAuth2ClientCredentials, authOAuth2AuthorizationCode:
		client, err := configuredClient(m.client, settings)
		if err != nil {
			return nil, fmt.Errorf("configure OAuth client: %w", err)
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://grpc.invalid", nil)
		if err := auth.authorize(ctx, client, request, nil); err != nil {
			return nil, fmt.Errorf("authorize gRPC request: %w", err)
		}
		md.Set("authorization", request.Header.Get("Authorization"))
	case authDigest, authAWSSignatureV4:
		return nil, fmt.Errorf("selected HTTP authentication mode is not supported by gRPC")
	}
	return md, nil
}

func reflectedMethod(ctx context.Context, conn *grpc.ClientConn, endpoint grpcEndpoint) (protoreflect.MethodDescriptor, *protoregistry.Files, error) {
	serialized, err := reflectedFilesV1(ctx, conn, string(endpoint.service))
	if err != nil {
		serialized, err = reflectedFilesV1Alpha(ctx, conn, string(endpoint.service))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s with server reflection: %w", endpoint.service, err)
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, data := range serialized {
		file := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(data, file); err != nil {
			return nil, nil, fmt.Errorf("decode reflected descriptor: %w", err)
		}
		set.File = append(set.File, file)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, nil, fmt.Errorf("build reflected descriptors: %w", err)
	}
	descriptor, err := files.FindDescriptorByName(endpoint.service)
	if err != nil {
		return nil, nil, fmt.Errorf("find reflected service %s: %w", endpoint.service, err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, nil, fmt.Errorf("reflected symbol %s is not a service", endpoint.service)
	}
	method := service.Methods().ByName(endpoint.method)
	if method == nil {
		return nil, nil, fmt.Errorf("service %s has no method %s", endpoint.service, endpoint.method)
	}
	return method, files, nil
}

func reflectedFilesV1(ctx context.Context, conn *grpc.ClientConn, symbol string) ([][]byte, error) {
	stream, err := reflectionv1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&reflectionv1.ServerReflectionRequest{MessageRequest: &reflectionv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol}}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if reflectedErr := response.GetErrorResponse(); reflectedErr != nil {
		return nil, fmt.Errorf("%s", reflectedErr.GetErrorMessage())
	}
	if response.GetFileDescriptorResponse() == nil {
		return nil, fmt.Errorf("reflection server returned no descriptors")
	}
	return response.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
}

// reflectedFilesV1Alpha keeps compatibility with servers that have not enabled the stable v1 reflection service.
func reflectedFilesV1Alpha(ctx context.Context, conn *grpc.ClientConn, symbol string) ([][]byte, error) {
	stream, err := reflectionv1alpha.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&reflectionv1alpha.ServerReflectionRequest{MessageRequest: &reflectionv1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol}}); err != nil { //nolint:staticcheck // v1alpha compatibility.
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if reflectedErr := response.GetErrorResponse(); reflectedErr != nil { //nolint:staticcheck // v1alpha compatibility.
		return nil, fmt.Errorf("%s", reflectedErr.GetErrorMessage()) //nolint:staticcheck // v1alpha compatibility.
	}
	if response.GetFileDescriptorResponse() == nil { //nolint:staticcheck // v1alpha compatibility.
		return nil, fmt.Errorf("reflection server returned no descriptors")
	}
	return response.GetFileDescriptorResponse().GetFileDescriptorProto(), nil //nolint:staticcheck // v1alpha compatibility.
}

func invokeUnaryGRPC(ctx context.Context, requestID uuid.UUID, conn *grpc.ClientConn, endpoint grpcEndpoint, method protoreflect.MethodDescriptor, types *dynamicpb.Types, input *dynamicpb.Message, assertions []headerEntry, started time.Time) responseMsg {
	output := dynamicpb.NewMessage(method.Output())
	var headers, trailers metadata.MD
	err := conn.Invoke(ctx, endpoint.fullMethod, input, output, grpc.Header(&headers), grpc.Trailer(&trailers))
	elapsed := time.Since(started)
	if err != nil {
		return grpcRPCFailure(requestID, endpoint, assertions, headers, trailers, err, elapsed)
	}
	body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", Resolver: types}).Marshal(output)
	if err != nil {
		return grpcFailure(requestID, grpcURL(endpoint), assertions, fmt.Errorf("encode gRPC response JSON: %w", err), elapsed)
	}
	return grpcSuccess(requestID, endpoint, assertions, headers, trailers, body, elapsed)
}

func startGRPCStream(ctx context.Context, cancelContext context.CancelFunc, requestID uuid.UUID, conn *grpc.ClientConn, endpoint grpcEndpoint, method protoreflect.MethodDescriptor, types *dynamicpb.Types, inputs []*dynamicpb.Message, assertions []headerEntry, started time.Time) responseMsg {
	updates := make(chan responseStreamMsg, 1)
	go func() {
		defer close(updates)
		defer cancelContext()
		defer conn.Close() //nolint:errcheck
		stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: method.IsStreamingServer(), ClientStreams: method.IsStreamingClient()}, endpoint.fullMethod)
		sendFinished := make(chan error, 1)
		if err == nil {
			go func() {
				for _, input := range inputs {
					if sendErr := stream.SendMsg(input); sendErr != nil {
						sendFinished <- sendErr
						return
					}
				}
				sendFinished <- stream.CloseSend()
			}()
		} else {
			sendFinished <- err
		}
		var headers metadata.MD
		if err == nil {
			headers, err = stream.Header()
		}
		var transcript []byte
		for err == nil {
			output := dynamicpb.NewMessage(method.Output())
			err = stream.RecvMsg(output)
			if err != nil {
				break
			}
			message, marshalErr := (protojson.MarshalOptions{Multiline: true, Indent: "  ", Resolver: types}).Marshal(output)
			if marshalErr != nil {
				err = marshalErr
				break
			}
			message = append(message, '\n')
			if len(transcript)+len(message) > maxResponseBody {
				err = fmt.Errorf("gRPC response exceeds the %s display limit", formatByteCount(maxResponseBody))
				break
			}
			transcript = append(transcript, message...)
			select {
			case updates <- responseStreamMsg{requestID: requestID, chunk: string(message)}:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			cancelContext()
		}
		if sendErr := <-sendFinished; sendErr != nil && errors.Is(err, io.EOF) {
			err = sendErr
		}
		elapsed := time.Since(started)
		var final responseMsg
		var trailers metadata.MD
		if stream != nil {
			trailers = stream.Trailer()
		}
		if errors.Is(err, io.EOF) {
			final = grpcSuccess(requestID, endpoint, assertions, headers, trailers, transcript, elapsed)
		} else {
			final = grpcRPCFailure(requestID, endpoint, assertions, headers, trailers, err, elapsed)
			final.responseRaw = string(transcript)
			final.responseRawAvailable = len(transcript) > 0
		}
		updates <- responseStreamMsg{requestID: requestID, final: &final}
	}()
	return responseMsg{requestID: requestID, responseMeta: "gRPC streaming…", finalURL: grpcURL(endpoint), responseRawAvailable: true, stream: updates}
}

func grpcSuccess(requestID uuid.UUID, endpoint grpcEndpoint, assertions []headerEntry, headers, trailers metadata.MD, body []byte, elapsed time.Duration) responseMsg {
	httpHeaders := grpcResponseHeaders(headers, trailers)
	results := evaluateAssertions(assertions, assertionResponse{status: http.StatusOK, headers: httpHeaders, body: body, duration: elapsed, size: len(body)})
	return responseMsg{
		requestID: requestID, responseBody: string(body), responseRaw: string(body), responseRawAvailable: true,
		responseHeaders: formatHeaders(httpHeaders), responseMeta: fmt.Sprintf("gRPC OK • %s • %s", elapsed.Round(time.Millisecond), formatByteCount(len(body))),
		statusCode: http.StatusOK, duration: elapsed, responseBytes: len(body), finalURL: grpcURL(endpoint),
		assertionResults: results, variableUpdates: successfulVariableUpdates(assertions, results),
	}
}

func grpcRPCFailure(requestID uuid.UUID, endpoint grpcEndpoint, assertions []headerEntry, headers, trailers metadata.MD, err error, elapsed time.Duration) responseMsg {
	if err == nil {
		err = fmt.Errorf("unknown gRPC error")
	}
	code := status.Code(err)
	if errors.Is(err, context.Canceled) {
		code = codes.Canceled
	}
	httpHeaders := grpcResponseHeaders(headers, trailers)
	body := []byte(fmt.Sprintf("gRPC %s: %v", code, err))
	return responseMsg{
		requestID: requestID, responseBody: string(body), responseHeaders: formatHeaders(httpHeaders),
		responseMeta: fmt.Sprintf("gRPC %s • %s", code, elapsed.Round(time.Millisecond)), duration: elapsed,
		responseBytes: len(body), finalURL: grpcURL(endpoint),
		assertionResults: unavailableAssertionResults(assertions, "gRPC call did not complete successfully"),
	}
}

func grpcFailure(requestID uuid.UUID, finalURL string, assertions []headerEntry, err error, elapsed time.Duration) responseMsg {
	meta := "gRPC request failed"
	if elapsed > 0 {
		meta += " • " + elapsed.Round(time.Millisecond).String()
	}
	if errors.Is(err, context.Canceled) {
		meta = "Cancelled • " + elapsed.Round(time.Millisecond).String()
	}
	return responseMsg{requestID: requestID, responseBody: "Error: " + err.Error(), responseMeta: meta, duration: elapsed, finalURL: finalURL, assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
}

func grpcResponseHeaders(headers, trailers metadata.MD) http.Header {
	result := make(http.Header)
	for key, values := range headers {
		for _, value := range values {
			result.Add(key, value)
		}
	}
	for key, values := range trailers {
		name := "Trailer-" + key
		for _, value := range values {
			result.Add(name, value)
		}
	}
	return result
}

func grpcURL(endpoint grpcEndpoint) string {
	scheme := "grpc"
	if endpoint.secure {
		scheme = "grpcs"
	}
	return fmt.Sprintf("%s://%s%s", scheme, endpoint.target, endpoint.fullMethod)
}

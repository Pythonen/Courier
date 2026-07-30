package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

type compiledProtoSchema struct {
	filePath    string
	importPaths []string
	files       *protoregistry.Files
}

func grpcURLWithParams(rawURL string, params []headerEntry, resolver *variableResolver) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse gRPC URL: %w", err)
	}
	query := parsed.Query()
	for _, parameter := range params {
		key := strings.TrimSpace(resolver.Resolve(parameter.key))
		if key != "" {
			query.Add(key, resolver.Resolve(parameter.value))
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func compileProtoSchema(ctx context.Context, path string, importPaths []string) (compiledProtoSchema, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return compiledProtoSchema{}, fmt.Errorf("protobuf service definition path is empty")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return compiledProtoSchema{}, fmt.Errorf("resolve protobuf path: %w", err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return compiledProtoSchema{}, fmt.Errorf("open protobuf service definition: %w", err)
	}
	if !info.Mode().IsRegular() {
		return compiledProtoSchema{}, fmt.Errorf("protobuf service definition is not a regular file: %s", absolutePath)
	}

	resolvedPaths := []string{filepath.Dir(absolutePath)}
	seenPaths := map[string]bool{resolvedPaths[0]: true}
	for _, importPath := range importPaths {
		importPath = strings.TrimSpace(importPath)
		if importPath == "" {
			continue
		}
		resolved, resolveErr := filepath.Abs(importPath)
		if resolveErr != nil {
			return compiledProtoSchema{}, fmt.Errorf("resolve protobuf import path %q: %w", importPath, resolveErr)
		}
		if !seenPaths[resolved] {
			resolvedPaths = append(resolvedPaths, resolved)
			seenPaths[resolved] = true
		}
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: resolvedPaths}),
	}
	compiled, err := compiler.Compile(ctx, filepath.Base(absolutePath))
	if err != nil {
		return compiledProtoSchema{}, fmt.Errorf("compile protobuf service definition: %w", err)
	}
	registry := new(protoregistry.Files)
	registered := make(map[string]bool)
	for _, file := range compiled {
		if err := registerProtoFile(registry, file, registered); err != nil {
			return compiledProtoSchema{}, fmt.Errorf("register protobuf descriptors: %w", err)
		}
	}
	return compiledProtoSchema{filePath: absolutePath, importPaths: resolvedPaths[1:], files: registry}, nil
}

func registerProtoFile(registry *protoregistry.Files, file protoreflect.FileDescriptor, registered map[string]bool) error {
	if file == nil || registered[file.Path()] {
		return nil
	}
	imports := file.Imports()
	for index := 0; index < imports.Len(); index++ {
		if err := registerProtoFile(registry, imports.Get(index).FileDescriptor, registered); err != nil {
			return err
		}
	}
	if err := registry.RegisterFile(file); err != nil {
		return err
	}
	registered[file.Path()] = true
	return nil
}

func localProtoMethod(ctx context.Context, endpoint grpcEndpoint) (protoreflect.MethodDescriptor, *protoregistry.Files, error) {
	schema, err := compileProtoSchema(ctx, endpoint.protoFile, endpoint.protoPaths)
	if err != nil {
		return nil, nil, err
	}
	descriptor, err := schema.files.FindDescriptorByName(endpoint.service)
	if err != nil {
		return nil, nil, fmt.Errorf("find service %s in %s: %w", endpoint.service, schema.filePath, err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, nil, fmt.Errorf("protobuf symbol %s is not a service", endpoint.service)
	}
	method := service.Methods().ByName(endpoint.method)
	if method == nil {
		return nil, nil, fmt.Errorf("service %s has no method %s", endpoint.service, endpoint.method)
	}
	return method, schema.files, nil
}

type protoRPCDefinition struct {
	service protoreflect.ServiceDescriptor
	method  protoreflect.MethodDescriptor
}

// ImportProto adds one editable saved request for every RPC reachable from a local protobuf schema.
func (m *model) ImportProto(path, server string, importPaths []string) (int, error) {
	schema, err := compileProtoSchema(context.Background(), path, importPaths)
	if err != nil {
		return 0, err
	}
	serverURL, err := parseProtoImportServer(server)
	if err != nil {
		return 0, err
	}
	var definitions []protoRPCDefinition
	schema.files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if strings.HasPrefix(file.Path(), "google/protobuf/") {
			return true
		}
		services := file.Services()
		for serviceIndex := 0; serviceIndex < services.Len(); serviceIndex++ {
			service := services.Get(serviceIndex)
			methods := service.Methods()
			for methodIndex := 0; methodIndex < methods.Len(); methodIndex++ {
				definitions = append(definitions, protoRPCDefinition{service: service, method: methods.Get(methodIndex)})
			}
		}
		return true
	})
	if len(definitions) == 0 {
		return 0, fmt.Errorf("protobuf service definition contains no RPC services")
	}
	sort.Slice(definitions, func(i, j int) bool {
		left := string(definitions[i].service.FullName()) + "/" + string(definitions[i].method.Name())
		right := string(definitions[j].service.FullName()) + "/" + string(definitions[j].method.Name())
		return left < right
	})

	requests := make([]savedRequest, 0, len(definitions))
	for _, definition := range definitions {
		requestURL := *serverURL
		requestURL.Path = "/" + string(definition.service.FullName()) + "/" + string(definition.method.Name())
		query := make(url.Values)
		query.Set("proto", schema.filePath)
		for _, importPath := range schema.importPaths {
			query.Add("proto_path", importPath)
		}
		requestURL.RawQuery = query.Encode()
		body, bodyErr := protoExampleBody(definition.method, schema.files)
		if bodyErr != nil {
			return 0, bodyErr
		}
		requests = append(requests, savedRequest{
			name:   strings.TrimSuffix(filepath.Base(schema.filePath), filepath.Ext(schema.filePath)) + " / " + string(definition.service.FullName()) + " / " + string(definition.method.Name()),
			method: "GRPC", url: requestURL.String(), auth: authConfig{typeID: authNone},
			body: bodyConfig{mode: bodyRaw, rawType: rawJSON, raw: body}, tests: []headerEntry{{key: "status", value: "200"}},
		})
	}
	m.savedRequests = append(m.savedRequests, requests...)
	return len(requests), nil
}

func parseProtoImportServer(server string) (*url.URL, error) {
	server = strings.TrimSpace(server)
	if !strings.Contains(server, "://") {
		server = "grpc://" + server
	}
	parsed, err := url.Parse(server)
	if err != nil {
		return nil, fmt.Errorf("parse gRPC import server: %w", err)
	}
	if parsed.Scheme != "grpc" && parsed.Scheme != "grpcs" {
		return nil, fmt.Errorf("gRPC import server must use grpc:// or grpcs://")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("gRPC import server is missing a host")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("gRPC import server must not include a method path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("gRPC import server must not include a query or fragment")
	}
	parsed.Path = ""
	return parsed, nil
}

func protoExampleBody(method protoreflect.MethodDescriptor, files *protoregistry.Files) (string, error) {
	types := dynamicpb.NewTypes(files)
	message := dynamicpb.NewMessage(method.Input())
	payload, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true, Resolver: types}).Marshal(message)
	if err != nil {
		return "", fmt.Errorf("create example message for %s: %w", method.FullName(), err)
	}
	if !method.IsStreamingClient() {
		return string(payload), nil
	}
	wrapped, err := json.MarshalIndent([]json.RawMessage{payload}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("create streaming example for %s: %w", method.FullName(), err)
	}
	return string(wrapped), nil
}

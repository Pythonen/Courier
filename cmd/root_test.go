package main

import "testing"

func TestValidateCLIActionsAcceptsCompatibleFlags(t *testing.T) {
	tests := []struct {
		name    string
		options cliActionOptions
	}{
		{name: "interactive TUI"},
		{name: "version", options: cliActionOptions{version: true}},
		{
			name: "multiple imports",
			options: cliActionOptions{
				importPostman:            true,
				importPostmanEnvironment: true,
				importOpenAPI:            true,
				importWSDL:               true,
				importProto:              true,
				importHAR:                true,
				importCurl:               true,
			},
		},
		{
			name: "import then export",
			options: cliActionOptions{
				importOpenAPI: true,
				exportPostman: true,
			},
		},
		{
			name: "import then run",
			options: cliActionOptions{
				importPostman: true,
				run:           true,
			},
		},
		{
			name: "multiple file exports",
			options: cliActionOptions{
				exportPostman:            true,
				exportPostmanEnvironment: true,
				exportHAR:                true,
			},
		},
		{
			name: "protobuf import modifiers",
			options: cliActionOptions{
				importProto:   true,
				grpcServerSet: true,
				protoPathSet:  true,
			},
		},
		{
			name: "collection run modifiers",
			options: cliActionOptions{
				run:           true,
				iterationsSet: true,
				delaySet:      true,
				dataSet:       true,
				runFormatSet:  true,
				bailSet:       true,
			},
		},
		{
			name: "mock selector",
			options: cliActionOptions{
				mock:            true,
				mockSelectorSet: true,
			},
		},
		{
			name: "cookie URL",
			options: cliActionOptions{
				setCookie: true,
				cookieURL: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateCLIActions(test.options); err != nil {
				t.Fatalf("validateCLIActions() error = %v", err)
			}
		})
	}
}

func TestValidateCLIActionsRejectsOrphanedModifiers(t *testing.T) {
	tests := []struct {
		name    string
		options cliActionOptions
		want    string
	}{
		{name: "gRPC server", options: cliActionOptions{grpcServerSet: true}, want: "-grpc-server requires -import-proto"},
		{name: "protobuf path", options: cliActionOptions{protoPathSet: true}, want: "-proto-path requires -import-proto"},
		{name: "iterations", options: cliActionOptions{iterationsSet: true}, want: "-iterations requires -run"},
		{name: "delay", options: cliActionOptions{delaySet: true}, want: "-delay requires -run"},
		{name: "data", options: cliActionOptions{dataSet: true}, want: "-data requires -run"},
		{name: "run format", options: cliActionOptions{runFormatSet: true}, want: "-run-format requires -run"},
		{name: "bail", options: cliActionOptions{bailSet: true}, want: "-bail requires -run"},
		{name: "mock selector", options: cliActionOptions{mockSelectorSet: true}, want: "-mock-selector requires -mock"},
		{name: "cookie URL", options: cliActionOptions{cookieURL: true}, want: "-cookie-url requires -set-cookie"},
		{name: "cookie value", options: cliActionOptions{setCookie: true}, want: "-set-cookie requires -cookie-url"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCLIValidationError(t, test.options, test.want)
		})
	}
}

func TestValidateCLIActionsRejectsAmbiguousActions(t *testing.T) {
	tests := []struct {
		name    string
		options cliActionOptions
		want    string
	}{
		{
			name:    "two cURL import sources",
			options: cliActionOptions{importCurl: true, importCurlFile: true},
			want:    "CLI actions -import-curl, -import-curl-file cannot be used together",
		},
		{
			name:    "two stdout exports",
			options: cliActionOptions{exportCurl: true, exportHTTPie: true},
			want:    "CLI actions -export-curl, -export-httpie cannot be used together",
		},
		{
			name:    "stdout and file exports",
			options: cliActionOptions{exportHTTPie: true, exportHAR: true},
			want:    "CLI actions -export-httpie, -export-har cannot be used together",
		},
		{
			name:    "two cookie actions",
			options: cliActionOptions{listCookies: true, clearCookies: true},
			want:    "CLI actions -list-cookies, -clear-cookies cannot be used together",
		},
		{
			name:    "version and import",
			options: cliActionOptions{version: true, importOpenAPI: true},
			want:    "CLI actions -version, -import-openapi cannot be used together",
		},
		{
			name:    "export and run",
			options: cliActionOptions{exportPostman: true, run: true},
			want:    "CLI actions -export-postman, -run cannot be used together",
		},
		{
			name:    "run and mock",
			options: cliActionOptions{run: true, mock: true},
			want:    "CLI actions -run, -mock cannot be used together",
		},
		{
			name:    "mock and cookie action",
			options: cliActionOptions{mock: true, setCookie: true, cookieURL: true},
			want:    "CLI actions -mock, -set-cookie cannot be used together",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCLIValidationError(t, test.options, test.want)
		})
	}
}

func assertCLIValidationError(t *testing.T, options cliActionOptions, want string) {
	t.Helper()
	err := validateCLIActions(options)
	if err == nil {
		t.Fatalf("validateCLIActions() error = nil, want %q", want)
	}
	if err.Error() != want {
		t.Fatalf("validateCLIActions() error = %q, want %q", err, want)
	}
}

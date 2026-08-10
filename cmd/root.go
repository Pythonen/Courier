package main

import (
	"context"
	"courier/tui/internal/tui"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type cliActionOptions struct {
	version                  bool
	importPostman            bool
	importPostmanEnvironment bool
	importOpenAPI            bool
	importWSDL               bool
	importProto              bool
	importHAR                bool
	importCurl               bool
	importCurlFile           bool
	exportCurl               bool
	exportHTTPie             bool
	exportPostman            bool
	exportPostmanEnvironment bool
	exportHAR                bool
	run                      bool
	mock                     bool
	listCookies              bool
	clearCookies             bool
	setCookie                bool
	cookieURL                bool
	grpcServerSet            bool
	protoPathSet             bool
	iterationsSet            bool
	delaySet                 bool
	dataSet                  bool
	runFormatSet             bool
	bailSet                  bool
	mockSelectorSet          bool
}

func validateCLIActions(options cliActionOptions) error {
	if options.grpcServerSet && !options.importProto {
		return errors.New("-grpc-server requires -import-proto")
	}
	if options.protoPathSet && !options.importProto {
		return errors.New("-proto-path requires -import-proto")
	}
	for _, modifier := range []struct {
		name string
		set  bool
	}{
		{name: "-iterations", set: options.iterationsSet},
		{name: "-delay", set: options.delaySet},
		{name: "-data", set: options.dataSet},
		{name: "-run-format", set: options.runFormatSet},
		{name: "-bail", set: options.bailSet},
	} {
		if modifier.set && !options.run {
			return fmt.Errorf("%s requires -run", modifier.name)
		}
	}
	if options.mockSelectorSet && !options.mock {
		return errors.New("-mock-selector requires -mock")
	}
	if options.cookieURL && !options.setCookie {
		return errors.New("-cookie-url requires -set-cookie")
	}
	if options.setCookie && !options.cookieURL {
		return errors.New("-set-cookie requires -cookie-url")
	}
	if options.importCurl && options.importCurlFile {
		return conflictingCLIActionError([]string{"-import-curl", "-import-curl-file"})
	}

	imports := activeCLIFlags(
		cliFlag{"-import-postman", options.importPostman},
		cliFlag{"-import-postman-environment", options.importPostmanEnvironment},
		cliFlag{"-import-openapi", options.importOpenAPI},
		cliFlag{"-import-wsdl", options.importWSDL},
		cliFlag{"-import-proto", options.importProto},
		cliFlag{"-import-har", options.importHAR},
		cliFlag{"-import-curl", options.importCurl},
		cliFlag{"-import-curl-file", options.importCurlFile},
	)
	stdoutExports := activeCLIFlags(
		cliFlag{"-export-curl", options.exportCurl},
		cliFlag{"-export-httpie", options.exportHTTPie},
	)
	fileExports := activeCLIFlags(
		cliFlag{"-export-postman", options.exportPostman},
		cliFlag{"-export-postman-environment", options.exportPostmanEnvironment},
		cliFlag{"-export-har", options.exportHAR},
	)
	if len(stdoutExports) > 1 || (len(stdoutExports) > 0 && len(fileExports) > 0) {
		return conflictingCLIActionError(append(stdoutExports, fileExports...))
	}
	exports := append(stdoutExports, fileExports...)
	cookieActions := activeCLIFlags(
		cliFlag{"-list-cookies", options.listCookies},
		cliFlag{"-clear-cookies", options.clearCookies},
		cliFlag{"-set-cookie", options.setCookie},
	)
	if len(cookieActions) > 1 {
		return conflictingCLIActionError(cookieActions)
	}

	terminalActionGroups := [][]string{
		activeCLIFlags(cliFlag{"-version", options.version}),
		exports,
		activeCLIFlags(cliFlag{"-run", options.run}),
		activeCLIFlags(cliFlag{"-mock", options.mock}),
		cookieActions,
	}
	var activeGroups [][]string
	for _, group := range terminalActionGroups {
		if len(group) > 0 {
			activeGroups = append(activeGroups, group)
		}
	}
	// Imports are deliberately composable with one terminal action (for example,
	// importing a collection and immediately exporting it). Version is the one
	// exception because it exits before any import can run.
	if options.version && len(imports) > 0 {
		return conflictingCLIActionError(append([]string{"-version"}, imports...))
	}
	if len(activeGroups) > 1 {
		var conflicting []string
		for _, group := range activeGroups {
			conflicting = append(conflicting, group...)
		}
		return conflictingCLIActionError(conflicting)
	}
	return nil
}

type cliFlag struct {
	name   string
	active bool
}

func activeCLIFlags(flags ...cliFlag) []string {
	active := make([]string, 0, len(flags))
	for _, candidate := range flags {
		if candidate.active {
			active = append(active, candidate.name)
		}
	}
	return active
}

func conflictingCLIActionError(flags []string) error {
	return fmt.Errorf("CLI actions %s cannot be used together", strings.Join(flags, ", "))
}

func main() {
	versionFlag := flag.Bool("version", false, "print version information and exit")
	workspaceFlag := flag.String("workspace", "", "path to the local Courier workspace JSON file")
	activeEnvironmentFlag := flag.String("environment", "", "select a named local environment")
	collectionFlag := flag.String("import-postman", "", "import a Postman Collection v2 JSON file")
	environmentFlag := flag.String("import-postman-environment", "", "import a Postman environment JSON file")
	openAPIFlag := flag.String("import-openapi", "", "import a Swagger 2.0 or OpenAPI 3.x JSON/YAML document")
	wsdlFlag := flag.String("import-wsdl", "", "import SOAP requests from a WSDL 1.1 document")
	protoFlag := flag.String("import-proto", "", "import gRPC requests from a local .proto service definition")
	grpcServerFlag := flag.String("grpc-server", "grpc://localhost:50051", "server URL used by -import-proto")
	protoPathFlag := flag.String("proto-path", "", "comma-separated protobuf import directories used by -import-proto")
	harFlag := flag.String("import-har", "", "import requests and captured responses from a HAR 1.2 file")
	curlFlag := flag.String("import-curl", "", "import a cURL command into the saved collection")
	curlFileFlag := flag.String("import-curl-file", "", "import a cURL command from a text file")
	exportCurlFlag := flag.String("export-curl", "", "print a saved request as cURL by 1-based index or exact name, then exit")
	exportHTTPieFlag := flag.String("export-httpie", "", "print a saved request as HTTPie by 1-based index or exact name, then exit")
	exportPostmanFlag := flag.String("export-postman", "", "export saved requests as a Postman v2.1 collection JSON file")
	exportPostmanEnvironmentFlag := flag.String("export-postman-environment", "", "export the active environment as a Postman JSON file")
	exportHARFlag := flag.String("export-har", "", "export saved requests and response examples as a HAR 1.2 file")
	runFlag := flag.String("run", "", "run saved requests: all, 1-based index, exact request name, or folder name")
	iterationsFlag := flag.Int("iterations", 1, "number of collection runner iterations")
	delayFlag := flag.Duration("delay", 0, "delay between collection runner requests")
	dataFlag := flag.String("data", "", "CSV or JSON iteration data for the collection runner")
	runFormatFlag := flag.String("run-format", "text", "collection runner output format: text, json, or junit")
	bailFlag := flag.Bool("bail", false, "stop a collection run after the first failed request")
	mockFlag := flag.String("mock", "", "serve saved response examples at this address, for example 127.0.0.1:8080")
	mockSelectorFlag := flag.String("mock-selector", "all", "saved request, folder, index, or all to expose from the mock server")
	listCookiesFlag := flag.Bool("list-cookies", false, "print the local cookie jar as JSON, then exit")
	clearCookiesFlag := flag.Bool("clear-cookies", false, "remove every cookie from the local workspace jar, then exit")
	setCookieFlag := flag.String("set-cookie", "", "add a Set-Cookie value to the local workspace jar, then exit")
	cookieURLFlag := flag.String("cookie-url", "", "absolute request URL used with -set-cookie")
	flag.Parse()
	explicitFlags := make(map[string]bool)
	flag.Visit(func(visited *flag.Flag) {
		explicitFlags[visited.Name] = true
	})
	if validationErr := validateCLIActions(cliActionOptions{
		version:                  *versionFlag,
		importPostman:            *collectionFlag != "",
		importPostmanEnvironment: *environmentFlag != "",
		importOpenAPI:            *openAPIFlag != "",
		importWSDL:               *wsdlFlag != "",
		importProto:              *protoFlag != "",
		importHAR:                *harFlag != "",
		importCurl:               *curlFlag != "",
		importCurlFile:           *curlFileFlag != "",
		exportCurl:               *exportCurlFlag != "",
		exportHTTPie:             *exportHTTPieFlag != "",
		exportPostman:            *exportPostmanFlag != "",
		exportPostmanEnvironment: *exportPostmanEnvironmentFlag != "",
		exportHAR:                *exportHARFlag != "",
		run:                      *runFlag != "",
		mock:                     *mockFlag != "",
		listCookies:              *listCookiesFlag,
		clearCookies:             *clearCookiesFlag,
		setCookie:                *setCookieFlag != "",
		cookieURL:                *cookieURLFlag != "",
		grpcServerSet:            explicitFlags["grpc-server"],
		protoPathSet:             explicitFlags["proto-path"],
		iterationsSet:            explicitFlags["iterations"],
		delaySet:                 explicitFlags["delay"],
		dataSet:                  explicitFlags["data"],
		runFormatSet:             explicitFlags["run-format"],
		bailSet:                  explicitFlags["bail"],
		mockSelectorSet:          explicitFlags["mock-selector"],
	}); validationErr != nil {
		fmt.Fprintln(os.Stderr, "Error:", validationErr)
		os.Exit(1)
	}
	if *versionFlag {
		fmt.Printf("courier %s (commit %s, built %s)\n", version, commit, date)
		return
	}
	workspacePath := *workspaceFlag
	if workspacePath == "" {
		var err error
		workspacePath, err = tui.DefaultWorkspacePath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error locating workspace:", err)
			os.Exit(1)
		}
	}
	model, err := tui.NewModelWithWorkspace(workspacePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error loading workspace:", err)
		os.Exit(1)
	}
	if *collectionFlag != "" {
		count, importErr := model.ImportPostmanCollection(*collectionFlag)
		if importErr != nil {
			fmt.Fprintln(os.Stderr, "Error importing collection:", importErr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Imported %d request(s) from Postman.\n", count)
	}
	if *environmentFlag != "" {
		count, importErr := model.ImportPostmanEnvironment(*environmentFlag)
		if importErr != nil {
			fmt.Fprintln(os.Stderr, "Error importing environment:", importErr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Imported %d environment value(s) from Postman.\n", count)
	}
	if *openAPIFlag != "" {
		count, importErr := model.ImportOpenAPI(*openAPIFlag)
		if importErr != nil {
			fmt.Fprintln(os.Stderr, "Error importing OpenAPI:", importErr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Imported %d request(s) from OpenAPI.\n", count)
	}
	if *wsdlFlag != "" {
		count, importErr := model.ImportWSDL(*wsdlFlag)
		if importErr != nil {
			fmt.Fprintln(os.Stderr, "Error importing WSDL:", importErr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Imported %d SOAP request(s) from WSDL.\n", count)
	}
	if *protoFlag != "" {
		var importPaths []string
		for _, value := range strings.Split(*protoPathFlag, ",") {
			if value = strings.TrimSpace(value); value != "" {
				importPaths = append(importPaths, value)
			}
		}
		count, importErr := model.ImportProto(*protoFlag, *grpcServerFlag, importPaths)
		if importErr != nil {
			fmt.Fprintln(os.Stderr, "Error importing protobuf service definition:", importErr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Imported %d gRPC request(s) from protobuf.\n", count)
	}
	if *harFlag != "" {
		count, importErr := model.ImportHAR(*harFlag)
		if importErr != nil {
			fmt.Fprintln(os.Stderr, "Error importing HAR:", importErr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Imported %d request(s) from HAR.\n", count)
	}
	if *curlFileFlag != "" {
		command, readErr := tui.ReadCurlCommand(*curlFileFlag)
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "Error importing cURL:", readErr)
			os.Exit(1)
		}
		*curlFlag = command
	}
	if *curlFlag != "" {
		if importErr := model.ImportCurl(*curlFlag); importErr != nil {
			fmt.Fprintln(os.Stderr, "Error importing cURL:", importErr)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Imported 1 request from cURL.")
	}
	if *activeEnvironmentFlag != "" {
		if selectErr := model.ActivateEnvironment(*activeEnvironmentFlag); selectErr != nil {
			fmt.Fprintln(os.Stderr, "Error selecting environment:", selectErr)
			os.Exit(1)
		}
	}
	if *clearCookiesFlag {
		model.ClearCookies()
	}
	if *setCookieFlag != "" {
		if err := model.SetCookie(*cookieURLFlag, *setCookieFlag); err != nil {
			fmt.Fprintln(os.Stderr, "Error setting cookie:", err)
			os.Exit(1)
		}
	} else if *cookieURLFlag != "" {
		fmt.Fprintln(os.Stderr, "Error: -cookie-url requires -set-cookie")
		os.Exit(1)
	}
	if *collectionFlag != "" || *environmentFlag != "" || *openAPIFlag != "" || *wsdlFlag != "" || *protoFlag != "" || *harFlag != "" || *curlFlag != "" {
		if saveErr := model.SaveWorkspace(); saveErr != nil {
			fmt.Fprintln(os.Stderr, "Error saving imported workspace:", saveErr)
			os.Exit(1)
		}
	}
	if *clearCookiesFlag || *setCookieFlag != "" {
		if saveErr := model.SaveWorkspace(); saveErr != nil {
			fmt.Fprintln(os.Stderr, "Error saving cookie jar:", saveErr)
			os.Exit(1)
		}
	}
	if *listCookiesFlag {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(model.Cookies()); encodeErr != nil {
			fmt.Fprintln(os.Stderr, "Error encoding cookie jar:", encodeErr)
			os.Exit(1)
		}
		return
	}
	if *clearCookiesFlag || *setCookieFlag != "" {
		return
	}
	if *exportCurlFlag != "" {
		command, exportErr := model.ExportSavedCurl(*exportCurlFlag)
		if exportErr != nil {
			fmt.Fprintln(os.Stderr, "Error exporting cURL:", exportErr)
			os.Exit(1)
		}
		fmt.Println(command)
		return
	}
	if *exportHTTPieFlag != "" {
		command, exportErr := model.ExportSavedHTTPie(*exportHTTPieFlag)
		if exportErr != nil {
			fmt.Fprintln(os.Stderr, "Error exporting HTTPie:", exportErr)
			os.Exit(1)
		}
		fmt.Println(command)
		return
	}
	exportedPostman := false
	if *exportPostmanFlag != "" {
		if exportErr := model.ExportPostmanCollection(*exportPostmanFlag); exportErr != nil {
			fmt.Fprintln(os.Stderr, "Error exporting Postman collection:", exportErr)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Exported Postman collection to", *exportPostmanFlag)
		exportedPostman = true
	}
	if *exportPostmanEnvironmentFlag != "" {
		if exportErr := model.ExportPostmanEnvironment(*exportPostmanEnvironmentFlag); exportErr != nil {
			fmt.Fprintln(os.Stderr, "Error exporting Postman environment:", exportErr)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Exported Postman environment to", *exportPostmanEnvironmentFlag)
		exportedPostman = true
	}
	if *exportHARFlag != "" {
		if exportErr := model.ExportHAR(*exportHARFlag); exportErr != nil {
			fmt.Fprintln(os.Stderr, "Error exporting HAR:", exportErr)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Exported HAR to", *exportHARFlag)
		exportedPostman = true
	}
	if exportedPostman {
		return
	}
	if *mockFlag != "" {
		handler, mockErr := model.MockHandler(*mockSelectorFlag)
		if mockErr != nil {
			fmt.Fprintln(os.Stderr, "Error starting mock server:", mockErr)
			os.Exit(1)
		}
		server := &http.Server{Addr: *mockFlag, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownContext)
		}()
		fmt.Fprintln(os.Stderr, "Courier mock server listening on http://"+*mockFlag)
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "Mock server failed:", serveErr)
			os.Exit(1)
		}
		return
	}
	if *runFlag != "" {
		if !strings.EqualFold(*runFormatFlag, "text") && !strings.EqualFold(*runFormatFlag, "json") && !strings.EqualFold(*runFormatFlag, "junit") {
			fmt.Fprintln(os.Stderr, "Error running collection: run format must be text, json, or junit")
			os.Exit(1)
		}
		var runData []map[string]string
		if *dataFlag != "" {
			runData, err = tui.LoadRunData(*dataFlag)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error loading runner data:", err)
				os.Exit(1)
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		report, runErr := model.RunCollection(ctx, tui.RunOptions{Selector: *runFlag, Iterations: *iterationsFlag, Delay: *delayFlag, Data: runData, Bail: *bailFlag})
		stop()
		if saveErr := model.SaveWorkspace(); saveErr != nil {
			fmt.Fprintln(os.Stderr, "Error saving runner environment:", saveErr)
			os.Exit(1)
		}
		if strings.EqualFold(*runFormatFlag, "json") {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			if encodeErr := encoder.Encode(report); encodeErr != nil {
				fmt.Fprintln(os.Stderr, "Error encoding collection report:", encodeErr)
				os.Exit(1)
			}
		} else if strings.EqualFold(*runFormatFlag, "junit") {
			encoded, encodeErr := tui.FormatRunReportJUnit(report)
			if encodeErr != nil {
				fmt.Fprintln(os.Stderr, "Error encoding collection report:", encodeErr)
				os.Exit(1)
			}
			fmt.Print(encoded)
		} else {
			fmt.Println(tui.FormatRunReport(report))
		}
		if runErr != nil {
			fmt.Fprintln(os.Stderr, "Error running collection:", runErr)
			os.Exit(1)
		}
		if report.Failed > 0 {
			os.Exit(1)
		}
		return
	}

	zone.NewGlobal()
	if _, err := tea.NewProgram(model).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error while running program:", err)
		os.Exit(1)
	}
}

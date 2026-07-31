<p align="center">
  <img src="./assets/Courier.png" alt="Courier" width="200"/>
</p>

<h1 align="center">Courier</h1>

✨Just like Postman, but in your terminal!✨

Are you the kind of person who does not want to leave the terminal just to test an API? Me neither.

Courier is a terminal UI HTTP, WebSocket, gRPC, and MQTT client built with Bubble Tea. It lets you compose requests (method, URL, query parameters, authorization, headers, and body), send them, and inspect response headers/body or live protocol messages without leaving your shell.

Courier targets the local, terminal-feasible API-client portion of Postman. It intentionally excludes cloud workspaces, account/team synchronization, hosted monitors, collaboration and governance, the visual Flows builder, and other service-backed features. It also does not execute Postman pre-request/test JavaScript, third-party packages, or response Visualizer scripts: Courier uses native declarative assertions and extraction actions and does not embed a JavaScript runtime, browser engine, or provider UI. OAuth authorization pages open in the operating system's external browser.

Built-in method presets are GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS, TRACE, COPY, LINK, UNLINK, PURGE, LOCK, UNLOCK, PROPFIND, and VIEW. Any valid custom HTTP method token can also be entered, imported, saved, rerun, mocked, and exported. Courier captures response cookies in a local persistent cookie jar and automatically sends matching cookies on later requests.

## Run Courier

### Install with Homebrew

Once the first release is published, install Courier from its Homebrew tap:

```bash
brew install --cask Pythonen/tap/courier
```

Upgrade an existing installation with `brew upgrade --cask courier`. Release
maintainer setup and tagging instructions are in
[`docs/RELEASING.md`](./docs/RELEASING.md).

### Prerequisites

- Go 1.25+

### Start directly

```bash
go run ./cmd
```

Courier loads and saves its local workspace at your operating system's user config directory (for example, `~/Library/Application Support/courier/workspace.json` on macOS). Use a different file when needed:

```bash
go run ./cmd -workspace /path/to/workspace.json
```

Inspect, manually add, or clear cookies in that workspace without opening the TUI:

```bash
go run ./cmd -workspace ./courier-workspace.json -list-cookies
go run ./cmd -workspace ./courier-workspace.json \
  -cookie-url https://api.example.test/account \
  -set-cookie 'session=token; Path=/; Secure; HttpOnly'
go run ./cmd -workspace ./courier-workspace.json -clear-cookies
```

`-set-cookie` accepts a standard `Set-Cookie` value. Domain, path, expiry, Secure, HttpOnly, SameSite, and host-only behavior are retained; expired cookies and server deletion cookies are removed. Cookie values are sensitive and are stored in the same owner-only (`0600`) workspace as credentials.

Import Postman Collection v2.x requests and environment values into a Courier workspace with:

```bash
go run ./cmd -workspace ./courier-workspace.json \
  -import-postman ./collection.json \
  -import-postman-environment ./environment.json
```

Imports are merged into the local workspace before the TUI opens. Collection folders are retained in saved-request names, disabled fields are skipped, and inherited Bearer, Basic, Digest, API-key, AWS Signature v4, OAuth 1.0, OAuth 2, Hawk, and NTLM authorization is converted where possible.

Export the local workspace back to offline Postman-compatible JSON files with:

```bash
go run ./cmd -workspace ./courier-workspace.json \
  -export-postman ./courier.postman_collection.json \
  -export-postman-environment ./courier.postman_environment.json
```

Collection export uses Postman Collection v2.1, retains folder paths, request bodies and authorization, and translates Courier assertions into Postman test scripts where an equivalent assertion is available. Export refuses to overwrite existing files and creates new files with owner-only permissions (`0600`).

Import Swagger 2.0 or OpenAPI 3.x JSON/YAML definitions with:

```bash
go run ./cmd -workspace ./courier-workspace.json \
  -import-openapi ./openapi.yaml
```

Courier creates one saved request per supported operation. It converts server and path variables into environment templates, imports query/header/cookie parameters, maps API-key, Basic, Bearer, OAuth 2, and OpenID security schemes to Courier auth, and generates request examples for JSON, forms, multipart uploads, binary data, XML, and text. Local `#/...` references, composed schemas, and nested references to local JSON or YAML files are resolved relative to the file that contains each reference. Cycles, missing targets, and remote references fail atomically without partially changing the collection.

Import SOAP operations from a WSDL 1.1 document with:

```bash
go run ./cmd -workspace ./courier-workspace.json -import-wsdl ./service.wsdl
```

Courier discovers service ports, SOAP 1.1 and SOAP 1.2 bindings, endpoint addresses, actions, input messages, and inline XML Schema fields. Each binding operation becomes a saved POST request with the correct SOAP content type, SOAPAction handling, and an editable XML envelope containing `{{field}}` placeholders. Generated SOAP requests use the same environments, authorization, TLS, runner, assertions, examples, and export features as other HTTP requests.

Import every RPC in a local protobuf service definition with:

```bash
go run ./cmd -workspace ./courier-workspace.json \
  -import-proto ./proto/service.proto \
  -proto-path ./proto/vendor,./shared/proto \
  -grpc-server grpcs://api.example.test:7443
```

Courier compiles `.proto` files natively in Go, resolves multi-file imports from the schema directory and optional comma-separated import roots, and includes the standard protobuf definitions without requiring `protoc` or generated client code. Each RPC becomes a saved gRPC request with an editable protobuf-JSON example; client-streaming and bidirectional methods receive a JSON array example. Imports are atomic, so an invalid schema adds no partial requests. The generated URLs retain the absolute schema and import paths for later interactive and headless collection runs.

Import HTTP Archive 1.2 traffic captured by browsers and local proxies with:

```bash
go run ./cmd -workspace ./courier-workspace.json -import-har ./capture.har
```

Each HAR entry becomes a saved request with its method, URL, query, headers, cookies, and body. Captured responses become local saved examples, including base64-decoded response content, so they can be inspected or served by Courier's mock server.

Export the saved collection and its response examples back to HAR 1.2 with:

```bash
go run ./cmd -workspace ./courier-workspace.json -export-har ./courier.har
```

The export contains standard replayable HAR request and response fields. Courier-specific extension fields preserve authorization settings, structured body modes, assertions, original URL/parameter separation, and multiple named response examples across a Courier HAR round trip. Export refuses to overwrite an existing file and writes with owner-only permissions (`0600`).

Import a cURL command directly or from a text file, and export a saved request by its one-based collection index or exact name:

```bash
go run ./cmd -import-curl "curl https://api.example.com/users"
go run ./cmd -import-curl-file ./request.curl
go run ./cmd -export-curl 1
go run ./cmd -export-httpie "Users / Create user"
```

The cURL importer parses command text without executing a shell. It supports request methods, headers, Basic, Digest, and AWS Signature v4 auth, cookies, query strings, raw and URL-encoded data, multipart forms, binary files, and `--unix-socket`. Unix-socket requests export back to an executable cURL command using the same option. HTTPie export covers headers, query values, cookies, Basic and Digest auth, token placeholders, raw/form/multipart/binary/GraphQL bodies, and shell-safe quoting; Unix-socket requests explicitly direct you to the cURL exporter because stock HTTPie requires a plugin for that transport.

Run the entire saved collection, one request, or a Postman folder path without opening the TUI:

```bash
go run ./cmd -run all
go run ./cmd -run 2 -iterations 10 -delay 250ms
go run ./cmd -run "Users" -run-format json
go run ./cmd -run all -data ./iterations.csv
go run ./cmd -environment Production -run all
go run ./cmd -run all -bail
go run ./cmd -run all -run-format junit > courier-results.xml
```

The runner executes requests sequentially using the workspace environment and a shared cookie jar. `-data` accepts a CSV file with a header row or a JSON array of objects; each row becomes an iteration and overrides matching environment variables. `-iterations` repeats the complete data set, `-delay` pauses between requests, and `-bail` stops after the first request or assertion failure. Text output is human-readable, JSON exposes the structured result, and JUnit XML integrates with CI test-report viewers. Transport failures and HTTP 4xx/5xx responses are reported as failures and produce a non-zero process exit status. `Ctrl+C` or `SIGTERM` cancels a run.

Serve saved response examples as an offline mock API with:

```bash
go run ./cmd -mock 127.0.0.1:8080
go run ./cmd -mock 127.0.0.1:8080 -mock-selector "Users"
```

The mock server matches HTTP method, path, wildcard `{{pathVariables}}`, and query values, then returns the closest saved example. Captured path and active environment variables are resolved in example headers and bodies. Use `X-Mock-Response-Name` or `X-Mock-Response-Code` to select a particular example. Set `X-Mock-Match-Request-Headers` to a comma-separated list of header names or `X-Mock-Match-Request-Body: true` to require those request details to match. The server binds only to the address you provide, runs entirely from the local workspace, and shuts down cleanly on `Ctrl+C` or `SIGTERM`.

### Or build a binary

```bash
go build -o courier ./cmd
./courier
```

## Basic controls

- `Tab` / `Shift+Tab`: move between panes
- `Ctrl+O`: cycle request method
- `O`: enter a custom HTTP method
- `Ctrl+S` or `Enter`: send request
- `Ctrl+X`: cancel the active request
- `Ctrl+K`: connect or disconnect a WebSocket or MQTT session
- `Ctrl+T`: open or close transport settings
- `Ctrl+E`: open or close the local environment editor
- `Ctrl+W`: save a new request or update the currently loaded saved request
- `Ctrl+Y`: save the active response as an example on the loaded collection request
- `Ctrl+G`: render the current request as a copyable cURL command in the response pane
- `Ctrl+D`: save the active response body or headers to a new local file
- `Ctrl+P`: cycle the sidebar through request history, the saved collection, saved response examples, and the persistent cookie jar
- `Ctrl+C`: quit

Inside the request pane:

- `Left` / `Right`: switch request tabs
- `i` / `Esc`: enter or leave input mode
- Headers and Params: `j`/`k` move rows, `h`/`l` move columns, `o` adds a row, and `dd` deletes one
- Authorization: `o` cycles No Auth, Bearer Token, JWT Bearer, Basic Auth, Digest Auth, API Key, AWS Signature v4, OAuth 2 Client Credentials, OAuth 2 Password Credentials, OAuth 2 Refresh Token, OAuth 2 Authorization Code, Hawk, NTLM, and OAuth 1.0; `Space` toggles API-key, JWT, and OAuth 1.0 placement between header and query
- Cookies: add explicit request cookies with the same key-value controls used by Headers and Query
- Tests: add declarative response assertions or `set.name` response-to-variable actions using the expression and expected/source columns
- Body: `m` cycles No Body, Raw, x-www-form-urlencoded, form-data, Binary, and GraphQL; `f` cycles the raw format
- Multipart form files use `@/path/to/file` as the field value; prefix a literal `@` value with `@@`

GraphQL mode provides separate query, variables JSON, and optional operation-name fields. Use `j`/`k` to choose a field and `i` to edit it. Courier validates the variables document, resolves `{{environment}}` templates in all three fields, and sends the standard JSON GraphQL envelope with `application/json` content type.

Inside the saved collection, use `j`/`k` to choose a request, `Enter` to load it, `r` to rename it, `c` to duplicate it, and `dd` to delete it. Names such as `Users / Create user` organize requests into folder paths and can be selected by folder name in the collection runner. Once a saved request is loaded, `Ctrl+W` updates it in place while preserving its name; duplicate it first when you want a “Save As” workflow. Saved requests preserve the method, URL, query parameters, headers, cookies, authorization, body configuration, and tests. Environment values and saved requests are written atomically to the workspace file, which Courier creates with owner-only permissions (`0600`). Since authorization values may contain secrets, treat that file as sensitive.

After sending a loaded collection request, press `Ctrl+Y` to attach the response as a named example. Cycle to the Examples sidebar with `Ctrl+P`; use `j`/`k` and `Enter` to inspect examples, `r` to rename one, and `dd` to delete it. Examples retain status, headers, raw and formatted bodies, survive request updates and duplication, and round-trip through Postman Collection v2.1 imports and exports.

Request history is also stored locally in the workspace and restores both the request configuration and captured response. The newest 100 entries are retained, subject to a 25 MiB serialized-history cap. Use `j`/`k` and `Enter` to reopen an entry, or `dd` to delete it permanently. History may contain authorization values and response data, so it has the same sensitivity as saved requests.

The Cookies request tab adds explicit cookies to that saved request. Matching cookies captured from HTTP, WebSocket, and Socket.IO responses are merged from the persistent workspace jar. Cycle the sidebar to Cookies to inspect them; use `j`/`k` and `dd` to delete an individual stored cookie. Session cookies are intentionally retained across Courier restarts so terminal collection runs can continue authenticated workflows; use `-clear-cookies` when you want a clean jar.

Transport settings include redirect following, request timeout, HTTP protocol selection, a proxy URL and bypass list, TLS certificate verification, a custom PEM CA bundle, and mutual-TLS client credentials. Proxies support `http`, `https`, `socks4`, `socks4a`, `socks5`, and `socks5h` URLs; credentials can be included in the URL, and the comma-separated bypass list accepts hosts, domain suffixes, IP addresses, and CIDR ranges. SOCKS4A and SOCKS5H resolve target hostnames through the proxy, while SOCKS4 and SOCKS5 resolve them locally. Client credentials can be a PEM certificate/private-key pair or a modern PKCS#12 `.p12`/`.pfx` bundle with an optional passphrase. Press `p` in the settings pane to switch between Network and TLS pages. HTTP protocol selection supports Auto negotiation, forced HTTP/1.x, and forced HTTP/2; use `h`/`l` or `Space` on the HTTP version row, and verify the negotiated protocol in the response metadata. TLS verification remains enabled by default; PEM certificate and key paths must be configured together, and PEM and PFX identities are mutually exclusive. Paths, proxy fields, and the PFX passphrase support the same `{{environment}}` templates as requests. These settings are persisted in the owner-only local workspace and honored by HTTP, WebSocket, Socket.IO, gRPC, OAuth token acquisition, and the headless collection runner. MQTT uses a direct raw TCP broker connection and rejects proxy configuration explicitly. Treat the workspace as sensitive when it contains literal proxy credentials or a PFX passphrase.

On macOS and Linux, Courier can send HTTP directly over a Unix domain socket using Postman's URL form: `http://unix:/absolute/path/to/service.sock:/resource`. The shorter `unix:/absolute/path/to/service.sock:/resource` form defaults to HTTP; use `https://unix:...` for TLS. Methods, query parameters, headers, bodies, auth, cookies, redirects, streaming, assertions, variables, history, saved requests, and collection runs work through the socket. A `Host` entry in the Headers tab controls the HTTP host when a daemon requires one.

Responses with `Content-Type: text/event-stream` are displayed incrementally as Server-Sent Events arrive, remain cancellable with `Ctrl+X`, and are finalized into history when the server closes the stream. Assertions and `set.name` extraction actions run against the complete stream in collection runs. Set the Network timeout to `0` (`no limit`) for intentionally long-lived streams; the default remains 30 seconds.

For gRPC, enter `grpc://host:port/package.Service/Method` for plaintext or `grpcs://host:port/package.Service/Method` for TLS, put the request message in Body as protobuf JSON, and send normally. Courier uses server reflection by default. When reflection is disabled, add `proto=/absolute/path/service.proto` and optional repeated `proto_path=/import/root` entries in Params (or in the URL query) to compile a local service definition instead; these values support `{{environment}}` templates. Unary, server-streaming, client-streaming, and bidirectional RPCs are supported; use one JSON object for unary/server-streaming input and a JSON array of messages for client/bidirectional input. Streaming responses arrive incrementally and can be cancelled with `Ctrl+X`. Request headers become gRPC metadata, response metadata and trailers appear in Headers, and Bearer, Basic, header API-key, OAuth 2 token grants, cookies, variables, timeouts, HTTP/HTTPS CONNECT or SOCKS proxies, custom CAs, and mutual TLS are reused. Assertions, extraction actions, history, saved requests, examples, and the headless collection runner operate on the completed response transcript.

For WebSockets, enter a `ws://` or `wss://` URL and press `Ctrl+K` (or send once) to connect. Courier applies query parameters, headers, cookies, authorization, proxy settings, custom CAs, and mutual TLS to the opening handshake. Once connected, `Ctrl+S` sends the current Body as a text message; Binary body mode sends a binary frame. Incoming and outgoing frames appear in a directional response transcript, with binary payloads represented as base64. Press `Ctrl+K` or `Ctrl+X` to disconnect. Response assertions and extraction actions are evaluated against the completed transcript. Saved WebSocket requests remain available in collections, while interactive WebSocket sessions are intentionally not executed by the headless HTTP collection runner.

For Socket.IO, use `socketio://host` or `socketios://host` to select the Socket.IO client while preserving the familiar `ws://` / `wss://` transport distinction. Courier speaks Socket.IO 5 over Engine.IO 4's native WebSocket transport. Set `event=name` in the URL query or Params tab, then press `Ctrl+S` to emit it. A JSON array Body supplies multiple event arguments, any other JSON value supplies one argument, and plain text is sent as a string. Use `namespace=/chat`, `handshake_path=/socket.io/`, and `auth={"token":"value"}` when required. Query parameters, headers, cookies, authorization, proxies, TLS controls, variables, transcripts, assertions, history, and saved requests work as they do for WebSockets. `Ctrl+K` connects or disconnects. Courier intentionally omits HTTP long-polling fallback, which Postman's Socket.IO client also does not support.

For MQTT, enter `mqtt://host/topic` for plaintext or `mqtts://host/topic` for TLS, then press `Ctrl+K` (or send once) to connect. Courier supports MQTT 5.0 by default and MQTT 3.1.1 with `version=3.1.1`. Press `Ctrl+S` to publish the current Body to the URL-path topic; Binary body mode publishes the selected file bytes. Incoming and outgoing publishes appear in the response transcript, with binary payloads represented as base64. Basic Auth supplies the broker username and password. Headers become MQTT 5 user properties, and TLS uses the same certificate verification, custom CA, and mutual-TLS settings as HTTPS.

MQTT controls can be placed in the URL query or Params tab: `qos=0|1|2`, `retain=true|false`, `clean_start=true|false`, `keep_alive=30s`, `client_id=name`, and repeated `subscribe=topic:qos` entries. Last-will settings use `will_topic`, `will_payload`, `will_qos`, and `will_retain`. Press `Ctrl+K` or `Ctrl+X` to disconnect. Response assertions and extraction actions are evaluated against the completed transcript; the synthetic connected status is `200` and `header.MQTT-Version` exposes the negotiated configuration. Saved MQTT requests remain available in collections, while live broker sessions are intentionally not executed by the headless collection runner. MQTT is implemented with native Go protocol clients and does not embed or execute a scripting runtime.

JWT Bearer auth generates and signs a fresh token for every request using native cryptography. It supports HS256/384/512, RS256/384/512, PS256/384/512, and ES256/384/512; HMAC secrets can be plain or base64-encoded, while asymmetric keys can be an inline PEM value or a path to a PKCS #8, PKCS #1, or SEC 1 PEM private key. Configure JSON claims, custom JWT headers, the authorization prefix, and header or query placement. Use `h`/`l` to choose the algorithm, `Space` to toggle placement, and `b` to toggle base64-secret decoding. All JWT fields support environment templates and persist with saved requests.

OAuth 2 Client Credentials accepts a token URL, client ID, client secret, and optional space-separated scopes. OAuth 2 Password Credentials adds the resource-owner username and password for legacy providers that still expose that grant; client credentials are optional for public clients. OAuth 2 Refresh Token exchanges a stored refresh token for a new access token. OAuth 2 Authorization Code opens the provider in the system browser and receives the redirect through an ephemeral local loopback listener; Courier does not embed a browser, JavaScript engine, or provider page. Configure the authorization and token URLs, client ID, optional secret and scopes, and a loopback `http://localhost`, `http://127.0.0.1`, or `http://[::1]` callback; port `0` asks the OS for a free port. Press `g` to authorize, `p` to toggle S256 PKCE, and `a` to choose Basic, request-body, or public-client token-endpoint authentication. Courier validates callback state, uses a fresh cryptographic verifier for every login, caches the issued access/refresh token locally, and refuses expired cached access tokens. Courier resolves environment templates in every field, and token acquisition uses the same timeout, proxy, TLS, redirect, and cancellation settings as normal requests. OAuth secrets, passwords, refresh tokens, and cached access tokens are stored in the owner-only local workspace file, so continue to treat that file as sensitive.

Digest Auth performs the HTTP challenge-and-response exchange and safely replays request bodies. Courier supports MD5, SHA-256, and SHA-512-256, including their `-sess` variants, with `qop=auth`, opaque values, and user hashing. Credentials support environment templates and are never sent before the server supplies a Digest challenge.

AWS Signature v4 accepts an access key ID, secret access key, Region, service code, and optional temporary session token. Courier hashes the outgoing payload, canonicalizes the URI, query, and required headers, and signs the request with `AWS4-HMAC-SHA256`. All credential fields support environment templates. AWS credentials are stored in the owner-only workspace when a request is saved, so treat that file as sensitive.

Hawk authentication accepts a credential ID, shared key, SHA-1 or SHA-256 algorithm, and optional application extension data. Courier generates a cryptographically random nonce and current timestamp for every request, signs the method, request target, host, port, and extension data, and includes a standards-compatible payload hash whenever a request body is present. Hawk fields support environment templates and round-trip through Courier, Postman, and HAR exports. SHA-1 is available only for interoperability with existing Hawk credentials; prefer SHA-256 for new credentials.

NTLM authentication supports NTLMv2 and Negotiate challenges for HTTP requests. Enter the username, password, optional Active Directory user domain, and optional workstation name. Courier sends the first request anonymously and converts the locally held credentials into NTLM handshake messages only after the server requests NTLM or Negotiate; it does not fall back to sending Basic credentials. Request bodies are safely replayed across the handshake. NTLM settings support environment templates and round-trip through Courier, Postman, HAR, and cURL. Persistent WebSocket, Socket.IO, MQTT, and gRPC sessions reject NTLM explicitly instead of exposing its bootstrap credentials outside the HTTP challenge transport.

OAuth 1.0 signs requests natively without an embedded scripting runtime. It supports HMAC-SHA1, HMAC-SHA256, RSA-SHA1, and PLAINTEXT signatures; optional access-token, realm, callback, and verifier fields; and either Authorization-header or body-and-query placement. Body-and-query placement writes protocol parameters into form-encoded POST/PUT bodies and otherwise uses the query string, matching OAuth 1.0 client behavior. Query parameters and form bodies participate in RFC 5849 normalization, while the timestamp and cryptographically random nonce are generated for every request. Press `b` to include the standard SHA-1 `oauth_body_hash` extension for non-form payloads. OAuth 1.0 settings support environment templates and round-trip through Courier, Postman, and HAR exports. SHA-1 and PLAINTEXT remain available for interoperability with existing providers; prefer HMAC-SHA256 when the provider supports it.

Environment variables use `{{name}}` templates and are resolved in URLs, query parameters, headers, cookies, authorization, bodies, multipart fields, and file paths. Supported dynamic variables include `{{$guid}}`, `{{$randomUUID}}`, `{{$timestamp}}`, `{{$isoTimestamp}}`, and `{{$randomInt}}`.

The environment editor supports multiple named local profiles. Press `p` to cycle profiles, `n` to create one, `r` to rename the active profile, and `dd` to delete it. Use `-environment NAME` to select a profile for collection runs or exports. Postman environment imports retain their environment name and become local profiles.

Inside the response pane, use `Left`/`Right` (or `h`/`l`) to switch tabs and uppercase `H`/`L` to scroll long lines horizontally. Press `/` to search the active body, headers, or tests tab, then use `n` and `N` to move between matches. Press `f` to filter a JSON response with JSONPath or an XML/HTML response with XPath; predicates, wildcards, recursion, array unions, and slices are supported. Press `F` to clear the filter. Filtering only changes the displayed body—exports, examples, assertions, extraction actions, and history continue to use the complete original response. Response export paths also support environment templates. Courier refuses to overwrite an existing export file and creates new files with owner-only permissions (`0600`). Structured filters are evaluated by native Go parsers and do not execute response code.

## Response assertions

Assertions are saved with each request and shown in the response pane's Tests tab. The collection runner treats any failed assertion as a failed request, even when the HTTP status itself is successful.

| Expression | Expected value | Meaning |
| --- | --- | --- |
| `status` | `200` or `200, 201` | Exact allowed status codes |
| `status.class` | `2xx` | Status-code class |
| `header.Content-Type` | `application/json` | Exact header value; use `*` for existence |
| `body.contains` | `success` | Literal response-body substring |
| `body.matches` | `^OK` | Go regular expression |
| `json.user.id` | `42` | JSON dot path, with array indexes such as `items[0].id` |
| `time.lt` | `500ms` | Maximum response time; a bare number means milliseconds |
| `size.lt` | `10KiB` | Maximum response size in bytes, KB, KiB, MB, or MiB |
| `set.token` | `json.access_token` | Store a JSON response value as `{{token}}` |
| `set.requestId` | `header.X-Request-ID` | Store a response header as `{{requestId}}` |
| `set.payload` | `body` | Store the complete response body as `{{payload}}` |
| `set.id` | `body.matches:id=(\d+)` | Store the first regular-expression capture, or the full match when there is no capture |
| `set.statusCode` | `status` | Store the numeric response status |

Successful `set.name` actions update the active local environment, making response values available to later collection requests and future runs. They round-trip through Courier workspace files and Postman collection exports. Courier does not embed or execute JavaScript; imported Postman JavaScript test and pre-request scripts are ignored. Equivalent portable workflows can be built with declarative assertions and extraction actions.

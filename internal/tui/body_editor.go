package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type bodyMode int

const (
	bodyNone bodyMode = iota
	bodyRaw
	bodyFormURLEncoded
	bodyMultipart
	bodyBinary
	bodyGraphQL
	bodyModeCount
)

type rawBodyType int

const (
	rawJSON rawBodyType = iota
	rawText
	rawXML
	rawHTML
	rawBodyTypeCount
)

type bodyConfig struct {
	mode                 bodyMode
	rawType              rawBodyType
	raw                  string
	form                 []headerEntry
	multipart            []headerEntry
	binaryPath           string
	graphqlQuery         string
	graphqlVariables     string
	graphqlOperationName string
}

func newGraphQLTextarea(placeholder string) textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.ShowLineNumbers = false
	input.CharLimit = 1 << 20
	input.Blur()
	return input
}

func newBinaryPathInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "/path/to/file"
	input.CharLimit = 4096
	input.Blur()
	return input
}

func newFormTable() headersTable {
	return newKeyValueTable("Field", "Value", "field", "value")
}

func newMultipartTable() headersTable {
	return newKeyValueTable("Field", "Value / @file", "field", "value or @/path")
}

func (m *model) bodyConfig() bodyConfig {
	return bodyConfig{
		mode:                 m.bodyMode,
		rawType:              m.rawBodyType,
		raw:                  m.bodyInput.Value(),
		form:                 m.formInput.Entries(),
		multipart:            m.multipartInput.Entries(),
		binaryPath:           m.binaryPathInput.Value(),
		graphqlQuery:         m.graphqlQueryInput.Value(),
		graphqlVariables:     m.graphqlVariablesInput.Value(),
		graphqlOperationName: m.graphqlOperationInput.Value(),
	}
}

func (m *model) setBodyConfig(config bodyConfig) {
	m.bodyMode = config.mode
	m.rawBodyType = config.rawType
	m.bodyInput.SetValue(config.raw)
	m.formInput.SetEntries(config.form)
	m.multipartInput.SetEntries(config.multipart)
	m.binaryPathInput.SetValue(config.binaryPath)
	m.graphqlQueryInput.SetValue(config.graphqlQuery)
	m.graphqlVariablesInput.SetValue(config.graphqlVariables)
	m.graphqlOperationInput.SetValue(config.graphqlOperationName)
	m.graphqlField = 0
	m.blurBodyEditor()
}

func (m model) bodyEditable() bool { return m.bodyMode != bodyNone }

func (m *model) focusBodyEditor() {
	switch m.bodyMode {
	case bodyFormURLEncoded:
		m.formInput.Focus()
	case bodyMultipart:
		m.multipartInput.Focus()
	}
}

func (m *model) blurBodyEditor() {
	m.bodyInput.Blur()
	m.formInput.Blur()
	m.multipartInput.Blur()
	m.binaryPathInput.Blur()
	m.graphqlQueryInput.Blur()
	m.graphqlVariablesInput.Blur()
	m.graphqlOperationInput.Blur()
}

func (m *model) focusBodyInput() tea.Cmd {
	switch m.bodyMode {
	case bodyRaw:
		return m.bodyInput.Focus()
	case bodyFormURLEncoded:
		return m.formInput.FocusCurrent()
	case bodyMultipart:
		return m.multipartInput.FocusCurrent()
	case bodyBinary:
		return m.binaryPathInput.Focus()
	case bodyGraphQL:
		if m.graphqlField == 0 {
			return m.graphqlQueryInput.Focus()
		}
		if m.graphqlField == 1 {
			return m.graphqlVariablesInput.Focus()
		}
		return m.graphqlOperationInput.Focus()
	default:
		return nil
	}
}

func (m *model) updateBodyInsert(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.bodyMode {
	case bodyRaw:
		m.bodyInput, cmd = m.bodyInput.Update(msg)
	case bodyFormURLEncoded:
		cmd = m.formInput.UpdateInsert(msg)
	case bodyMultipart:
		cmd = m.multipartInput.UpdateInsert(msg)
	case bodyBinary:
		m.binaryPathInput, cmd = m.binaryPathInput.Update(msg)
	case bodyGraphQL:
		switch m.graphqlField {
		case 0:
			m.graphqlQueryInput, cmd = m.graphqlQueryInput.Update(msg)
		case 1:
			m.graphqlVariablesInput, cmd = m.graphqlVariablesInput.Update(msg)
		default:
			m.graphqlOperationInput, cmd = m.graphqlOperationInput.Update(msg)
		}
	}
	return cmd
}

func (m *model) updateBodyNormal(keyStr string) {
	switch keyStr {
	case "m":
		m.bodyMode = (m.bodyMode + 1) % bodyModeCount
		m.blurBodyEditor()
		m.focusBodyEditor()
		if m.bodyMode == bodyGraphQL {
			m.graphqlField = 0
		}
	case "f":
		if m.bodyMode == bodyRaw {
			m.rawBodyType = (m.rawBodyType + 1) % rawBodyTypeCount
		}
	default:
		if m.bodyMode == bodyGraphQL {
			switch keyStr {
			case "j", "down":
				m.graphqlField = min(2, m.graphqlField+1)
			case "k", "up":
				m.graphqlField = max(0, m.graphqlField-1)
			}
			return
		}
		switch m.bodyMode {
		case bodyFormURLEncoded:
			m.formInput.UpdateNormal(keyStr)
		case bodyMultipart:
			m.multipartInput.UpdateNormal(keyStr)
		}
	}
}

func (m model) viewBody() string {
	modeName := map[bodyMode]string{
		bodyNone: "No Body", bodyRaw: "Raw", bodyFormURLEncoded: "x-www-form-urlencoded",
		bodyMultipart: "form-data", bodyBinary: "Binary", bodyGraphQL: "GraphQL",
	}[m.bodyMode]
	header := headerStyle.Render("Type: ") + modeName + hintStyle.Render("  m:cycle")

	switch m.bodyMode {
	case bodyNone:
		return header + "\n" + hintStyle.Render("No request body will be sent.")
	case bodyRaw:
		formatName := map[rawBodyType]string{rawJSON: "JSON", rawText: "Text", rawXML: "XML", rawHTML: "HTML"}[m.rawBodyType]
		return header + headerStyle.Render("  Format: ") + formatName + hintStyle.Render("  f:cycle") + "\n" + m.bodyInput.View()
	case bodyFormURLEncoded:
		return header + "\n" + m.formInput.View()
	case bodyMultipart:
		return header + hintStyle.Render("  Prefix file paths with @") + "\n" + m.multipartInput.View()
	case bodyBinary:
		return header + "\n" + headerStyle.Render("File: ") + m.binaryPathInput.View()
	case bodyGraphQL:
		queryLabel, variablesLabel, operationLabel := "  Query", "  Variables (JSON)", "  Operation: "
		if m.focus == paneRequest && m.requestTab == requestTabBody {
			switch m.graphqlField {
			case 0:
				queryLabel = "> Query"
			case 1:
				variablesLabel = "> Variables (JSON)"
			default:
				operationLabel = "> Operation: "
			}
		}
		return header + hintStyle.Render("  jk:field") + "\n" + activeCellStyle.Render(operationLabel) + m.graphqlOperationInput.View() + "\n" + activeCellStyle.Render(queryLabel) + "\n" + m.graphqlQueryInput.View() + "\n" + activeCellStyle.Render(variablesLabel) + "\n" + m.graphqlVariablesInput.View()
	default:
		return header
	}
}

func (m *model) buildRequestBody(resolver *variableResolver) (payload []byte, contentType string, hasBody bool, err error) {
	switch m.bodyMode {
	case bodyNone:
		return nil, "", false, nil
	case bodyRaw:
		contentTypes := map[rawBodyType]string{
			rawJSON: "application/json", rawText: "text/plain", rawXML: "application/xml", rawHTML: "text/html",
		}
		return []byte(resolver.Resolve(m.bodyInput.Value())), contentTypes[m.rawBodyType], true, nil
	case bodyFormURLEncoded:
		values := url.Values{}
		for _, entry := range m.formInput.Entries() {
			values.Add(resolver.Resolve(entry.key), resolver.Resolve(entry.value))
		}
		return []byte(values.Encode()), "application/x-www-form-urlencoded", true, nil
	case bodyMultipart:
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		for _, entry := range m.multipartInput.Entries() {
			key := resolver.Resolve(entry.key)
			value := resolver.Resolve(entry.value)
			if strings.HasPrefix(value, "@@") {
				value = strings.TrimPrefix(value, "@")
			}
			if strings.HasPrefix(value, "@") {
				path := strings.TrimSpace(strings.TrimPrefix(value, "@"))
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return nil, "", false, fmt.Errorf("read multipart file %q: %w", path, readErr)
				}
				part, createErr := writer.CreateFormFile(key, filepath.Base(path))
				if createErr != nil {
					return nil, "", false, fmt.Errorf("create multipart field %q: %w", key, createErr)
				}
				if _, writeErr := part.Write(data); writeErr != nil {
					return nil, "", false, fmt.Errorf("write multipart field %q: %w", key, writeErr)
				}
				continue
			}
			if writeErr := writer.WriteField(key, value); writeErr != nil {
				return nil, "", false, fmt.Errorf("write multipart field %q: %w", key, writeErr)
			}
		}
		if closeErr := writer.Close(); closeErr != nil {
			return nil, "", false, fmt.Errorf("finalize multipart body: %w", closeErr)
		}
		return buffer.Bytes(), writer.FormDataContentType(), true, nil
	case bodyBinary:
		path := strings.TrimSpace(resolver.Resolve(m.binaryPathInput.Value()))
		if path == "" {
			return nil, "", false, fmt.Errorf("binary body requires a file path")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, "", false, fmt.Errorf("read binary body %q: %w", path, readErr)
		}
		return data, "application/octet-stream", true, nil
	case bodyGraphQL:
		data, graphQLErr := buildGraphQLPayload(m.graphqlQueryInput.Value(), m.graphqlVariablesInput.Value(), m.graphqlOperationInput.Value(), resolver)
		if graphQLErr != nil {
			return nil, "", false, graphQLErr
		}
		return data, "application/json", true, nil
	default:
		return nil, "", false, nil
	}
}

func buildGraphQLPayload(queryText, variablesText, operationName string, resolver *variableResolver) ([]byte, error) {
	query := resolver.Resolve(queryText)
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("GraphQL body requires a query")
	}
	request := map[string]interface{}{"query": query}
	if operationName = strings.TrimSpace(resolver.Resolve(operationName)); operationName != "" {
		request["operationName"] = operationName
	}
	variablesText = strings.TrimSpace(resolver.Resolve(variablesText))
	if variablesText != "" {
		var variables interface{}
		if decodeErr := json.Unmarshal([]byte(variablesText), &variables); decodeErr != nil {
			return nil, fmt.Errorf("parse GraphQL variables: %w", decodeErr)
		}
		request["variables"] = variables
	}
	data, encodeErr := json.Marshal(request)
	if encodeErr != nil {
		return nil, fmt.Errorf("encode GraphQL body: %w", encodeErr)
	}
	return data, nil
}

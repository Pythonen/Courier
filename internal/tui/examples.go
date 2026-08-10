package tui

import (
	"fmt"
	"strconv"
	"strings"
)

type savedExampleRef struct {
	requestIndex int
	exampleIndex int
}

func (m model) savedExampleRefs() []savedExampleRef {
	var refs []savedExampleRef
	for requestIndex, request := range m.savedRequests {
		for exampleIndex := range request.examples {
			refs = append(refs, savedExampleRef{requestIndex: requestIndex, exampleIndex: exampleIndex})
		}
	}
	return refs
}

func (m *model) saveCurrentResponseExample() error {
	if m.activeSavedIndex < 0 || m.activeSavedIndex >= len(m.savedRequests) {
		return fmt.Errorf("load or save a collection request before saving an example")
	}
	if m.response == "" && !m.responseRawAvailable {
		return fmt.Errorf("send the request before saving an example")
	}
	request := &m.savedRequests[m.activeSavedIndex]
	name := strings.TrimSpace(strings.Split(m.responseMeta, "•")[0])
	if name == "" {
		name = "Response"
		if m.responseStatusCode != 0 {
			name = strconv.Itoa(m.responseStatusCode)
		}
	}
	name = uniqueExampleName(request.examples, name)
	previousPos := m.examplePos
	request.examples = append(request.examples, savedExample{
		name: name, statusCode: m.responseStatusCode,
		responseBody: m.response, responseRaw: m.responseRaw, responseRawAvailable: m.responseRawAvailable,
		responseHeaders: m.responseHeaders, responseMeta: m.responseMeta,
	})
	m.examplePos = len(m.savedExampleRefs()) - 1
	saveResult := m.saveWorkspaceWithStatus()
	if saveResult == workspaceSaveConflictHandled {
		m.examplePos = clampWorkspacePosition(previousPos, len(m.savedExampleRefs()))
	}
	if !saveResult.succeeded() {
		return fmt.Errorf("%s", m.response)
	}
	m.responseSearchStatus = "Saved response example " + name
	return nil
}

func uniqueExampleName(examples []savedExample, base string) string {
	used := func(candidate string) bool {
		for _, example := range examples {
			if example.name == candidate {
				return true
			}
		}
		return false
	}
	if !used(base) {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s %d", base, suffix)
		if !used(candidate) {
			return candidate
		}
	}
}

func (m *model) applySavedExample(ref savedExampleRef) {
	if ref.requestIndex < 0 || ref.requestIndex >= len(m.savedRequests) || ref.exampleIndex < 0 || ref.exampleIndex >= len(m.savedRequests[ref.requestIndex].examples) {
		return
	}
	request := m.savedRequests[ref.requestIndex]
	example := request.examples[ref.exampleIndex]
	m.applySavedRequest(request)
	m.activeSavedIndex = ref.requestIndex
	m.collectionPos = ref.requestIndex
	m.response = example.responseBody
	m.responseRaw = example.responseRaw
	m.responseRawAvailable = example.responseRawAvailable
	m.setResponseHeaders(example.responseHeaders)
	m.responseMeta = example.responseMeta
	m.responseStatusCode = example.statusCode
	m.responseTests = "Saved example: " + example.name
	m.responseModel.SetContent(m.response)
	m.responseTestsModel.SetContent(m.responseTests)
}

func responseHeaderEntries(formatted string) []headerEntry {
	var entries []headerEntry
	for line := range strings.SplitSeq(formatted, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(name) != "" {
			entries = append(entries, headerEntry{key: strings.TrimSpace(name), value: strings.TrimSpace(value)})
		}
	}
	return entries
}

func formattedResponseHeaders(entries []headerEntry) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.key) != "" {
			lines = append(lines, entry.key+": "+entry.value)
		}
	}
	return strings.Join(lines, "\n")
}

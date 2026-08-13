package tui

import "slices"

type requestLoadKind int

const (
	requestLoadCollection requestLoadKind = iota
	requestLoadHistory
	requestLoadExample
)

type requestLoadTarget struct {
	kind    requestLoadKind
	index   int
	example savedExampleRef
}

func (m *model) markRequestDraftClean() {
	m.requestDraftBaseline = m.captureCurrentRequest()
}

func (m model) requestDraftDirty() bool {
	return !sameRequestDraft(m.captureCurrentRequest(), m.requestDraftBaseline)
}

func sameRequestDraft(left, right savedRequest) bool {
	return left.method == right.method &&
		left.url == right.url &&
		left.auth == right.auth &&
		slices.Equal(left.headers, right.headers) &&
		slices.Equal(left.params, right.params) &&
		sameBodyDraft(left.body, right.body) &&
		slices.Equal(left.cookies, right.cookies) &&
		slices.Equal(left.tests, right.tests)
}

func sameBodyDraft(left, right bodyConfig) bool {
	return left.mode == right.mode &&
		left.rawType == right.rawType &&
		left.raw == right.raw &&
		slices.Equal(left.form, right.form) &&
		slices.Equal(left.multipart, right.multipart) &&
		left.binaryPath == right.binaryPath &&
		left.graphqlQuery == right.graphqlQuery &&
		left.graphqlVariables == right.graphqlVariables &&
		left.graphqlOperationName == right.graphqlOperationName
}

func (m *model) loadRequestOrConfirm(target requestLoadTarget) {
	if m.requestDraftDirty() {
		m.requestLoadConfirmOpen = true
		m.pendingRequestLoad = target
		return
	}
	m.applyRequestLoad(target)
}

func (m *model) cancelPendingRequestLoad() {
	m.requestLoadConfirmOpen = false
	m.pendingRequestLoad = requestLoadTarget{}
}

func (m *model) confirmPendingRequestLoad() {
	target := m.pendingRequestLoad
	m.cancelPendingRequestLoad()
	m.applyRequestLoad(target)
}

func (m *model) applyRequestLoad(target requestLoadTarget) {
	switch target.kind {
	case requestLoadCollection:
		if target.index < 0 || target.index >= len(m.savedRequests) {
			return
		}
		m.collectionPos = target.index
		m.applySavedRequest(m.savedRequests[target.index])
		m.activeSavedIndex = target.index
		m.setFocus(paneURL)
	case requestLoadHistory:
		if target.index < 0 || target.index >= len(m.history) {
			return
		}
		m.historyPos = target.index
		m.applyHistoryItem(m.history[target.index])
		m.setFocus(paneURL)
	case requestLoadExample:
		ref := target.example
		if ref.requestIndex < 0 || ref.requestIndex >= len(m.savedRequests) ||
			ref.exampleIndex < 0 || ref.exampleIndex >= len(m.savedRequests[ref.requestIndex].examples) {
			return
		}
		m.applySavedExample(ref)
		m.setFocus(paneResponse)
	}
}

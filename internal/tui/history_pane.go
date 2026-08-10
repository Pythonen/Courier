package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
)

type sidebarMode int

const (
	sidebarHistory sidebarMode = iota
	sidebarCollections
	sidebarExamples
	sidebarCookies
	sidebarModeCount
)

func (m *model) handleHistoryKeys(keyStr string) {
	if m.sidebarMode == sidebarCookies {
		cookies := m.Cookies()
		switch keyStr {
		case "up", "k":
			m.cookiePendingD = false
			if m.cookiePos > 0 {
				m.cookiePos--
			}
		case "down", "j":
			m.cookiePendingD = false
			if m.cookiePos < len(cookies)-1 {
				m.cookiePos++
			}
		case "d":
			if m.cookiePendingD && len(cookies) > 0 {
				if jar := m.persistentJar(); jar != nil {
					jar.Delete(cookies[m.cookiePos])
				}
				if m.cookiePos >= len(cookies)-1 && m.cookiePos > 0 {
					m.cookiePos--
				}
				m.cookiePendingD = false
				m.responseMeta = "Deleted stored cookie"
				m.saveWorkspaceWithStatus()
			} else {
				m.cookiePendingD = true
			}
			return
		case "v":
			m.cookiePendingD = false
			m.cookieSecretsVisible = !m.cookieSecretsVisible
			return
		default:
			m.cookiePendingD = false
		}
		return
	}
	if m.sidebarMode == sidebarExamples {
		refs := m.savedExampleRefs()
		switch keyStr {
		case "up", "k":
			m.examplePendingD = false
			if m.examplePos > 0 {
				m.examplePos--
			}
		case "down", "j":
			m.examplePendingD = false
			if m.examplePos < len(refs)-1 {
				m.examplePos++
			}
		case "enter":
			m.examplePendingD = false
			if len(refs) > 0 {
				m.loadRequestOrConfirm(requestLoadTarget{kind: requestLoadExample, example: refs[m.examplePos]})
			}
			return
		case "d":
			if m.examplePendingD && len(refs) > 0 {
				ref := refs[m.examplePos]
				examples := m.savedRequests[ref.requestIndex].examples
				m.savedRequests[ref.requestIndex].examples = append(examples[:ref.exampleIndex], examples[ref.exampleIndex+1:]...)
				if m.examplePos >= len(refs)-1 && m.examplePos > 0 {
					m.examplePos--
				}
				m.examplePendingD = false
				m.responseMeta = "Deleted saved example"
				m.saveWorkspaceWithStatus()
			} else {
				m.examplePendingD = true
			}
			return
		case "r":
			m.examplePendingD = false
			if len(refs) > 0 {
				ref := refs[m.examplePos]
				m.collectionRenameOpen = true
				m.collectionRenameInput.SetValue(m.savedRequests[ref.requestIndex].examples[ref.exampleIndex].name)
				m.collectionRenameInput.CursorEnd()
				_ = m.collectionRenameInput.Focus()
			}
			return
		default:
			m.examplePendingD = false
		}
		return
	}

	switch m.sidebarMode {
	case sidebarCollections:
		switch keyStr {
		case "up", "k":
			m.collectionPendingD = false
			if m.collectionPos > 0 {
				m.collectionPos--
			}
		case "down", "j":
			m.collectionPendingD = false
			if m.collectionPos < len(m.savedRequests)-1 {
				m.collectionPos++
			}
		case "enter":
			m.collectionPendingD = false
			if len(m.savedRequests) > 0 {
				m.loadRequestOrConfirm(requestLoadTarget{kind: requestLoadCollection, index: m.collectionPos})
			}
			return
		case "d":
			if m.collectionPendingD && len(m.savedRequests) > 0 {
				deleted := m.collectionPos
				m.savedRequests = append(m.savedRequests[:m.collectionPos], m.savedRequests[m.collectionPos+1:]...)
				if m.activeSavedIndex == deleted {
					m.activeSavedIndex = -1
				} else if m.activeSavedIndex > deleted {
					m.activeSavedIndex--
				}
				if m.collectionPos >= len(m.savedRequests) && m.collectionPos > 0 {
					m.collectionPos--
				}
				m.collectionPendingD = false
				m.responseMeta = "Deleted saved request"
				m.saveWorkspaceWithStatus()
			} else {
				m.collectionPendingD = true
			}
			return
		case "r":
			m.collectionPendingD = false
			if len(m.savedRequests) > 0 {
				m.collectionRenameOpen = true
				m.collectionRenameInput.SetValue(m.savedRequests[m.collectionPos].name)
				m.collectionRenameInput.CursorEnd()
				_ = m.collectionRenameInput.Focus()
			}
			return
		case "c":
			m.collectionPendingD = false
			if len(m.savedRequests) > 0 {
				copyRequest := m.savedRequests[m.collectionPos].toWorkspace().fromWorkspace()
				copyRequest.name = m.uniqueSavedRequestCopyName(copyRequest.name)
				insertAt := m.collectionPos + 1
				m.savedRequests = append(m.savedRequests, savedRequest{})
				copy(m.savedRequests[insertAt+1:], m.savedRequests[insertAt:])
				m.savedRequests[insertAt] = copyRequest
				m.collectionPos = insertAt
				m.activeSavedIndex = insertAt
				m.applySavedRequest(copyRequest)
				m.responseMeta = "Duplicated saved request"
				m.saveWorkspaceWithStatus()
			}
			return
		default:
			m.collectionPendingD = false
		}
		return
	}

	switch keyStr {
	case "up", "k":
		m.historyPendingD = false
		if m.historyPos > 0 {
			m.historyPos--
		}
	case "down", "j":
		m.historyPendingD = false
		if m.historyPos < len(m.history)-1 {
			m.historyPos++
		}
	case "d":
		if m.historyPendingD && len(m.history) > 0 {
			m.history = append(m.history[:m.historyPos], m.history[m.historyPos+1:]...)
			if m.historyPos >= len(m.history) && m.historyPos > 0 {
				m.historyPos--
			}
			m.historyPendingD = false
			m.responseMeta = "Deleted history entry"
			m.saveWorkspaceWithStatus()
		} else {
			m.historyPendingD = true
		}
		return
	case "enter":
		m.historyPendingD = false
		if len(m.history) > 0 {
			m.loadRequestOrConfirm(requestLoadTarget{kind: requestLoadHistory, index: m.historyPos})
		}
		return
	default:
		m.historyPendingD = false
	}
}

func (m *model) applyHistoryItem(item historyItem) {
	m.clearResponseFilter()
	m.activeSavedIndex = -1
	m.requestId = item.requestID
	m.urlInput.SetValue(item.url)
	m.setBodyConfig(item.requestBodyConfig)
	m.headersInput.SetEntries(item.requestHeaders)
	m.paramsInput.SetEntries(item.requestParams)
	m.authInput.SetConfig(item.requestAuth)
	m.cookiesInput.SetEntries(item.requestCookies)
	m.testsInput.SetEntries(item.requestTests)
	m.response = item.responseBody
	m.responseRaw = item.responseRaw
	m.responseRawAvailable = item.responseRawAvailable
	m.responseStatusCode = 0
	m.responseHeaders = item.responseHeaders
	m.responseMeta = item.responseMeta
	m.responseTests = item.responseTests
	m.responseModel.SetContent(m.response)
	m.responseHeadersModel.SetContent(m.responseHeaders)
	m.responseTestsModel.SetContent(m.responseTests)
	m.setMethodForURL(item.method, item.url)
	m.markRequestDraftClean()
}

func (m model) viewHistory(contentHeight int) string {
	border := blurredBorder
	if m.focus == paneHistory {
		border = focusedBorder
	}

	labelText := "History"
	position := m.historyPos
	type sidebarItem struct{ method, label string }
	var sidebarItems []sidebarItem
	switch m.sidebarMode {
	case sidebarCollections:
		labelText = "Collections"
		position = m.collectionPos
		for _, request := range m.savedRequests {
			sidebarItems = append(sidebarItems, sidebarItem{method: request.method, label: request.displayName()})
		}
	case sidebarExamples:
		labelText = "Examples"
		position = m.examplePos
		for _, ref := range m.savedExampleRefs() {
			request := m.savedRequests[ref.requestIndex]
			example := request.examples[ref.exampleIndex]
			method := strconv.Itoa(example.statusCode)
			if example.statusCode == 0 {
				method = "RESP"
			}
			sidebarItems = append(sidebarItems, sidebarItem{method: method, label: request.displayName() + " / " + example.name})
		}
	case sidebarCookies:
		labelText = "Cookies"
		position = m.cookiePos
		for _, cookie := range m.Cookies() {
			value := cookie.Value
			if !m.cookieSecretsVisible {
				value = maskedSecretValue(value)
			}
			label := cookie.Name + "=" + value + " " + cookie.Domain + cookie.Path
			sidebarItems = append(sidebarItems, sidebarItem{method: "JAR", label: label})
		}
	default:
		for _, item := range m.history {
			sidebarItems = append(sidebarItems, sidebarItem{method: item.method, label: item.url})
		}
	}

	label := labelStyle.Render(labelText)
	if m.collectionRenameOpen {
		label = labelStyle.Render("Rename: ") + m.collectionRenameInput.View()
	} else if m.sidebarMode == sidebarCollections {
		label += hintStyle.Render("  r c dd")
	} else if m.sidebarMode == sidebarExamples {
		label += hintStyle.Render("  r dd")
	} else if m.sidebarMode == sidebarCookies {
		secretAction := "reveal"
		if m.cookieSecretsVisible {
			secretAction = "hide"
		}
		label += hintStyle.Render("  dd v:" + secretAction)
	} else {
		label += hintStyle.Render("  dd")
	}
	var items []string
	visible := contentHeight - 4
	if visible < 1 {
		visible = 1
	}
	start := 0
	if position >= visible {
		start = position - visible + 1
	}
	end := min(start+visible, len(sidebarItems))
	for i := start; i < end; i++ {
		item := sidebarItems[i]
		maxLabel := historyWidth - 12
		if maxLabel < 5 {
			maxLabel = 5
		}
		itemLabel := ansi.Truncate(item.label, maxLabel, "…")

		var line string
		if m.focus == paneHistory && i == position {
			line = lipgloss.NewStyle().
				Background(lipgloss.Color("57")).
				Foreground(lipgloss.Color("230")).
				Width(historyWidth - 2).
				Render(item.method + " " + itemLabel)
		} else {
			line = historyMethodStyle.Render(item.method) + historyItemStyle.Render(itemLabel)
		}
		items = append(items, line)
	}

	if len(items) == 0 {
		empty := "No history yet"
		switch m.sidebarMode {
		case sidebarCollections:
			empty = "No saved requests"
		case sidebarExamples:
			empty = "No saved examples"
		case sidebarCookies:
			empty = "No stored cookies"
		}
		items = append(items, historyItemStyle.Render(empty))
	}

	content := label + "\n" + strings.Join(items, "\n")
	return zone.Mark("history", border.
		Width(historyWidth).
		Height(contentHeight).
		Render(content))
}

func (m *model) uniqueSavedRequestCopyName(name string) string {
	base := strings.TrimSpace(name)
	if base == "" {
		base = "Saved request"
	}
	candidate := base + " copy"
	used := func(value string) bool {
		for _, request := range m.savedRequests {
			if request.name == value {
				return true
			}
		}
		return false
	}
	if !used(candidate) {
		return candidate
	}
	for suffix := 2; ; suffix++ {
		candidate = fmt.Sprintf("%s copy %d", base, suffix)
		if !used(candidate) {
			return candidate
		}
	}
}

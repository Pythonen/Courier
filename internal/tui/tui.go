package tui

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uuid "github.com/google/uuid"
	zone "github.com/lrstanley/bubblezone/v2"
)

const (
	helpHeight   = 3
	urlBarHeight = 3
	historyWidth = 28
	methodWidth  = 10
)

// Pane focus targets
type pane int

const (
	paneURL pane = iota
	paneRequest
	paneResponse
	paneHistory
	paneCount // sentinel for wrapping
)

var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

type historyItem struct {
	method          string
	url             string
	requestBody     string
	requestHeaders  []headerEntry
	responseBody    string
	responseHeaders string
	responseMeta    string
	requestID       uuid.UUID
}

type inputMode int

const (
	modeNormal inputMode = iota
	modeInsert
)

type keymap struct {
	next, prev, send, cycleMethod, quit key.Binding
}

type model struct {
	width  int
	height int
	client *http.Client

	urlInput             textinput.Model
	methodIdx            int
	bodyInput            textarea.Model
	headersInput         headersTable
	responseHeadersModel viewport.Model
	responseHeaders      string
	responseMeta         string
	responseTab          responseTab
	requestTab           requestTab
	history              []historyItem // TODO: Would be nice to have this as map
	historyPos           int
	responseModel        viewport.Model
	response             string
	requestId            uuid.UUID

	focus     pane
	inputMode inputMode
	keymap    keymap
	help      help.Model
}

func NewModel() model {
	ti := textinput.New()
	ti.Placeholder = "https://api.example.com/endpoint"
	ti.CharLimit = 2048
	ti.Focus()

	ta := textarea.New()
	ta.Placeholder = `{"key": "value"}`
	ta.ShowLineNumbers = true
	ta.Prompt = ""
	ta.Blur()

	m := model{
		client:       &http.Client{Timeout: 30 * time.Second},
		urlInput:     ti,
		bodyInput:    ta,
		headersInput: newHeadersTable(),
		responseModel: viewport.New(
			viewport.WithWidth(0),
			viewport.WithHeight(0),
		),
		responseHeadersModel: viewport.New(
			viewport.WithWidth(0),
			viewport.WithHeight(0),
		),
		history:   []historyItem{},
		focus:     paneURL,
		inputMode: modeNormal,
		help:      help.New(),
		keymap: keymap{
			next: key.NewBinding(
				key.WithKeys("tab"),
				key.WithHelp("tab", "next pane"),
			),
			prev: key.NewBinding(
				key.WithKeys("shift+tab"),
				key.WithHelp("shift+tab", "prev pane"),
			),
			send: key.NewBinding(
				key.WithKeys("ctrl+s", "enter"),
				key.WithHelp("ctrl+s/enter", "send request"),
			),
			cycleMethod: key.NewBinding(
				key.WithKeys("ctrl+o"),
				key.WithHelp("ctrl+o", "cycle method"),
			),
			quit: key.NewBinding(
				key.WithKeys("ctrl+c"),
				key.WithHelp("ctrl+c", "quit"),
			),
		},
	}
	return m
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case responseMsg:
		for i := range m.history {
			if m.history[i].requestID == msg.requestID {
				m.history[i].responseBody = msg.responseBody
				m.history[i].responseHeaders = msg.responseHeaders
				m.history[i].responseMeta = msg.responseMeta
				break
			}
		}
		// A slower, older request must not replace the currently selected response.
		if msg.requestID == m.requestId {
			m.responseModel.SetContent(msg.responseBody)
			m.response = msg.responseBody
			m.responseHeadersModel.SetContent(msg.responseHeaders)
			m.responseHeaders = msg.responseHeaders
			m.responseMeta = msg.responseMeta
		}

	case tea.KeyMsg:
		inInsert := m.focus == paneRequest && m.inputMode == modeInsert

		switch {
		case key.Matches(msg, m.keymap.quit):
			return m, tea.Quit

		case !inInsert && key.Matches(msg, m.keymap.next):
			m.setFocus((m.focus + 1) % paneCount)

		case !inInsert && key.Matches(msg, m.keymap.prev):
			m.setFocus((m.focus - 1 + paneCount) % paneCount)

		case !inInsert && key.Matches(msg, m.keymap.cycleMethod):
			m.methodIdx = (m.methodIdx + 1) % len(methods)

		case !inInsert && key.Matches(msg, m.keymap.send) && (msg.String() != "enter" || m.focus == paneURL):
			method := methods[m.methodIdx]
			url := m.urlInput.Value()
			if url != "" {
				m.responseModel.SetContent(fmt.Sprintf("Sending request %s %s ...", method, url))
				requestBody := m.bodyInput.Value()
				requestId := uuid.New()
				m.requestId = requestId
				m.historyPos = 0
				m.responseMeta = "Sending..."
				m.history = append([]historyItem{{
					method:         method,
					url:            url,
					requestBody:    requestBody,
					requestHeaders: m.headersInput.Entries(),
					requestID:      requestId,
				}}, m.history...)
				return m, m.DoRequest()
			}

		default:
			switch m.focus {
			case paneURL:
				var cmd tea.Cmd
				m.urlInput, cmd = m.urlInput.Update(msg)
				cmds = append(cmds, cmd)
			case paneRequest:
				keyStr := msg.String()
				if m.inputMode == modeInsert {
					if keyStr == "esc" {
						m.inputMode = modeNormal
						m.bodyInput.Blur()
						m.headersInput.blurAll()
					} else if m.requestTab == requestTabHeaders {
						cmd := m.headersInput.UpdateInsert(msg)
						cmds = append(cmds, cmd)
					} else if m.requestTab == requestTabBody {
						var cmd tea.Cmd
						m.bodyInput, cmd = m.bodyInput.Update(msg)
						cmds = append(cmds, cmd)
					}
				} else {
					// Normal mode
					switch keyStr {
					case "i":
						m.inputMode = modeInsert
						switch m.requestTab {
						case requestTabHeaders:
							cmd := m.headersInput.FocusCurrent()
							cmds = append(cmds, cmd)
						case requestTabBody:
							m.bodyInput.Focus()
						}
					case "left", "right":
						m.handleRequestKeys(keyStr)
						m.syncRequestTabFocus()
					default:
						if m.requestTab == requestTabHeaders {
							m.headersInput.UpdateNormal(keyStr)
						}
					}
				}
			case paneHistory:
				m.handleHistoryKeys(msg.String())
			case paneResponse:
				if !m.handleResponseKeys(msg.String()) {
					var cmd tea.Cmd
					if m.responseTab == responseTabHeaders {
						m.responseHeadersModel, cmd = m.responseHeadersModel.Update(msg)
					} else {
						m.responseModel, cmd = m.responseModel.Update(msg)
					}
					cmds = append(cmds, cmd)
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sizeComponents()

	case tea.MouseReleaseMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}

		if zone.Get("method").InBounds(msg) {
			m.setFocus(paneURL)
			m.methodIdx = (m.methodIdx + 1) % len(methods)
		} else if zone.Get("url").InBounds(msg) {
			m.setFocus(paneURL)
		} else if zone.Get("bodyTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabBody
			m.syncRequestTabFocus()
		} else if zone.Get("headersTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabHeaders
			m.syncRequestTabFocus()
		} else if zone.Get("authTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabAuth
			m.syncRequestTabFocus()
		} else if zone.Get("paramsTab").InBounds(msg) {
			m.setFocus(paneRequest)
			m.requestTab = requestTabParams
			m.syncRequestTabFocus()
		} else if zone.Get("request").InBounds(msg) {
			m.setFocus(paneRequest)
			m.syncRequestTabFocus()
			// TODO: Ability to click individual history items
		} else if zone.Get("history").InBounds(msg) {
			m.setFocus(paneHistory)
		} else if zone.Get("responseTabBody").InBounds(msg) {
			m.setFocus(paneResponse)
			m.responseTab = responseTabBody
			m.resetActiveResponseXOffset()
		} else if zone.Get("responseTabHeaders").InBounds(msg) {
			m.setFocus(paneResponse)
			m.responseTab = responseTabHeaders
			m.resetActiveResponseXOffset()
		} else if zone.Get("response").InBounds(msg) {
			m.setFocus(paneResponse)
		}

		return m, nil
	}

	// Forward non-key messages (like blink ticks) to focused input.
	if _, isKey := msg.(tea.KeyMsg); !isKey {
		if m.focus == paneURL {
			var cmd tea.Cmd
			m.urlInput, cmd = m.urlInput.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.focus == paneRequest && m.inputMode == modeInsert {
			switch m.requestTab {
			case requestTabBody:
				var cmd tea.Cmd
				m.bodyInput, cmd = m.bodyInput.Update(msg)
				cmds = append(cmds, cmd)
			case requestTabHeaders:
				cmd := m.headersInput.UpdateInsert(msg)
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *model) setFocus(p pane) {
	m.focus = p
	m.inputMode = modeNormal

	if p == paneURL {
		m.urlInput.Focus()
	} else {
		m.urlInput.Blur()
	}

	// When entering request pane, start in normal mode (nothing focused).
	// When leaving, blur everything.
	m.bodyInput.Blur()
	m.headersInput.Blur()

	if p == paneRequest {
		m.headersInput.Focus()
		m.syncRequestTabFocus()
	}
}

func (m *model) syncRequestTabFocus() {
	m.headersInput.Blur()
	m.bodyInput.Blur()
	if m.requestTab == requestTabHeaders {
		m.headersInput.Focus()
	}
}

func (m *model) sizeComponents() {
	mainWidth, _, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)

	m.urlInput.SetWidth(mainWidth - methodWidth - 4)

	m.bodyInput.SetWidth(mainWidth - 2)
	m.bodyInput.SetHeight(bodyHeight)
	m.headersInput.SetWidth(mainWidth - 4)
	m.headersInput.SetHeight(bodyHeight)

	// Persist viewport geometry on the model. View has a value receiver, so
	// sizing these inside viewResponse only changes a temporary copy and causes
	// scrolling to calculate offsets with a zero-height viewport.
	viewportHeight := responseHeight - 4 // border frame, tab bar, and top padding
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	viewportWidth := mainWidth - 3 // border frame and left content padding
	if viewportWidth < 1 {
		viewportWidth = 1
	}
	m.responseModel.SetWidth(viewportWidth)
	m.responseModel.SetHeight(viewportHeight)
	m.responseHeadersModel.SetWidth(viewportWidth)
	m.responseHeadersModel.SetHeight(viewportHeight)
}

func layoutDimensions(width, height int) (mainWidth, contentHeight, bodyHeight, responseHeight int) {
	mainWidth = width - historyWidth - 4
	if mainWidth < 20 {
		mainWidth = 20
	}

	contentHeight = height - helpHeight - 2
	bodyHeight = (contentHeight - urlBarHeight - 2) / 2
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	requestSectionHeight := bodyHeight + 2 // bordered content plus custom bottom border
	responseHeight = contentHeight - urlBarHeight - requestSectionHeight
	if responseHeight < 3 {
		responseHeight = 3
	}
	return mainWidth, contentHeight, bodyHeight, responseHeight
}

func (m model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.ReportFocus = true
	v.MouseMode = tea.MouseModeCellMotion

	if m.width == 0 {
		v.SetContent("Loading...")
		return v
	}

	mainWidth, contentHeight, bodyHeight, responseHeight := layoutDimensions(m.width, m.height)

	// Render each pane via its own file's method
	urlSection := zone.Mark("url", m.viewURL(mainWidth))

	requestSection := m.viewRequest(mainWidth, bodyHeight)
	responseSection := m.viewResponse(mainWidth, responseHeight)

	rightCol := lipgloss.JoinVertical(lipgloss.Left, urlSection, requestSection, responseSection)
	historySection := m.viewHistory(contentHeight)

	layout := lipgloss.JoinHorizontal(lipgloss.Top, historySection, rightCol)

	helpView := helpStyle.Render(m.help.ShortHelpView([]key.Binding{
		m.keymap.next,
		m.keymap.prev,
		m.keymap.cycleMethod,
		m.keymap.send,
		m.keymap.quit,
	}))

	v.SetContent(zone.Scan(layout + "\n" + helpView))
	return v
}

// TODO: We have to either wrap the lines or make the viewport scrollable sideways
func formatHeaders(h http.Header) string {
	if len(h) == 0 {
		return "(no headers)"
	}

	// Sort keys for consistent output
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	return b.String()
}

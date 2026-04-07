package main

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

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
	method             string
	url                string
	responseComponents map[string]string // TODO: This might have to be something else down the line...
	requestComponents  map[string]string // Body, params, auth, headers
	requestId          uuid.UUID
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

	urlInput             textinput.Model
	methodIdx            int
	bodyInput            textarea.Model
	headersInput         headersTable
	responseHeadersModel viewport.Model
	responseHeaders      string
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

func newModel() model {
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
				key.WithKeys("ctrl+s"),
				key.WithHelp("ctrl+s", "send request"),
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
		m.responseModel.SetContent(msg.responseBody)
		m.response = msg.responseBody
		m.responseHeadersModel.SetContent(msg.responseHeaders)
		m.responseHeaders = msg.responseHeaders
		if len(m.history) > 0 {
			// TODO: This is a bit meh, let's see if we can refactor history to be a map
			for _, n := range m.history {
				if n.requestId == m.requestId {
					n.responseComponents["body"] = msg.responseBody
					n.responseComponents["headers"] = msg.responseHeaders
					break
				}
			}
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

		case !inInsert && key.Matches(msg, m.keymap.send):
			method := methods[m.methodIdx]
			url := m.urlInput.Value()
			if url != "" {
				m.responseModel.SetContent(fmt.Sprintf("Sending request %s %s ...", method, url))
				requestComponents := make(map[string]string)
				// TODO: populate auth and params as well when the panes are implemented
				requestBody := m.bodyInput.Value()
				requestComponents["body"] = requestBody
				requestId := uuid.New()
				responseComponents := make(map[string]string)
				responseComponents["body"] = ""
				responseComponents["headers"] = ""
				m.requestId = requestId
				m.history = append([]historyItem{{method: method, url: url, requestComponents: requestComponents, requestId: requestId, responseComponents: responseComponents}}, m.history...)
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
				m.handleResponseKeys(msg.String())
				var cmd tea.Cmd
				m.responseModel, cmd = m.responseModel.Update(msg)
				cmds = append(cmds, cmd)

				m.responseHeadersModel, cmd = m.responseHeadersModel.Update(msg)
				cmds = append(cmds, cmd)
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sizeComponents()
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
	mainWidth := m.width - historyWidth - 4
	if mainWidth < 20 {
		mainWidth = 20
	}

	m.urlInput.SetWidth(mainWidth - methodWidth - 4)

	bodyHeight := (m.height - urlBarHeight - helpHeight - 6) / 2
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	m.bodyInput.SetWidth(mainWidth - 2)
	m.bodyInput.SetHeight(bodyHeight)
	m.headersInput.SetWidth(mainWidth - 4)
	m.headersInput.SetHeight(bodyHeight)
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

	mainWidth := m.width - historyWidth - 4
	if mainWidth < 20 {
		mainWidth = 20
	}

	contentHeight := m.height - helpHeight - 2

	// Render each pane via its own file's method
	urlSection := m.viewURL(mainWidth)

	bodyHeight := (contentHeight - lipgloss.Height(urlSection) - 2) / 2
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	requestSection := m.viewRequest(mainWidth, bodyHeight)

	responseHeight := contentHeight - lipgloss.Height(urlSection) - lipgloss.Height(requestSection)
	if responseHeight < 3 {
		responseHeight = 3
	}
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

func main() {
	zone.NewGlobal()
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Println("Error while running program:", err)
		os.Exit(1)
	}
}

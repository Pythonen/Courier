package tui

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"

	uuid "github.com/google/uuid"
)

const defaultEnvironmentName = "Default"

type environmentProfile struct {
	name   string
	values []headerEntry
}

var variablePattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

type variableResolver struct {
	values  map[string]string
	dynamic map[string]string
}

func newVariablesTable() headersTable {
	table := newKeyValueTable("Variable", "Value", "variable-name", "value")
	table.maskAllValues = true
	return table
}

func (m *model) ensureEnvironmentProfiles() {
	if len(m.environments) == 0 {
		m.environments = []environmentProfile{{name: defaultEnvironmentName}}
		m.environmentPos = 0
	}
	if m.environmentPos < 0 || m.environmentPos >= len(m.environments) {
		m.environmentPos = 0
	}
}

func (m *model) syncActiveEnvironment() {
	m.ensureEnvironmentProfiles()
	m.environments[m.environmentPos].values = m.variablesInput.Entries()
}

func (m *model) activateEnvironmentIndex(index int) {
	m.syncActiveEnvironment()
	if index < 0 || index >= len(m.environments) {
		return
	}
	m.environmentPos = index
	m.variablesInput.SetEntries(m.environments[index].values)
}

// ActivateEnvironment selects a named local environment for TUI or runner use.
func (m *model) ActivateEnvironment(name string) error {
	m.syncActiveEnvironment()
	for index := range m.environments {
		if m.environments[index].name == name {
			m.environmentPos = index
			m.variablesInput.SetEntries(m.environments[index].values)
			return nil
		}
	}
	return fmt.Errorf("environment %q was not found", name)
}

func (m *model) activeEnvironmentName() string {
	m.ensureEnvironmentProfiles()
	return m.environments[m.environmentPos].name
}

func (m *model) environmentNameAvailable(name string, except int) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for index, environment := range m.environments {
		if index != except && environment.name == name {
			return false
		}
	}
	return true
}

func (m *model) nextEnvironmentName() string {
	for suffix := 1; ; suffix++ {
		name := fmt.Sprintf("Environment %d", suffix)
		if m.environmentNameAvailable(name, -1) {
			return name
		}
	}
}

func newVariableResolver(entries []headerEntry) *variableResolver {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		values[strings.TrimSpace(entry.key)] = entry.value
	}
	return &variableResolver{values: values, dynamic: map[string]string{}}
}

func (r *variableResolver) Resolve(input string) string {
	return variablePattern.ReplaceAllStringFunc(input, func(match string) string {
		parts := variablePattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		name := strings.TrimSpace(parts[1])
		if value, ok := r.values[name]; ok {
			return value
		}
		if value, ok := r.dynamic[name]; ok {
			return value
		}
		value, ok := dynamicVariable(name)
		if !ok {
			return match
		}
		r.dynamic[name] = value
		return value
	})
}

func dynamicVariable(name string) (string, bool) {
	switch name {
	case "$guid", "$randomUUID":
		return uuid.NewString(), true
	case "$timestamp":
		return strconv.FormatInt(time.Now().Unix(), 10), true
	case "$isoTimestamp":
		return time.Now().UTC().Format(time.RFC3339), true
	case "$randomInt":
		return strconv.Itoa(rand.IntN(1001)), true
	default:
		return "", false
	}
}

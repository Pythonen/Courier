package tui

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadRunData reads Postman-style iteration variables from a JSON array of
// objects or a CSV file with a header row.
func LoadRunData(path string) ([]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open runner data: %w", err)
	}
	defer file.Close() //nolint:errcheck

	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return decodeJSONRunData(file)
	case ".csv":
		return decodeCSVRunData(file)
	default:
		return nil, fmt.Errorf("runner data must use a .json or .csv extension")
	}
}

func decodeJSONRunData(reader io.Reader) ([]map[string]string, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	var input []map[string]any
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode JSON runner data: %w", err)
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("JSON runner data contains no rows")
	}
	rows := make([]map[string]string, 0, len(input))
	for index, inputRow := range input {
		row := make(map[string]string, len(inputRow))
		for key, value := range inputRow {
			key = strings.TrimSpace(key)
			if key == "" {
				return nil, fmt.Errorf("JSON runner data row %d contains an empty variable name", index+1)
			}
			text, err := runDataValue(value)
			if err != nil {
				return nil, fmt.Errorf("JSON runner data row %d variable %q: %w", index+1, key, err)
			}
			row[key] = text
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func runDataValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	case json.Number:
		return typed.String(), nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

func decodeCSVRunData(reader io.Reader) ([]map[string]string, error) {
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1
	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("decode CSV runner data: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("CSV runner data must contain a header and at least one data row")
	}
	headings := records[0]
	seen := make(map[string]struct{}, len(headings))
	for index := range headings {
		headings[index] = strings.TrimSpace(strings.TrimPrefix(headings[index], "\ufeff"))
		if headings[index] == "" {
			return nil, fmt.Errorf("CSV runner data contains an empty column name")
		}
		if _, exists := seen[headings[index]]; exists {
			return nil, fmt.Errorf("CSV runner data contains duplicate column %q", headings[index])
		}
		seen[headings[index]] = struct{}{}
	}
	rows := make([]map[string]string, 0, len(records)-1)
	for index, record := range records[1:] {
		if len(record) != len(headings) {
			return nil, fmt.Errorf("CSV runner data row %d has %d values; expected %d", index+2, len(record), len(headings))
		}
		row := make(map[string]string, len(headings))
		for column, heading := range headings {
			row[heading] = record[column]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

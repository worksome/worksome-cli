package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/rodaine/table"
	"golang.org/x/term"
)

// Format controls how output is rendered.
type Format string

const (
	FormatJSON  Format = "json"
	FormatTable Format = "table"
)

// Column describes a single table column: header text, the JSON field path
// used to extract values from data, and an optional fixed width.
type Column struct {
	Header string
	Field  string
	Width  int
}

// Formatter writes structured data as either JSON or an ASCII table.
type Formatter struct {
	format  Format
	noColor bool
	writer  io.Writer
}

// New creates a Formatter with an explicit format choice.
func New(w io.Writer, format Format, noColor bool) *Formatter {
	return &Formatter{
		format:  format,
		noColor: noColor,
		writer:  w,
	}
}

// Auto creates a Formatter that picks the format automatically:
// table when the writer is a terminal, JSON otherwise.
func Auto(w io.Writer, noColor bool) *Formatter {
	f := FormatJSON
	if isTTY(w) {
		f = FormatTable
	}

	return &Formatter{
		format:  f,
		noColor: noColor,
		writer:  w,
	}
}

// Format returns the configured output format.
func (f *Formatter) Format() Format {
	return f.format
}

// Print dispatches to PrintJSON or PrintTable based on the configured format.
func (f *Formatter) Print(data any, columns []Column) error {
	switch f.format {
	case FormatTable:
		return f.PrintTable(data, columns)
	default:
		return f.PrintJSON(data)
	}
}

// PrintJSON marshals data as indented JSON and writes it to the output writer.
func (f *Formatter) PrintJSON(data any) error {
	enc := json.NewEncoder(f.writer)
	enc.SetIndent("", "  ")

	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}

	return nil
}

// PrintTable renders data as an ASCII table using the provided column
// definitions. data must be a slice/array or a single object.
func (f *Formatter) PrintTable(data any, columns []Column) error {
	rows := ExtractFields(data, columns)

	headers := make([]interface{}, len(columns))
	for i, c := range columns {
		headers[i] = c.Header
	}

	tbl := table.New(headers...)
	tbl.WithWriter(f.writer)

	for _, row := range rows {
		vals := make([]interface{}, len(row))
		for i, v := range row {
			vals[i] = v
		}
		tbl.AddRow(vals...)
	}

	tbl.Print()

	return nil
}

// ExtractFields pulls column values out of data for each row.
// data may be a slice/array (one row per element) or a single
// map[string]any / struct (treated as one row).
func ExtractFields(data any, columns []Column) [][]string {
	if data == nil {
		return nil
	}

	v := reflect.ValueOf(data)

	// Dereference pointer.
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	// If it's a slice/array, iterate over elements.
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var rows [][]string
		for i := 0; i < v.Len(); i++ {
			rows = append(rows, extractRow(v.Index(i).Interface(), columns))
		}
		return rows
	}

	// Single object: one row.
	return [][]string{extractRow(data, columns)}
}

// extractRow extracts column values from a single item.
func extractRow(item any, columns []Column) []string {
	row := make([]string, len(columns))

	if item == nil {
		return row
	}

	v := reflect.ValueOf(item)
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return row
		}
		v = v.Elem()
	}

	for i, col := range columns {
		row[i] = resolveField(v, col.Field)
	}

	return row
}

// resolveField walks a dot-separated field path against a value and returns
// a string representation. Supports both map[string]any and struct types.
func resolveField(v reflect.Value, path string) string {
	parts := strings.Split(path, ".")

	cur := v
	for _, part := range parts {
		// Dereference pointers/interfaces along the way.
		for cur.Kind() == reflect.Ptr || cur.Kind() == reflect.Interface {
			if cur.IsNil() {
				return ""
			}
			cur = cur.Elem()
		}

		switch cur.Kind() {
		case reflect.Map:
			cur = cur.MapIndex(reflect.ValueOf(part))
			if !cur.IsValid() {
				return ""
			}
		case reflect.Struct:
			cur = fieldByJSONTag(cur, part)
			if !cur.IsValid() {
				return ""
			}
		default:
			return ""
		}
	}

	// Final dereference.
	for cur.Kind() == reflect.Ptr || cur.Kind() == reflect.Interface {
		if cur.IsNil() {
			return ""
		}
		cur = cur.Elem()
	}

	return fmt.Sprintf("%v", cur.Interface())
}

// fieldByJSONTag finds a struct field whose json tag matches name, falling
// back to a case-insensitive field name match.
func fieldByJSONTag(v reflect.Value, name string) reflect.Value {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("json")
		tagName := strings.SplitN(tag, ",", 2)[0]

		if tagName == name {
			return v.Field(i)
		}
	}

	// Fallback: match exported field name case-insensitively.
	return v.FieldByNameFunc(func(n string) bool {
		return strings.EqualFold(n, name)
	})
}

// isTTY returns true when w is backed by a terminal file descriptor.
func isTTY(w io.Writer) bool {
	type fder interface {
		Fd() uintptr
	}

	if f, ok := w.(fder); ok {
		return term.IsTerminal(int(f.Fd()))
	}

	return false
}

// IsTTYFile is a convenience wrapper: returns true when f is a terminal.
func IsTTYFile(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// FilterFields filters data to include only the specified field paths.
// It accepts map[string]any, []any, or []map[string]any data structures.
// Field paths use dot notation for nested fields (e.g., "worker.name").
// If fields is empty, data is returned unchanged.
func FilterFields(data any, fields []string) any {
	if len(fields) == 0 {
		return data
	}

	// Normalise field paths — trim whitespace.
	trimmed := make([]string, len(fields))
	for i, f := range fields {
		trimmed[i] = strings.TrimSpace(f)
	}

	switch v := data.(type) {
	case map[string]any:
		return filterMap(v, trimmed)
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			if m, ok := item.(map[string]any); ok {
				result[i] = filterMap(m, trimmed)
			} else {
				result[i] = item
			}
		}
		return result
	case []map[string]any:
		result := make([]any, len(v))
		for i, m := range v {
			result[i] = filterMap(m, trimmed)
		}
		return result
	default:
		return data
	}
}

// filterMap returns a new map containing only the keys/paths listed in fields.
func filterMap(m map[string]any, fields []string) map[string]any {
	out := make(map[string]any)
	for _, path := range fields {
		parts := strings.SplitN(path, ".", 2)
		key := parts[0]

		val, ok := m[key]
		if !ok {
			continue
		}

		if len(parts) == 1 {
			// Leaf field — copy the value directly.
			out[key] = val
		} else {
			// Nested path — recurse into child map.
			child, ok := val.(map[string]any)
			if !ok {
				continue
			}
			// Merge with any previously filtered content for this key.
			existing, _ := out[key].(map[string]any)
			if existing == nil {
				existing = make(map[string]any)
			}
			filtered := filterMap(child, []string{parts[1]})
			for k, v := range filtered {
				existing[k] = v
			}
			out[key] = existing
		}
	}
	return out
}

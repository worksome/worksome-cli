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
	format Format
	color  bool
	writer io.Writer
}

// New creates a Formatter with an explicit format choice.
func New(w io.Writer, format Format, noColor bool) *Formatter {
	return &Formatter{
		format: format,
		color:  colorEnabled(w, noColor),
		writer: w,
	}
}

// Auto creates a Formatter that picks the format automatically:
// table when the writer is a terminal, JSON otherwise.
func Auto(w io.Writer, noColor bool) *Formatter {
	f := FormatJSON
	if isTerminal(w) {
		f = FormatTable
	}

	return &Formatter{
		format: f,
		color:  colorEnabled(w, noColor),
		writer: w,
	}
}

// isTerminal detects whether a writer is a TTY; a variable so tests can stub it.
var isTerminal = isTTY

// colorEnabled reports whether ANSI color should be used: only when writing
// to a terminal, and neither --no-color nor the NO_COLOR env var disables it.
func colorEnabled(w io.Writer, noColor bool) bool {
	return !noColor && os.Getenv("NO_COLOR") == "" && isTerminal(w)
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
	if f.color {
		tbl.WithHeaderFormatter(boldHeaderFormatter)
	}

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

// boldHeaderFormatter renders table headers in bold using ANSI escape codes.
// The reset goes before the trailing newline so it stays on the header line.
func boldHeaderFormatter(format string, vals ...interface{}) string {
	s := fmt.Sprintf(format, vals...)
	nl := ""
	if strings.HasSuffix(s, "\n") {
		s, nl = strings.TrimSuffix(s, "\n"), "\n"
	}
	return "\x1b[1m" + s + "\x1b[0m" + nl
}

// ExtractFields pulls column values out of data for each row.
// data may be a slice/array (one row per element) or a single
// map[string]any / struct (treated as one row).
func ExtractFields(data any, columns []Column) [][]string {
	if data == nil {
		return nil
	}

	// Unwrap single-key maps whose value is a slice.
	// This handles responses like {"accounts": [{...}, {...}]}.
	if m, ok := data.(map[string]any); ok && len(m) == 1 {
		for _, val := range m {
			if reflect.TypeOf(val) != nil {
				rv := reflect.ValueOf(val)
				if rv.Kind() == reflect.Slice {
					data = val
				}
			}
		}
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
	return resolvePath(v, strings.Split(path, "."))
}

// resolvePath walks the remaining path segments against a value. A segment that
// lands on a list is applied to every element and the results joined — otherwise
// the column would render blank, which reads as "no data" rather than "unsupported".
func resolvePath(v reflect.Value, parts []string) string {
	cur := v
	for i, part := range parts {
		// Dereference pointers/interfaces along the way.
		for cur.Kind() == reflect.Ptr || cur.Kind() == reflect.Interface {
			if cur.IsNil() {
				return ""
			}
			cur = cur.Elem()
		}

		if cur.Kind() == reflect.Slice || cur.Kind() == reflect.Array {
			var vals []string
			for j := 0; j < cur.Len(); j++ {
				if s := resolvePath(cur.Index(j), parts[i:]); s != "" {
					vals = append(vals, s)
				}
			}
			return strings.Join(vals, ", ")
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

// FilterColumns returns only the columns whose Field matches one of the
// comma-separated names in selected. If selected is empty, all columns are
// returned unchanged. The result preserves the order from selected.
func FilterColumns(columns []Column, selected string) []Column {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return columns
	}

	names := strings.Split(selected, ",")

	// Build a lookup from field name to column.
	lookup := make(map[string]Column, len(columns))
	for _, c := range columns {
		lookup[c.Field] = c
	}

	var result []Column
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if c, ok := lookup[name]; ok {
			result = append(result, c)
		}
	}

	return result
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

	// Operations whose root field is a plain list arrive as the response
	// envelope — {"accounts": [...]} — because only single objects and
	// paginated "data" are unwrapped upstream. Resolving the paths against
	// that envelope matches nothing and prints {}. Resolve them against the
	// list instead, as ExtractFields does for tables, but keep the wrapper so
	// the JSON shape callers already pipe into (`.accounts[]`) is unchanged.
	if m, ok := data.(map[string]any); ok && len(m) == 1 {
		for key, val := range m {
			if list, ok := val.([]any); ok && !selectsKey(trimmed, key) {
				return map[string]any{key: filterValue(list, trimmed)}
			}
		}
	}

	return filterValue(data, trimmed)
}

// selectsKey reports whether any field path starts at key — in which case the
// envelope itself is what the caller means to filter.
func selectsKey(fields []string, key string) bool {
	for _, f := range fields {
		if head, _, _ := strings.Cut(f, "."); head == key {
			return true
		}
	}
	return false
}

// filterMap returns a new map containing only the keys/paths listed in fields.
func filterMap(m map[string]any, fields []string) map[string]any {
	// Group subpaths by their leading key so "owners.name,owners.email" is
	// applied in a single pass rather than merged after the fact.
	var order []string
	seen := make(map[string]bool)
	leaf := make(map[string]bool)
	subpaths := make(map[string][]string)

	for _, path := range fields {
		parts := strings.SplitN(path, ".", 2)
		key := parts[0]
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
		if len(parts) == 1 {
			leaf[key] = true
			continue
		}
		subpaths[key] = append(subpaths[key], parts[1])
	}

	out := make(map[string]any)
	for _, key := range order {
		val, ok := m[key]
		if !ok {
			continue
		}
		// Selecting the key itself wins over any subpath for it.
		if leaf[key] {
			out[key] = val
			continue
		}
		out[key] = filterValue(val, subpaths[key])
	}
	return out
}

// filterValue applies nested field paths to a value, descending into maps and
// through lists. A value with no sub-structure to filter is returned unchanged
// rather than dropped — a silently missing field yields a report that is
// quietly wrong instead of one that fails.
func filterValue(val any, fields []string) any {
	switch v := val.(type) {
	case map[string]any:
		return filterMap(v, fields)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = filterValue(item, fields)
		}
		return out
	case []map[string]any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = filterMap(item, fields)
		}
		return out
	default:
		return val
	}
}

package output

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestPrintJSON_map(t *testing.T) {
	var buf bytes.Buffer
	f := New(&buf, FormatJSON, true)

	data := map[string]any{
		"name": "Alice",
		"age":  30,
	}

	if err := f.PrintJSON(data); err != nil {
		t.Fatalf("PrintJSON returned error: %v", err)
	}

	// Should be valid, indented JSON.
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if decoded["name"] != "Alice" {
		t.Errorf("expected name=Alice, got %v", decoded["name"])
	}

	// Verify indentation.
	if !strings.Contains(buf.String(), "  ") {
		t.Error("expected indented JSON output")
	}
}

func TestPrintJSON_slice(t *testing.T) {
	var buf bytes.Buffer
	f := New(&buf, FormatJSON, true)

	data := []map[string]any{
		{"id": 1, "name": "A"},
		{"id": 2, "name": "B"},
	}

	if err := f.PrintJSON(data); err != nil {
		t.Fatalf("PrintJSON returned error: %v", err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if len(decoded) != 2 {
		t.Fatalf("expected 2 items, got %d", len(decoded))
	}
}

func TestPrintTable_basic(t *testing.T) {
	var buf bytes.Buffer
	f := New(&buf, FormatTable, true)

	data := []map[string]any{
		{"name": "Alice", "role": "admin"},
		{"name": "Bob", "role": "user"},
	}

	cols := []Column{
		{Header: "Name", Field: "name"},
		{Header: "Role", Field: "role"},
	}

	if err := f.PrintTable(data, cols); err != nil {
		t.Fatalf("PrintTable returned error: %v", err)
	}

	out := buf.String()

	// Table should include headers and data.
	if !strings.Contains(out, "Name") {
		t.Error("expected table to contain header 'Name'")
	}
	if !strings.Contains(out, "Role") {
		t.Error("expected table to contain header 'Role'")
	}
	if !strings.Contains(out, "Alice") {
		t.Error("expected table to contain 'Alice'")
	}
	if !strings.Contains(out, "Bob") {
		t.Error("expected table to contain 'Bob'")
	}
}

// stubTTY forces terminal detection for the duration of the test.
func stubTTY(t *testing.T, tty bool) {
	t.Helper()
	orig := isTerminal
	isTerminal = func(io.Writer) bool { return tty }
	t.Cleanup(func() { isTerminal = orig })
}

func TestNew_colorEnabled(t *testing.T) {
	tests := []struct {
		name       string
		tty        bool
		noColor    bool
		noColorEnv string
		want       bool
	}{
		{name: "TTY with color", tty: true, want: true},
		{name: "TTY suppressed by --no-color", tty: true, noColor: true, want: false},
		{name: "TTY suppressed by NO_COLOR env", tty: true, noColorEnv: "1", want: false},
		{name: "non-TTY writer never colors", tty: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColorEnv)
			stubTTY(t, tt.tty)

			var buf bytes.Buffer
			f := New(&buf, FormatTable, tt.noColor)
			if f.color != tt.want {
				t.Errorf("New(...).color = %v, want %v", f.color, tt.want)
			}
		})
	}
}

func TestAuto_colorSuppressedByNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	stubTTY(t, true)

	var buf bytes.Buffer
	f := Auto(&buf, false)
	if f.format != FormatTable {
		t.Errorf("Auto on a TTY should pick table format, got %s", f.format)
	}
	if f.color {
		t.Error("NO_COLOR env should disable color at construction")
	}
}

func TestPrintTable_headerColor(t *testing.T) {
	tests := []struct {
		name       string
		tty        bool
		noColor    bool
		noColorEnv string
		wantBold   bool
	}{
		{name: "bold headers on TTY with color", tty: true, wantBold: true},
		{name: "suppressed by --no-color", tty: true, noColor: true, wantBold: false},
		{name: "suppressed by NO_COLOR env", tty: true, noColorEnv: "1", wantBold: false},
		{name: "suppressed on non-TTY writer", tty: false, wantBold: false},
	}

	data := []map[string]any{{"name": "Alice"}}
	cols := []Column{{Header: "Name", Field: "name"}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColorEnv)
			stubTTY(t, tt.tty)

			var buf bytes.Buffer
			f := New(&buf, FormatTable, tt.noColor)

			if err := f.PrintTable(data, cols); err != nil {
				t.Fatalf("PrintTable returned error: %v", err)
			}

			out := buf.String()
			if gotBold := strings.Contains(out, "\x1b[1m"); gotBold != tt.wantBold {
				t.Errorf("bold header = %v, want %v\n%s", gotBold, tt.wantBold, out)
			}

			if tt.wantBold {
				if !strings.Contains(out, "\x1b[0m") {
					t.Error("expected ANSI reset after bold header")
				}
				// Data rows must never be colored.
				for _, line := range strings.Split(out, "\n") {
					if strings.Contains(line, "Alice") && strings.Contains(line, "\x1b[") {
						t.Errorf("data row should not contain ANSI codes: %q", line)
					}
				}
			}
		})
	}
}

func TestPrintJSON_neverColored(t *testing.T) {
	var buf bytes.Buffer
	f := New(&buf, FormatJSON, false)
	f.color = true // even with color on, JSON stays plain

	if err := f.PrintJSON(map[string]any{"name": "Alice"}); err != nil {
		t.Fatalf("PrintJSON returned error: %v", err)
	}

	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("JSON output should never contain ANSI codes: %q", buf.String())
	}
}

func TestPrint_dispatchesJSON(t *testing.T) {
	var buf bytes.Buffer
	f := New(&buf, FormatJSON, true)

	data := map[string]any{"key": "value"}
	cols := []Column{{Header: "Key", Field: "key"}}

	if err := f.Print(data, cols); err != nil {
		t.Fatalf("Print returned error: %v", err)
	}

	// Should produce JSON, not a table.
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("expected JSON output: %v", err)
	}
}

func TestPrint_dispatchesTable(t *testing.T) {
	var buf bytes.Buffer
	f := New(&buf, FormatTable, true)

	data := []map[string]any{{"key": "value"}}
	cols := []Column{{Header: "Key", Field: "key"}}

	if err := f.Print(data, cols); err != nil {
		t.Fatalf("Print returned error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Key") {
		t.Error("expected table output with header 'Key'")
	}
}

func TestIsTTY_bytesBuffer(t *testing.T) {
	// A bytes.Buffer does not implement Fd(), so it should not be detected
	// as a terminal.
	var buf bytes.Buffer
	if isTTY(&buf) {
		t.Error("bytes.Buffer should not be detected as a TTY")
	}
}

func TestAuto_nonTTY(t *testing.T) {
	var buf bytes.Buffer
	f := Auto(&buf, false)

	if f.format != FormatJSON {
		t.Errorf("Auto with bytes.Buffer should default to JSON, got %s", f.format)
	}
}

func TestExtractFields_maps(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "email": "alice@example.com"},
		{"name": "Bob", "email": "bob@example.com"},
	}

	cols := []Column{
		{Header: "Name", Field: "name"},
		{Header: "Email", Field: "email"},
	}

	rows := ExtractFields(data, cols)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	if rows[0][0] != "Alice" {
		t.Errorf("expected rows[0][0]=Alice, got %s", rows[0][0])
	}
	if rows[0][1] != "alice@example.com" {
		t.Errorf("expected rows[0][1]=alice@example.com, got %s", rows[0][1])
	}
	if rows[1][0] != "Bob" {
		t.Errorf("expected rows[1][0]=Bob, got %s", rows[1][0])
	}
}

func TestExtractFields_struct(t *testing.T) {
	type User struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	data := []User{
		{Name: "Alice", Email: "alice@example.com"},
		{Name: "Bob", Email: "bob@example.com"},
	}

	cols := []Column{
		{Header: "Name", Field: "name"},
		{Header: "Email", Field: "email"},
	}

	rows := ExtractFields(data, cols)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	if rows[0][0] != "Alice" {
		t.Errorf("expected rows[0][0]=Alice, got %s", rows[0][0])
	}
	if rows[1][1] != "bob@example.com" {
		t.Errorf("expected rows[1][1]=bob@example.com, got %s", rows[1][1])
	}
}

func TestExtractFields_nestedMap(t *testing.T) {
	data := []map[string]any{
		{
			"user": map[string]any{
				"name": "Alice",
			},
		},
	}

	cols := []Column{
		{Header: "User Name", Field: "user.name"},
	}

	rows := ExtractFields(data, cols)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if rows[0][0] != "Alice" {
		t.Errorf("expected rows[0][0]=Alice, got %s", rows[0][0])
	}
}

func TestExtractFields_missingField(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice"},
	}

	cols := []Column{
		{Header: "Name", Field: "name"},
		{Header: "Missing", Field: "nonexistent"},
	}

	rows := ExtractFields(data, cols)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if rows[0][0] != "Alice" {
		t.Errorf("expected rows[0][0]=Alice, got %s", rows[0][0])
	}
	if rows[0][1] != "" {
		t.Errorf("expected rows[0][1]='', got %s", rows[0][1])
	}
}

func TestExtractFields_nil(t *testing.T) {
	cols := []Column{
		{Header: "Name", Field: "name"},
	}

	rows := ExtractFields(nil, cols)
	if rows != nil {
		t.Errorf("expected nil rows for nil data, got %v", rows)
	}
}

func TestExtractFields_nilPointer(t *testing.T) {
	var data *[]map[string]any

	cols := []Column{
		{Header: "Name", Field: "name"},
	}

	rows := ExtractFields(data, cols)
	if rows != nil {
		t.Errorf("expected nil rows for nil pointer, got %v", rows)
	}
}

func TestExtractFields_singleMap(t *testing.T) {
	// A single map (not in a slice) should produce one row.
	data := map[string]any{"name": "Alice", "age": 30}

	cols := []Column{
		{Header: "Name", Field: "name"},
		{Header: "Age", Field: "age"},
	}

	rows := ExtractFields(data, cols)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if rows[0][0] != "Alice" {
		t.Errorf("expected rows[0][0]=Alice, got %s", rows[0][0])
	}
	if rows[0][1] != "30" {
		t.Errorf("expected rows[0][1]=30, got %s", rows[0][1])
	}
}

func TestFilterColumns_empty(t *testing.T) {
	cols := []Column{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
	}

	result := FilterColumns(cols, "")
	if len(result) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(result))
	}
	if result[0].Field != "id" || result[1].Field != "name" {
		t.Errorf("expected original columns unchanged, got %v", result)
	}
}

func TestFilterColumns_subset(t *testing.T) {
	cols := []Column{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
		{Header: "Status", Field: "status"},
	}

	result := FilterColumns(cols, "name,status")
	if len(result) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(result))
	}
	if result[0].Field != "name" {
		t.Errorf("expected first column field=name, got %s", result[0].Field)
	}
	if result[1].Field != "status" {
		t.Errorf("expected second column field=status, got %s", result[1].Field)
	}
}

func TestFilterColumns_preservesOrder(t *testing.T) {
	cols := []Column{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
		{Header: "Status", Field: "status"},
	}

	result := FilterColumns(cols, "status,id")
	if len(result) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(result))
	}
	if result[0].Field != "status" {
		t.Errorf("expected first column field=status, got %s", result[0].Field)
	}
	if result[1].Field != "id" {
		t.Errorf("expected second column field=id, got %s", result[1].Field)
	}
}

func TestFilterColumns_unknownIgnored(t *testing.T) {
	cols := []Column{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
	}

	result := FilterColumns(cols, "id,unknown,name")
	if len(result) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(result))
	}
	if result[0].Field != "id" || result[1].Field != "name" {
		t.Errorf("expected id,name columns, got %v", result)
	}
}

func TestFilterColumns_dotPath(t *testing.T) {
	cols := []Column{
		{Header: "ID", Field: "id"},
		{Header: "Worker Name", Field: "worker.name"},
		{Header: "Status", Field: "status"},
	}

	result := FilterColumns(cols, "worker.name,id")
	if len(result) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(result))
	}
	if result[0].Field != "worker.name" {
		t.Errorf("expected first column field=worker.name, got %s", result[0].Field)
	}
	if result[1].Field != "id" {
		t.Errorf("expected second column field=id, got %s", result[1].Field)
	}
}

func TestFilterColumns_whitespace(t *testing.T) {
	cols := []Column{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
	}

	result := FilterColumns(cols, " id , name ")
	if len(result) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(result))
	}
	if result[0].Field != "id" || result[1].Field != "name" {
		t.Errorf("expected id,name columns, got %v", result)
	}
}

func TestFilterColumns_allUnknown(t *testing.T) {
	cols := []Column{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
	}

	result := FilterColumns(cols, "foo,bar")
	if len(result) != 0 {
		t.Fatalf("expected 0 columns for all unknown names, got %d", len(result))
	}
}

func TestExtractFields_nilMapValue(t *testing.T) {
	data := []map[string]any{
		{"name": nil},
	}

	cols := []Column{
		{Header: "Name", Field: "name"},
	}

	rows := ExtractFields(data, cols)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	// nil values should render as empty string.
	if rows[0][0] != "" {
		t.Errorf("expected empty string for nil value, got %q", rows[0][0])
	}
}

func TestExtractFields_unwrapSingleKeySlice(t *testing.T) {
	// Simulates a response like {"accounts": [{"id": "1", "name": "Alice"}]}
	data := map[string]any{
		"accounts": []any{
			map[string]any{"id": "1", "name": "Alice"},
			map[string]any{"id": "2", "name": "Bob"},
		},
	}

	cols := []Column{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
	}

	rows := ExtractFields(data, cols)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0][0] != "1" || rows[0][1] != "Alice" {
		t.Errorf("row 0 = %v, want [1 Alice]", rows[0])
	}
	if rows[1][0] != "2" || rows[1][1] != "Bob" {
		t.Errorf("row 1 = %v, want [2 Bob]", rows[1])
	}
}

// --- FilterFields tests ---

func TestFilterFields_emptyFields(t *testing.T) {
	data := map[string]any{"id": "1", "name": "Alice"}
	result := FilterFields(data, nil)

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}
	if m["id"] != "1" || m["name"] != "Alice" {
		t.Error("expected data unchanged when fields is nil")
	}

	result2 := FilterFields(data, []string{})
	m2, ok := result2.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}
	if m2["id"] != "1" || m2["name"] != "Alice" {
		t.Error("expected data unchanged when fields is empty slice")
	}
}

func TestFilterFields_mapTopLevel(t *testing.T) {
	data := map[string]any{
		"id":     "1",
		"name":   "Alice",
		"status": "active",
		"email":  "alice@example.com",
	}

	result := FilterFields(data, []string{"id", "name"})
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}

	if len(m) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(m))
	}
	if m["id"] != "1" {
		t.Errorf("expected id=1, got %v", m["id"])
	}
	if m["name"] != "Alice" {
		t.Errorf("expected name=Alice, got %v", m["name"])
	}
	if _, ok := m["status"]; ok {
		t.Error("status should have been filtered out")
	}
}

func TestFilterFields_mapNestedDotPath(t *testing.T) {
	data := map[string]any{
		"id": "1",
		"worker": map[string]any{
			"id":    "w1",
			"name":  "Bob",
			"email": "bob@example.com",
		},
	}

	result := FilterFields(data, []string{"id", "worker.name"})
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}

	if m["id"] != "1" {
		t.Errorf("expected id=1, got %v", m["id"])
	}

	worker, ok := m["worker"].(map[string]any)
	if !ok {
		t.Fatal("expected worker to be map[string]any")
	}
	if worker["name"] != "Bob" {
		t.Errorf("expected worker.name=Bob, got %v", worker["name"])
	}
	if _, ok := worker["email"]; ok {
		t.Error("worker.email should have been filtered out")
	}
	if _, ok := worker["id"]; ok {
		t.Error("worker.id should have been filtered out")
	}
}

func TestFilterFields_multipleNestedPaths(t *testing.T) {
	data := map[string]any{
		"worker": map[string]any{
			"id":    "w1",
			"name":  "Bob",
			"email": "bob@example.com",
		},
	}

	result := FilterFields(data, []string{"worker.id", "worker.name"})
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}

	worker, ok := m["worker"].(map[string]any)
	if !ok {
		t.Fatal("expected worker to be map[string]any")
	}
	if len(worker) != 2 {
		t.Fatalf("expected 2 worker fields, got %d", len(worker))
	}
	if worker["id"] != "w1" {
		t.Errorf("expected worker.id=w1, got %v", worker["id"])
	}
	if worker["name"] != "Bob" {
		t.Errorf("expected worker.name=Bob, got %v", worker["name"])
	}
}

func TestFilterFields_sliceOfMaps(t *testing.T) {
	data := []any{
		map[string]any{"id": "1", "name": "Alice", "status": "active"},
		map[string]any{"id": "2", "name": "Bob", "status": "inactive"},
	}

	result := FilterFields(data, []string{"id", "name"})
	slice, ok := result.([]any)
	if !ok {
		t.Fatal("expected []any")
	}
	if len(slice) != 2 {
		t.Fatalf("expected 2 items, got %d", len(slice))
	}

	first, ok := slice[0].(map[string]any)
	if !ok {
		t.Fatal("expected first item to be map[string]any")
	}
	if len(first) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(first))
	}
	if first["id"] != "1" || first["name"] != "Alice" {
		t.Error("unexpected values in first item")
	}

	second, ok := slice[1].(map[string]any)
	if !ok {
		t.Fatal("expected second item to be map[string]any")
	}
	if second["id"] != "2" || second["name"] != "Bob" {
		t.Error("unexpected values in second item")
	}
}

func TestFilterFields_typedSliceOfMaps(t *testing.T) {
	data := []map[string]any{
		{"id": "1", "name": "Alice"},
		{"id": "2", "name": "Bob"},
	}

	result := FilterFields(data, []string{"name"})
	slice, ok := result.([]any)
	if !ok {
		t.Fatal("expected []any")
	}
	if len(slice) != 2 {
		t.Fatalf("expected 2 items, got %d", len(slice))
	}

	first, ok := slice[0].(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}
	if len(first) != 1 {
		t.Fatalf("expected 1 field, got %d", len(first))
	}
	if first["name"] != "Alice" {
		t.Errorf("expected name=Alice, got %v", first["name"])
	}
}

func TestFilterFields_missingField(t *testing.T) {
	data := map[string]any{"id": "1", "name": "Alice"}

	result := FilterFields(data, []string{"id", "nonexistent"})
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}
	if len(m) != 1 {
		t.Fatalf("expected 1 field, got %d", len(m))
	}
	if m["id"] != "1" {
		t.Errorf("expected id=1, got %v", m["id"])
	}
}

func TestFilterFields_nonMapData(t *testing.T) {
	// Non-map/slice data should be returned as-is.
	data := "just a string"
	result := FilterFields(data, []string{"id"})
	if result != "just a string" {
		t.Errorf("expected string to pass through, got %v", result)
	}
}

func TestFilterFields_whitespace(t *testing.T) {
	data := map[string]any{"id": "1", "name": "Alice"}
	result := FilterFields(data, []string{" id ", " name "})
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}
	if m["id"] != "1" || m["name"] != "Alice" {
		t.Error("expected whitespace-trimmed fields to match")
	}
}

func TestFilterFields_deepNesting(t *testing.T) {
	data := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep",
				"d": "also deep",
			},
		},
	}

	result := FilterFields(data, []string{"a.b.c"})
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any")
	}
	a, ok := m["a"].(map[string]any)
	if !ok {
		t.Fatal("expected a to be map[string]any")
	}
	b, ok := a["b"].(map[string]any)
	if !ok {
		t.Fatal("expected a.b to be map[string]any")
	}
	if b["c"] != "deep" {
		t.Errorf("expected a.b.c=deep, got %v", b["c"])
	}
	if _, ok := b["d"]; ok {
		t.Error("a.b.d should have been filtered out")
	}
}

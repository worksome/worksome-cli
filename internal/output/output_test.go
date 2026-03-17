package output

import (
	"bytes"
	"encoding/json"
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

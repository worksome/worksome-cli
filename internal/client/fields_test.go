package client

import (
	"context"
	"strings"
	"testing"
)

var testSchema = Schema{
	"Timesheet": {Fields: map[string]FieldInfo{
		"id":            {},
		"startDate":     {},
		"worker":        {Type: "Worker"},
		"hire":          {Type: "Hire"},
		"registrations": {Type: "TimesheetRegistration", Paginated: true},
		"files":         {Type: "File", Paginated: true, NeedsArgs: true},
		"approvable":    {Type: "Approvable"},
	}, Nested: "id startDate"},
	"Worker":                {Fields: map[string]FieldInfo{"id": {}, "name": {}, "email": {}, "phone": {}}, Nested: "id name"},
	"Hire":                  {Fields: map[string]FieldInfo{"id": {}, "number": {}, "job": {Type: "Job"}, "company": {Type: "Company"}, "timesheets": {Type: "Timesheet", Paginated: true}}, Nested: "id number"},
	"Job":                   {Fields: map[string]FieldInfo{"id": {}, "name": {}}, Nested: "id name"},
	"Company":               {Fields: map[string]FieldInfo{"id": {}, "name": {}, "parent": {Type: "Company"}}, Nested: "id name"},
	"TimesheetRegistration": {Fields: map[string]FieldInfo{"id": {}, "type": {}, "date": {}, "duration": {}}, Nested: "id type"},
	"File":                  {Fields: map[string]FieldInfo{"id": {}}, Nested: "id"},
	"PaginatorInfo":         {Fields: map[string]FieldInfo{"count": {}, "hasMorePages": {}, "total": {}}, Nested: "total"},
	"Approvable":            {Nested: "__typename ... on Hire { id number }"},
}

const timesheetsQuery = `query Timesheets($first: Int! = 10, $page: Int) {
	timesheets(first: $first, page: $page) { paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { id worker { id name } hire { id number } startDate } }
}`

func mustSelect(t *testing.T, fields ...string) string {
	t.Helper()
	got, err := selectFields(timesheetsQuery, testSchema, "Timesheet", fields)
	if err != nil {
		t.Fatalf("selectFields(%v) error = %v", fields, err)
	}
	return got
}

// The acceptance case: fields outside the default selection, nested objects,
// and paginated items addressed without the "data" segment.
func TestSelectFields_ReachesFieldsOutsideTheDefault(t *testing.T) {
	got := mustSelect(t, "id", "startDate", "registrations.date", "registrations.duration", "hire.job.name", "hire.company.name")
	want := "data { id startDate registrations { paginatorInfo { total } data { date duration } } hire { job { name } company { name } } }"
	if !strings.Contains(got, want) {
		t.Errorf("selectFields() = %s\nwant it to contain %s", got, want)
	}
	if !strings.Contains(got, "timesheets(first: $first, page: $page) { paginatorInfo { count currentPage hasMorePages lastPage perPage total } data {") {
		t.Errorf("selectFields() must keep the operation's paginator: %s", got)
	}
}

func TestSelectFields_DataSegmentIsOptional(t *testing.T) {
	if a, b := mustSelect(t, "registrations.data.date"), mustSelect(t, "registrations.date"); a != b {
		t.Errorf("registrations.data.date = %s\nregistrations.date = %s\nwant the same query", a, b)
	}
}

func TestSelectFields_RelationWithoutSubfieldsUsesNestedSelection(t *testing.T) {
	got := mustSelect(t, "registrations")
	if !strings.Contains(got, "data { registrations { paginatorInfo { total } data { id type } } }") {
		t.Errorf("selectFields() = %s, want the registration's nested selection", got)
	}
}

func TestSelectFields_PaginatorInfoFields(t *testing.T) {
	got := mustSelect(t, "registrations.paginatorInfo.hasMorePages", "registrations.date")
	if !strings.Contains(got, "registrations { paginatorInfo { total hasMorePages } data { date } }") {
		t.Errorf("selectFields() = %s, want hasMorePages added to paginatorInfo", got)
	}
}

func TestSelectFields_ObjectKeepsDefaultSubtreeAndMerges(t *testing.T) {
	if got := mustSelect(t, "worker"); !strings.Contains(got, "data { worker { id name } }") {
		t.Errorf("selectFields(worker) = %s, want the default worker subtree", got)
	}
	if got := mustSelect(t, "worker.email"); !strings.Contains(got, "data { worker { email } }") {
		t.Errorf("selectFields(worker.email) = %s, want a field the default leaves out", got)
	}
	if got := mustSelect(t, "worker", "worker.email"); !strings.Contains(got, "data { worker { id name email } }") {
		t.Errorf("selectFields(worker, worker.email) = %s, want the default subtree plus email", got)
	}
	if got := mustSelect(t, "hire.job"); !strings.Contains(got, "data { hire { job { id name } } }") {
		t.Errorf("selectFields(hire.job) = %s, want the job's nested selection", got)
	}
}

func TestSelectFields_UnionFieldUsesFragments(t *testing.T) {
	if got := mustSelect(t, "approvable"); !strings.Contains(got, "approvable { __typename ... on Hire { id number } }") {
		t.Errorf("selectFields(approvable) = %s, want the union's fragments", got)
	}
	if got := mustSelect(t, "approvable.number"); !strings.Contains(got, "approvable { ... on Hire { number } }") {
		t.Errorf("selectFields(approvable.number) = %s, want the fragment narrowed", got)
	}
}

func TestSelectFields_Errors(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
		want   []string
	}{
		{"unknown nested field lists the type's fields", []string{"hire.jobb"}, []string{`unknown field "hire.jobb"`, "company, id, job, number, timesheets"}},
		{"unknown root field", []string{"nmae"}, []string{`unknown field "nmae"`, "hire", "registrations"}},
		{"unknown item field", []string{"registrations.hours"}, []string{`unknown field "registrations.hours"`, "date, duration, id, type"}},
		{"scalar has no subfields", []string{"id.value"}, []string{`"id" has no subfields`}},
		{"needs arguments", []string{"files.id"}, []string{`"files" needs arguments`}},
		{"too deep", []string{"hire.company.parent.parent.parent.name"}, []string{`"hire.company.parent.parent.parent" is nested more than 4 levels deep`}},
		{"paginated inside paginated", []string{"hire.timesheets.registrations.date"}, []string{`"hire.timesheets.registrations" is a paginated list inside another paginated list`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := selectFields(timesheetsQuery, testSchema, "Timesheet", tt.fields)
			if err == nil {
				t.Fatalf("selectFields(%v) error = nil", tt.fields)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error = %v\nwant it to contain %s", err, w)
				}
			}
		})
	}
}

func TestSelectFields_FourLevelsIsAllowed(t *testing.T) {
	if got := mustSelect(t, "hire.company.parent.parent.name"); !strings.Contains(got, "hire { company { parent { parent { name } } } }") {
		t.Errorf("selectFields() = %s", got)
	}
}

// A type the schema does not describe keeps the old behaviour: only the
// default selection can be narrowed.
func TestSelectFields_FallsBackToPruning(t *testing.T) {
	for _, typeName := range []string{"", "Approvable", "Missing"} {
		got, err := selectFields(timesheetsQuery, testSchema, typeName, []string{"worker.name"})
		if err != nil {
			t.Fatalf("selectFields(%q) error = %v", typeName, err)
		}
		if want, _ := pruneQuery(timesheetsQuery, []string{"worker.name"}); got != want {
			t.Errorf("selectFields(%q) = %s, want the pruned query %s", typeName, got, want)
		}
	}
}

func TestExecuteFor_SendsSelectedFields(t *testing.T) {
	var wire []string
	srv := newCapturingServer(t, &wire)
	defer srv.Close()

	c := New(srv.URL, "token", WithFields([]string{"id", "registrations.date"}))
	if err := c.ExecuteFor(context.Background(), timesheetsQuery, testSchema, "Timesheet", nil, nil); err != nil {
		t.Fatalf("ExecuteFor() error = %v", err)
	}
	if !strings.Contains(wire[0], "data { id registrations { paginatorInfo { total } data { date } } }") {
		t.Errorf("wire query = %s, want registrations selected", wire[0])
	}

	wire = nil
	c = New(srv.URL, "token")
	if err := c.ExecuteFor(context.Background(), timesheetsQuery, testSchema, "Timesheet", nil, nil); err != nil {
		t.Fatalf("ExecuteFor() error = %v", err)
	}
	if wire[0] != timesheetsQuery {
		t.Errorf("without --fields the query must be sent verbatim: %s", wire[0])
	}
}

// An object with its own "data" field is not a paginated result.
func TestParseOperation_DataFieldWithoutPaginatorInfo(t *testing.T) {
	const q = `query Compliance($id: ID!) { compliance(id: $id) { name data { __typename } } }`
	got, err := pruneQuery(q, []string{"name"})
	if err != nil {
		t.Fatalf("pruneQuery() error = %v", err)
	}
	if want := `query Compliance($id: ID!) { compliance(id: $id) { name } }`; got != want {
		t.Errorf("pruneQuery() = %s, want %s", got, want)
	}
}

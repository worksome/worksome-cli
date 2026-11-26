package queries

// Hand-written. The generator only writes queries.go and fieldschema.go, so
// this file survives `make generate`.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"
	"github.com/worksome/worksome-cli/internal/client"
)

// The acceptance case for --fields on timesheets: registration items, and the
// hire's job and company, none of which the default query selects.
func TestTimesheetsFieldsReachTheWholeSchema(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		sent = req.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"timesheets":{"paginatorInfo":{"hasMorePages":false},"data":[]}}}`))
	}))
	defer srv.Close()

	fields := []string{"id", "startDate", "registrations.date", "registrations.duration", "hire.job.name", "hire.company.name"}
	q := NewQuerier(client.New(srv.URL, "token", client.WithFields(fields)))
	if _, err := q.Timesheets(context.Background(), map[string]any{"first": 10}); err != nil {
		t.Fatalf("Timesheets() error = %v", err)
	}

	for _, want := range []string{
		"registrations { paginatorInfo { total } data { date duration } }",
		"hire { job { name } company { name } }",
	} {
		if !strings.Contains(sent, want) {
			t.Errorf("wire query = %s\nwant it to contain %s", sent, want)
		}
	}

	schemaSrc, err := os.ReadFile("../../../schema/schema.graphql")
	if err != nil {
		t.Fatalf("reading schema: %v", err)
	}
	schema, gqlErr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(schemaSrc)})
	if gqlErr != nil {
		t.Fatalf("loading schema: %v", gqlErr)
	}
	if _, errs := gqlparser.LoadQueryWithRules(schema, sent, rules.NewDefaultRules()); errs != nil {
		t.Errorf("wire query is invalid against the schema: %v\n%s", errs, sent)
	}
}

func TestTimesheetsUnknownFieldListsValidFields(t *testing.T) {
	q := NewQuerier(client.New("http://127.0.0.1:1", "token", client.WithFields([]string{"registrations.hours"})))
	_, err := q.Timesheets(context.Background(), map[string]any{"first": 10})
	if err == nil {
		t.Fatal("Timesheets() error = nil, want an unknown-field error")
	}
	for _, want := range []string{`unknown field "registrations.hours"`, "date", "duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %s", err, want)
		}
	}
}

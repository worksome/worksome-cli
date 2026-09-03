package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const listQuery = `query Hires($first: Int! = 10, $page: Int) {
	hires(first: $first, page: $page) { paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { id number status worker { id name email } triggersApproval currentApprovalState { id createdAt } } }
}`

const singleQuery = `query Hire($id: ID!) {
	hire(id: $id) { id number worker { id name email } triggersApproval }
}`

func TestPruneQuery_ListKeepsPaginatorInfo(t *testing.T) {
	got, err := pruneQuery(listQuery, []string{"id", "status"})
	if err != nil {
		t.Fatalf("pruneQuery() error = %v", err)
	}

	want := `query Hires($first: Int! = 10, $page: Int) { hires(first: $first, page: $page) { paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { id status } } }`
	if got != want {
		t.Errorf("pruneQuery() =\n%s\nwant\n%s", got, want)
	}
}

func TestPruneQuery_NestedPath(t *testing.T) {
	got, err := pruneQuery(listQuery, []string{"id", "worker.name"})
	if err != nil {
		t.Fatalf("pruneQuery() error = %v", err)
	}

	if !strings.Contains(got, "data { id worker { name } }") {
		t.Errorf("pruneQuery() = %s, want data { id worker { name } }", got)
	}
	if strings.Contains(got, "triggersApproval") {
		t.Errorf("pruneQuery() still requests triggersApproval: %s", got)
	}
}

func TestPruneQuery_ObjectPathKeepsSubtree(t *testing.T) {
	got, err := pruneQuery(listQuery, []string{"worker"})
	if err != nil {
		t.Fatalf("pruneQuery() error = %v", err)
	}

	if !strings.Contains(got, "data { worker { id name email } }") {
		t.Errorf("pruneQuery() = %s, want the full worker subtree", got)
	}
}

func TestPruneQuery_SingleObjectQuery(t *testing.T) {
	got, err := pruneQuery(singleQuery, []string{"id", "worker.email"})
	if err != nil {
		t.Fatalf("pruneQuery() error = %v", err)
	}

	want := `query Hire($id: ID!) { hire(id: $id) { id worker { email } } }`
	if got != want {
		t.Errorf("pruneQuery() =\n%s\nwant\n%s", got, want)
	}
}

func TestPruneQuery_InlineFragment(t *testing.T) {
	query := `query MultiFactors($first: Int!) {
	multiFactors(first: $first) { paginatorInfo { hasMorePages } data { __typename ... on HasMultiFactorMetadata { id name status verifiedAt } } }
}`

	got, err := pruneQuery(query, []string{"name"})
	if err != nil {
		t.Fatalf("pruneQuery() error = %v", err)
	}

	if !strings.Contains(got, "data { ... on HasMultiFactorMetadata { name } }") {
		t.Errorf("pruneQuery() = %s, want the fragment kept with only name", got)
	}
}

func TestPruneQuery_UnknownField(t *testing.T) {
	_, err := pruneQuery(listQuery, []string{"id", "nmae"})
	if err == nil {
		t.Fatal("pruneQuery() error = nil, want unknown field error")
	}
	if !strings.Contains(err.Error(), `unknown field "nmae"`) {
		t.Errorf("pruneQuery() error = %v, want it to name the unknown field", err)
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("pruneQuery() error = %v, want it to list the available fields", err)
	}
}

func TestPruneQuery_UnknownNestedField(t *testing.T) {
	_, err := pruneQuery(listQuery, []string{"worker.nmae"})
	if err == nil {
		t.Fatal("pruneQuery() error = nil, want unknown field error")
	}
	if !strings.Contains(err.Error(), `unknown field "worker.nmae"`) {
		t.Errorf("pruneQuery() error = %v, want the full path in the message", err)
	}
}

func TestPruneQuery_ScalarHasNoSubfields(t *testing.T) {
	_, err := pruneQuery(listQuery, []string{"id.value"})
	if err == nil {
		t.Fatal("pruneQuery() error = nil, want no-subfields error")
	}
	if !strings.Contains(err.Error(), "has no subfields") {
		t.Errorf("pruneQuery() error = %v, want a no-subfields error", err)
	}
}

func TestPruneQuery_UnprunableQueriesAreUnchanged(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		fields []string
	}{
		{"no fields", listQuery, []string{"", "  "}},
		{"no selection set", "query Accounts {\n\taccounts \n}", []string{"id"}},
		{"malformed", "query Broken { hires { id ", []string{"id"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pruneQuery(tt.query, tt.fields)
			if err != nil {
				t.Fatalf("pruneQuery() error = %v", err)
			}
			if got != tt.query {
				t.Errorf("pruneQuery() = %q, want it unchanged", got)
			}
		})
	}
}

// captureQuery runs Execute against a stub server and returns the query as sent.
func captureQuery(t *testing.T, query string, opts ...Option) string {
	t.Helper()

	var sent string
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		var body graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		sent = body.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	defer srv.Close()

	c := New(srv.URL, "test-token", opts...)
	if err := c.Execute(context.Background(), query, nil, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return sent
}

func TestExecute_WithFieldsNarrowsWireQuery(t *testing.T) {
	sent := captureQuery(t, listQuery, WithFields([]string{"id", "worker.name"}))

	if strings.Contains(sent, "triggersApproval") {
		t.Errorf("wire query still requests triggersApproval: %s", sent)
	}
	if !strings.Contains(sent, "data { id worker { name } }") {
		t.Errorf("wire query = %s, want the narrowed selection set", sent)
	}
	if !strings.Contains(sent, "paginatorInfo {") {
		t.Errorf("wire query = %s, want paginatorInfo preserved", sent)
	}
}

func TestExecute_WithoutFieldsSendsQueryVerbatim(t *testing.T) {
	if sent := captureQuery(t, listQuery); sent != listQuery {
		t.Errorf("wire query =\n%s\nwant it byte-identical to\n%s", sent, listQuery)
	}
}

func TestExecute_WithUnknownFieldFailsBeforeRequest(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Execute() sent a request, want it to fail before that")
	})
	defer srv.Close()

	c := New(srv.URL, "test-token", WithFields([]string{"nmae"}))
	if err := c.Execute(context.Background(), listQuery, nil, nil); err == nil {
		t.Fatal("Execute() error = nil, want unknown field error")
	}
}

func TestPruneQuery_RejectsEmptyPathSegments(t *testing.T) {
	const query = `query Jobs { jobs { data { id name } paginatorInfo { total } } }`
	for _, f := range []string{".name", "worker.", "worker..name", "."} {
		t.Run(f, func(t *testing.T) {
			if _, err := pruneQuery(query, []string{f}); err == nil {
				t.Fatalf("pruneQuery(%q) should reject an empty segment", f)
			}
		})
	}
}

// The option must not alias the caller's slice: a later mutation would silently
// change which fields every subsequent request asks for.
func TestWithFields_ClonesInput(t *testing.T) {
	fields := []string{"id", "name"}
	c := New("http://example.invalid", "token", WithFields(fields))
	fields[0] = "mutated"
	if c.fields[0] != "id" {
		t.Errorf("client.fields[0] = %q, want %q", c.fields[0], "id")
	}
}

const groupsQuery = `query UserGroups($first: Int! = 10, $page: Int) {
	userGroups(first: $first, page: $page) { paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { id name status } }
}`

var groupsOptional = map[string]string{
	"users": "users { paginatorInfo { total } data { id name email } }",
}

// Paginated relations are left out of the generated query and added on
// request, before pruning, so `--fields users.data.name` reaches them.
func TestAddOptionalSelections_IntoPaginatedData(t *testing.T) {
	got := AddOptionalSelections(groupsQuery, groupsOptional)
	want := "data { id name status users { paginatorInfo { total } data { id name email } } }"
	if !strings.Contains(got, want) {
		t.Errorf("AddOptionalSelections() = %s\nwant it to contain %s", got, want)
	}

	pruned, err := pruneQuery(got, []string{"id", "users.data.name"})
	if err != nil {
		t.Fatalf("pruneQuery() error = %v", err)
	}
	if want := "data { id users { data { name } } }"; !strings.Contains(pruned, want) {
		t.Errorf("pruneQuery() after adding = %s\nwant it to contain %s", pruned, want)
	}
}

func TestAddOptionalSelections_IntoSingleObject(t *testing.T) {
	got := AddOptionalSelections(singleQuery, map[string]string{
		"approvals": "approvals { paginatorInfo { total } data { id status } }",
	})
	if !strings.Contains(got, "triggersApproval approvals { paginatorInfo { total } data { id status } } }") {
		t.Errorf("AddOptionalSelections() = %s, want approvals appended to the root object", got)
	}
}

func TestAddOptionalSelections_SkipsPresentAndUnparseable(t *testing.T) {
	// Already selected: not duplicated.
	got := AddOptionalSelections(groupsQuery, map[string]string{"name": "name"})
	if strings.Count(got, " name ") != 1 {
		t.Errorf("AddOptionalSelections() duplicated an existing field: %s", got)
	}
	// Unparseable query: returned as is.
	if got := AddOptionalSelections("not graphql", groupsOptional); got != "not graphql" {
		t.Errorf("AddOptionalSelections() rewrote an unparseable query: %q", got)
	}
}

// Naming an on-request field that does not exist still errors, and the error
// lists the on-request ones alongside the rest.
func TestAddOptionalSelections_UnknownFieldListsOptional(t *testing.T) {
	_, err := pruneQuery(AddOptionalSelections(groupsQuery, groupsOptional), []string{"members"})
	if err == nil || !strings.Contains(err.Error(), "users") {
		t.Errorf("expected an unknown-field error naming users, got %v", err)
	}
}

// Over the wire: the relation is sent only when --fields names it.
func TestExecuteWithOptional_AddsOnlyWhenRequested(t *testing.T) {
	var wire []string
	srv := newCapturingServer(t, &wire)
	defer srv.Close()

	c := New(srv.URL, "token", WithFields([]string{"id", "users.data.name"}))
	if err := c.ExecuteWithOptional(context.Background(), groupsQuery, groupsOptional, nil, nil); err != nil {
		t.Fatalf("ExecuteWithOptional() error = %v", err)
	}
	if !strings.Contains(wire[0], "users { data { name } }") {
		t.Errorf("wire query should select users when --fields names it: %s", wire[0])
	}

	wire = nil
	c = New(srv.URL, "token")
	if err := c.ExecuteWithOptional(context.Background(), groupsQuery, groupsOptional, nil, nil); err != nil {
		t.Fatalf("ExecuteWithOptional() error = %v", err)
	}
	if strings.Contains(wire[0], "users") {
		t.Errorf("wire query must stay lean without --fields: %s", wire[0])
	}
}

func newCapturingServer(t *testing.T, wire *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		*wire = append(*wire, req.Query)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
}

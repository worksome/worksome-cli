package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const nestedQuery = `query Timesheets($first: Int! = 10, $page: Int) {
	timesheets(first: $first, page: $page) { paginatorInfo { hasMorePages total } data { id registrations { paginatorInfo { total } data { date } } } }
}`

var registrationsPage = regexp.MustCompile(`registrations\(first: (\d+)(?:, page: (\d+))?\)`)

// registrationsServer serves timesheets whose registration counts are given
// per row, honouring the nested first/page arguments the way the API does.
// rowID lets a test change which row sits at a position between requests.
func registrationsServer(t *testing.T, counts []int, rowID func(call, row int) string) (*httptest.Server, *[]string) {
	t.Helper()
	var queries []string
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		var body graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		queries = append(queries, body.Query)
		call := len(queries)

		first, page := 10, 1
		if m := registrationsPage.FindStringSubmatch(body.Query); m != nil {
			first, _ = strconv.Atoi(m[1])
			if m[2] != "" {
				page, _ = strconv.Atoi(m[2])
			}
		}

		rows := make([]any, len(counts))
		for i, n := range counts {
			var items []any
			for j := (page - 1) * first; j < n && j < page*first; j++ {
				items = append(items, map[string]any{"date": fmt.Sprintf("d%d", j)})
			}
			if items == nil {
				items = []any{}
			}
			info := map[string]any{"total": n}
			if strings.Contains(body.Query, "hasMorePages") {
				info["hasMorePages"] = page*first < n
			}
			rows[i] = map[string]any{
				"id":            rowID(call, i),
				"registrations": map[string]any{"paginatorInfo": info, "data": items},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"timesheets": map[string]any{
			"paginatorInfo": map[string]any{"hasMorePages": false, "total": len(counts)},
			"data":          rows,
		}}})
	})
	return srv, &queries
}

func stableIDs(_, row int) string { return strconv.Itoa(row) }

func registrationDates(t *testing.T, result map[string]any) [][]string {
	t.Helper()
	var out [][]string
	rows := result["timesheets"].(map[string]any)["data"].([]any)
	for _, row := range rows {
		var dates []string
		for _, item := range row.(map[string]any)["registrations"].(map[string]any)["data"].([]any) {
			dates = append(dates, item.(map[string]any)["date"].(string))
		}
		out = append(out, dates)
	}
	return out
}

func runNested(t *testing.T, counts []int, rowID func(call, row int) string, opts ...Option) (map[string]any, []string, string) {
	t.Helper()
	srv, queries := registrationsServer(t, counts, rowID)
	defer srv.Close()
	var warn bytes.Buffer
	c := New(srv.URL, "token", append(opts, WithWarnWriter(&warn))...)
	var result map[string]any
	if err := c.Execute(context.Background(), nestedQuery, nil, &result); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return result, *queries, warn.String()
}

// A row with more registrations than one page gets all of them; the server's
// default of ten no longer cuts the list silently.
func TestExecute_CompletesNestedLists(t *testing.T) {
	result, queries, warn := runNested(t, []int{250, 5, 0}, stableIDs)

	dates := registrationDates(t, result)
	if len(dates[0]) != 250 || len(dates[1]) != 5 || len(dates[2]) != 0 {
		t.Fatalf("registrations per row = %d, %d, %d, want 250, 5, 0", len(dates[0]), len(dates[1]), len(dates[2]))
	}
	for i, d := range dates[0] {
		if d != fmt.Sprintf("d%d", i) {
			t.Fatalf("registration %d = %s, want d%d: pages appended out of order", i, d, i)
		}
	}
	if len(queries) != 3 {
		t.Errorf("sent %d requests, want 3 (pages 1 to 3 of the longest list)", len(queries))
	}
	if !strings.Contains(queries[0], "registrations(first: 100) { paginatorInfo { total hasMorePages } data { date } }") {
		t.Errorf("first request = %s, want the nested list asked for 100 items and hasMorePages", queries[0])
	}
	if !strings.Contains(queries[2], "registrations(first: 100, page: 3)") {
		t.Errorf("third request = %s, want page 3 of the nested list", queries[2])
	}
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}

	info := result["timesheets"].(map[string]any)["data"].([]any)[0].(map[string]any)["registrations"].(map[string]any)["paginatorInfo"].(map[string]any)
	if _, ok := info["hasMorePages"]; ok {
		t.Errorf("paginatorInfo = %v: hasMorePages was added by the CLI and must not be printed", info)
	}
	if info["total"] != float64(250) {
		t.Errorf("paginatorInfo.total = %v, want 250", info["total"])
	}
}

func TestExecute_ShortNestedListsNeedOneRequest(t *testing.T) {
	_, queries, warn := runNested(t, []int{3, 100}, stableIDs)
	if len(queries) != 1 || warn != "" {
		t.Errorf("sent %d requests with warning %q, want 1 and none", len(queries), warn)
	}
}

// Rows are matched by position across requests; when a row moves, its list is
// left as it was and the cut is reported rather than filled with another
// row's items.
func TestExecute_NestedListOfAMovedRowIsReported(t *testing.T) {
	moved := func(call, row int) string {
		if call > 1 {
			return strconv.Itoa(row + 1)
		}
		return strconv.Itoa(row)
	}
	result, _, warn := runNested(t, []int{150}, moved)
	if got := len(registrationDates(t, result)[0]); got != 100 {
		t.Errorf("registrations = %d, want the first page of 100 kept", got)
	}
	if want := "warning: timesheets.data[0].registrations: only 100 of 150 items were fetched, because the data changed between requests"; !strings.Contains(warn, want) {
		t.Errorf("warning = %q, want %q", warn, want)
	}
}

func TestExecute_NestedListsStopAtThePageCap(t *testing.T) {
	result, queries, warn := runNested(t, []int{nestedPageSize*maxNestedPage + 1}, stableIDs)
	if len(queries) != maxNestedPage {
		t.Errorf("sent %d requests, want %d", len(queries), maxNestedPage)
	}
	if got := len(registrationDates(t, result)[0]); got != nestedPageSize*maxNestedPage {
		t.Errorf("registrations = %d, want %d", got, nestedPageSize*maxNestedPage)
	}
	if !strings.Contains(warn, fmt.Sprintf("because the CLI stops after %d pages", maxNestedPage)) {
		t.Errorf("warning = %q, want the page cap named", warn)
	}
}

// A list inside another nested list's items cannot be completed: its parent
// row moves between the outer list's pages. It is reported instead.
func TestExecute_InnerNestedListIsReported(t *testing.T) {
	const query = `query Hires { hires { paginatorInfo { total } data { id timesheets { paginatorInfo { total } data { id registrations { paginatorInfo { total } data { date } } } } } } }`
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"hires":{"paginatorInfo":{"total":1},"data":[{"id":"h","timesheets":{"paginatorInfo":{"total":1,"hasMorePages":false},"data":[{"id":"t","registrations":{"paginatorInfo":{"total":120,"hasMorePages":true},"data":[{"date":"d0"}]}}]}}]}}}`))
	})
	defer srv.Close()
	var warn bytes.Buffer
	c := New(srv.URL, "token", WithWarnWriter(&warn))
	if err := c.Execute(context.Background(), query, nil, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := "warning: hires.data[0].timesheets.data[0].registrations: only 1 of 120 items were fetched, because it is inside another paginated list"; !strings.Contains(warn.String(), want) {
		t.Errorf("warning = %q, want %q", warn.String(), want)
	}
}

// Mutations are never re-sent, so their nested lists are left alone.
func TestPlanNestedPages_SkipsMutationsAndFlatQueries(t *testing.T) {
	for _, q := range []string{
		`mutation M { m { id items { paginatorInfo { total } data { id } } } }`,
		listQuery,
		"not graphql",
	} {
		if plan := planNestedPages(q); plan != nil {
			t.Errorf("planNestedPages(%q) = %q, want nil", q, plan.query)
		}
	}
}

func TestWithArg(t *testing.T) {
	tests := map[string]string{
		"registrations":                          "registrations(first: 100)",
		"registrations(first: 5)":                "registrations(first: 5)",
		"r: registrations(orderBy: DATE)":        "r: registrations(orderBy: DATE, first: 100)",
		"registrations(orderBy: DATE, first: 5)": "registrations(orderBy: DATE, first: 5)",
	}
	for head, want := range tests {
		if got := withArg(head, "first", "100"); got != want {
			t.Errorf("withArg(%q) = %q, want %q", head, got, want)
		}
	}
}

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestServer creates an httptest.Server that responds with the given JSON body.
func newTestServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func TestExecute_Success(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers.
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", got)
		}

		// Verify request body.
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if !strings.Contains(req.Query, "hello") {
			t.Errorf("unexpected query: %s", req.Query)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"greeting":"world"}}`)
	})
	defer srv.Close()

	c := New(srv.URL, "test-token")

	var result struct {
		Greeting string `json:"greeting"`
	}
	err := c.Execute(context.Background(), "{ hello }", nil, &result)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Greeting != "world" {
		t.Errorf("Greeting = %q, want %q", result.Greeting, "world")
	}
}

func TestExecute_GraphQLErrors(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"data": null,
			"errors": [
				{"message": "Field not found", "path": ["user", "email"]},
				{"message": "Unauthorized access", "extensions": {"code": "FORBIDDEN"}}
			]
		}`)
	})
	defer srv.Close()

	c := New(srv.URL, "token")
	err := c.Execute(context.Background(), "{ user { email } }", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	gqlErrs, ok := err.(GraphQLErrors)
	if !ok {
		t.Fatalf("expected GraphQLErrors, got %T", err)
	}
	if len(gqlErrs) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(gqlErrs))
	}
	if gqlErrs[0].Message != "Field not found" {
		t.Errorf("first error message = %q", gqlErrs[0].Message)
	}
	if gqlErrs[1].Extensions["code"] != "FORBIDDEN" {
		t.Errorf("second error extension code = %v", gqlErrs[1].Extensions["code"])
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "non-graphql error",
			err:  fmt.Errorf("something went wrong"),
			want: false,
		},
		{
			name: "unauthenticated graphql error",
			err: GraphQLErrors{
				{Message: "Unauthenticated."},
			},
			want: true,
		},
		{
			name: "mixed errors with unauthenticated",
			err: GraphQLErrors{
				{Message: "Some other error"},
				{Message: "Unauthenticated"},
			},
			want: true,
		},
		{
			name: "graphql error without auth",
			err: GraphQLErrors{
				{Message: "Validation error"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAuthError(tt.err); got != tt.want {
				t.Errorf("IsAuthError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecute_RetryOnNetworkError(t *testing.T) {
	var attempts atomic.Int32

	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			// Force a network-level error by hijacking and closing the connection.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("server does not support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack failed: %v", err)
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"ok":true}}`)
	})
	defer srv.Close()

	// Use a short backoff-friendly HTTP client.
	c := New(srv.URL, "token", WithHTTPClient(&http.Client{Timeout: 2 * time.Second}))
	// Override backoff for faster tests -- we can't change the const, so we
	// accept the default 1s/2s waits. Instead we use a context with a generous timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var result struct {
		OK bool `json:"ok"`
	}
	err := c.Execute(ctx, "{ health }", nil, &result)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK {
		t.Error("expected ok=true")
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

func TestExecute_Verbose(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"ping":"pong"}}`)
	})
	defer srv.Close()

	var buf bytes.Buffer
	prevOut, prevFlags := log.Default().Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0) // Remove timestamps for deterministic output.
	// Restore the real writer, not nil: a nil writer makes every later
	// log.Printf in the process panic.
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	c := New(srv.URL, "token", WithVerbose(true))
	var result struct {
		Ping string `json:"ping"`
	}
	err := c.Execute(context.Background(), "{ ping }", nil, &result)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[graphql] --> POST") {
		t.Errorf("verbose output missing request log:\n%s", output)
	}
	if !strings.Contains(output, "[graphql] <--") {
		t.Errorf("verbose output missing response log:\n%s", output)
	}
	if !strings.Contains(output, "ping") {
		t.Errorf("verbose output missing query content:\n%s", output)
	}
}

func TestExecuteAll_Pagination(t *testing.T) {
	page1 := `{
		"data": {
			"result": {
				"data": [{"id": 1, "name": "Alice"}, {"id": 2, "name": "Bob"}],
				"paginatorInfo": {
					"count": 2, "currentPage": 1, "hasMorePages": true,
					"lastPage": 3, "perPage": 2, "total": 5
				}
			}
		}
	}`
	page2 := `{
		"data": {
			"result": {
				"data": [{"id": 3, "name": "Carol"}, {"id": 4, "name": "Dave"}],
				"paginatorInfo": {
					"count": 2, "currentPage": 2, "hasMorePages": true,
					"lastPage": 3, "perPage": 2, "total": 5
				}
			}
		}
	}`
	page3 := `{
		"data": {
			"result": {
				"data": [{"id": 5, "name": "Eve"}],
				"paginatorInfo": {
					"count": 1, "currentPage": 3, "hasMorePages": false,
					"lastPage": 3, "perPage": 2, "total": 5
				}
			}
		}
	}`

	pages := []string{page1, page2, page3}

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		pageNum := 1
		if p, ok := req.Variables["page"]; ok {
			// JSON numbers decode as float64.
			if pf, ok := p.(float64); ok {
				pageNum = int(pf)
			}
		}

		if pageNum < 1 || pageNum > len(pages) {
			http.Error(w, "page out of range", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, pages[pageNum-1])
	})
	defer srv.Close()

	type Worker struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	c := New(srv.URL, "token")
	query := `query($page: Int!) {
		result: workers(page: $page, first: 2) {
			data { id name }
			paginatorInfo { count currentPage hasMorePages lastPage perPage total }
		}
	}`

	workers, err := ExecuteAll[Worker](context.Background(), c, query, nil)
	if err != nil {
		t.Fatalf("ExecuteAll returned error: %v", err)
	}

	if len(workers) != 5 {
		t.Fatalf("expected 5 workers, got %d", len(workers))
	}

	expectedNames := []string{"Alice", "Bob", "Carol", "Dave", "Eve"}
	for i, w := range workers {
		if w.Name != expectedNames[i] {
			t.Errorf("worker[%d].Name = %q, want %q", i, w.Name, expectedNames[i])
		}
		if w.ID != i+1 {
			t.Errorf("worker[%d].ID = %d, want %d", i, w.ID, i+1)
		}
	}
}

func TestExecuteAll_SinglePage(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"data": {
				"result": {
					"data": [{"value": "only"}],
					"paginatorInfo": {
						"count": 1, "currentPage": 1, "hasMorePages": false,
						"lastPage": 1, "perPage": 10, "total": 1
					}
				}
			}
		}`)
	})
	defer srv.Close()

	type Item struct {
		Value string `json:"value"`
	}

	c := New(srv.URL, "token")
	items, err := ExecuteAll[Item](context.Background(), c, "{ result: items(page: $page) { data { value } paginatorInfo { hasMorePages } } }", nil)
	if err != nil {
		t.Fatalf("ExecuteAll returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Value != "only" {
		t.Errorf("item value = %q, want %q", items[0].Value, "only")
	}
}

func TestGraphQLErrors_ErrorString(t *testing.T) {
	errs := GraphQLErrors{
		{Message: "first"},
		{Message: "second"},
	}
	got := errs.Error()
	want := "first; second"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestExecute_NilResult(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"mutation":"ok"}}`)
	})
	defer srv.Close()

	c := New(srv.URL, "token")
	// Passing nil result should not panic or error.
	err := c.Execute(context.Background(), "mutation { doThing }", nil, nil)
	if err != nil {
		t.Fatalf("Execute with nil result returned error: %v", err)
	}
}

func TestWithTimeout(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		// Simulate a slow server that takes longer than the client timeout.
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"ok":true}}`)
	})
	defer srv.Close()

	c := New(srv.URL, "token", WithTimeout(100*time.Millisecond))

	err := c.Execute(context.Background(), "{ health }", nil, nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "Client.Timeout") && !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("expected timeout-related error, got: %v", err)
	}
}

func TestWithTimeout_Default(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"ok":true}}`)
	})
	defer srv.Close()

	// No WithTimeout option — should use the default 30s and succeed quickly.
	c := New(srv.URL, "token")

	var result struct {
		OK bool `json:"ok"`
	}
	err := c.Execute(context.Background(), "{ health }", nil, &result)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK {
		t.Error("expected ok=true")
	}
}

func TestExecute_HTTPError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})
	defer srv.Close()

	c := New(srv.URL, "token")
	err := c.Execute(context.Background(), "{ health }", nil, nil)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code 500: %v", err)
	}
}

func TestExecute_NonJSONResponse(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `<!DOCTYPE html><html><body><h1>Not Found</h1></body></html>`)
	})
	defer srv.Close()

	c := New(srv.URL, "token")
	err := c.Execute(context.Background(), "{ health }", nil, nil)
	if err == nil {
		t.Fatal("expected error for HTML response, got nil")
	}
	if !strings.Contains(err.Error(), "expected JSON") {
		t.Errorf("error should mention expected JSON: %v", err)
	}
	if !strings.Contains(err.Error(), "text/html") {
		t.Errorf("error should mention content type: %v", err)
	}
	// Must NOT contain the actual HTML body.
	if strings.Contains(err.Error(), "<!DOCTYPE") {
		t.Error("error should not contain raw HTML body")
	}
}

// A certificate that fails verification will fail identically on every
// retry. Retrying burned ~7s before surfacing the real problem, which in
// containers is almost always a missing CA bundle.
func TestExecute_DoesNotRetryCertificateErrors(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	// Count connections, not handler calls: the handshake fails before the
	// handler ever runs, so a request counter would read 0 whether we retry
	// once or ten times. Each retry dials a new connection.
	var conns atomic.Int32
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	// The client will fail the handshake by design; don't let the server
	// spew that onto the shared logger.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()

	// Default client: does not trust the test server's self-signed cert.
	c := New(srv.URL, "token")

	err := c.Execute(context.Background(), "query { viewer { id } }", nil, nil)

	if err == nil {
		t.Fatal("expected a certificate verification error")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected a certificate error, got: %v", err)
	}
	// Only the exhausted-retry path wraps with "after N attempts".
	if strings.Contains(err.Error(), "attempts") {
		t.Errorf("certificate error should not be retried, got: %v", err)
	}
	if got := conns.Load(); got != 1 {
		t.Errorf("dialled %d times, want exactly 1: certificate errors must not be retried", got)
	}
}

// The real hires query returns every row plus a "DOWNSTREAM_SERVICE_ERROR" for
// two fields on each of them. Discarding the rows because errors is non-empty
// made the whole resource unusable.
func TestExecute_PartialResponseKeepsData(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"data": {"hires": {"data": [{"id": "SGlyZTox", "triggersApproval": null}]}},
			"errors": [
				{"message": "Internal server error", "path": ["hires", "data", 0, "triggersApproval"]}
			]
		}`)
	})
	defer srv.Close()

	var warnings bytes.Buffer
	c := New(srv.URL, "token", WithWarnWriter(&warnings))

	var result map[string]any
	if err := c.Execute(context.Background(), "query Hires { hires }", nil, &result); err != nil {
		t.Fatalf("partial response should not fail: %v", err)
	}

	hires, ok := result["hires"].(map[string]any)
	if !ok {
		t.Fatalf("data was dropped, got %#v", result)
	}
	if rows, _ := hires["data"].([]any); len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}

	// The failure still has to reach the operator, on stderr and with the path
	// so they can tell which field broke.
	if got := warnings.String(); !strings.Contains(got, "hires.data[0].triggersApproval: Internal server error") {
		t.Errorf("warning = %q, want the failing path", got)
	}
}

func TestExecute_NullDataWithErrorsStillFails(t *testing.T) {
	for name, body := range map[string]string{
		"explicit null": `{"data": null, "errors": [{"message": "Unauthenticated."}]}`,
		"absent key":    `{"errors": [{"message": "Unauthenticated."}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, body)
			})
			defer srv.Close()

			var warnings bytes.Buffer
			c := New(srv.URL, "token", WithWarnWriter(&warnings))
			err := c.Execute(context.Background(), "query { viewer }", nil, nil)
			if err == nil {
				t.Fatal("expected an error when no data resolved")
			}
			if warnings.Len() != 0 {
				t.Errorf("a hard failure should not warn, got %q", warnings.String())
			}
		})
	}
}

func TestExecute_PartialResponseIsNotCached(t *testing.T) {
	var calls atomic.Int32
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"data": {"hires": []},
			"errors": [{"message": "Internal server error", "path": ["hires", "total"]}]
		}`)
	})
	defer srv.Close()

	c := New(srv.URL, "token", WithCache(NewCache(time.Minute)), WithWarnWriter(&bytes.Buffer{}))
	query := "query Hires { hires }"
	for range 2 {
		if err := c.Execute(context.Background(), query, nil, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Caching a partial response would serve the missing fields as real until
	// the entry expires.
	if got := calls.Load(); got != 2 {
		t.Errorf("request count = %d, want 2 (partial response was cached)", got)
	}
}

func TestGraphQLError_Location(t *testing.T) {
	for name, tc := range map[string]struct {
		path []any
		want string
	}{
		"nested with index": {[]any{"hires", "data", float64(0), "triggersApproval"}, "hires.data[0].triggersApproval"},
		"leading index":     {[]any{float64(2), "id"}, "[2].id"},
		"single field":      {[]any{"viewer"}, "viewer"},
		"no path":           {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := (GraphQLError{Path: tc.path}).Location(); got != tc.want {
				t.Errorf("Location() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGraphQLErrors_ErrorStringIncludesPathsAndCaps(t *testing.T) {
	errs := GraphQLErrors{{Message: "boom", Path: []any{"hires", float64(0), "field"}}}
	if got, want := errs.Error(), "hires[0].field: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	var many GraphQLErrors
	for range maxReportedErrors + 3 {
		many = append(many, GraphQLError{Message: "Internal server error"})
	}
	got := many.Error()
	if !strings.HasSuffix(got, "(and 3 more)") {
		t.Errorf("Error() = %q, want a truncation suffix", got)
	}
	if n := strings.Count(got, "Internal server error"); n != maxReportedErrors {
		t.Errorf("rendered %d errors, want %d", n, maxReportedErrors)
	}
}

func TestWrapOperation(t *testing.T) {
	tests := []struct {
		name string
		op   string
		err  error
		want string
	}{
		{
			// The bug: the generated wrapper added "profile: " to an error
			// whose GraphQL path already rendered as "profile".
			name: "does not repeat a prefix the error already has",
			op:   "profile",
			err:  GraphQLErrors{{Message: "Unauthenticated.", Path: []any{"profile"}}},
			want: "profile: Unauthenticated.",
		},
		{
			name: "adds context to a GraphQL error with no path",
			op:   "profile",
			err:  GraphQLErrors{{Message: "Unauthenticated."}},
			want: "profile: Unauthenticated.",
		},
		{
			name: "adds context to a transport error",
			op:   "hires",
			err:  fmt.Errorf("sending request: connection refused"),
			want: "hires: sending request: connection refused",
		},
		{
			name: "keeps a deeper path that merely starts with the operation",
			op:   "hires",
			err:  GraphQLErrors{{Message: "boom", Path: []any{"hires", "data", float64(0), "triggersApproval"}}},
			want: "hires.data[0].triggersApproval: boom",
		},
		{
			// "hiresomething:" must not be mistaken for the "hires" prefix.
			name: "does not treat a longer operation name as a match",
			op:   "hire",
			err:  GraphQLErrors{{Message: "boom", Path: []any{"hires"}}},
			want: "hire: hires: boom",
		},
		{
			name: "nil error stays nil",
			op:   "hires",
			err:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WrapOperation(tt.op, tt.err)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected an error, got nil")
			}
			if got.Error() != tt.want {
				t.Errorf("WrapOperation() = %q, want %q", got.Error(), tt.want)
			}
		})
	}
}

// WrapOperation must not break errors.As/Is for callers inspecting the cause.
func TestWrapOperationPreservesErrorIdentity(t *testing.T) {
	orig := GraphQLErrors{{Message: "Unauthenticated."}}
	wrapped := WrapOperation("profile", orig)

	if !IsAuthError(errors.Unwrap(wrapped)) {
		t.Error("unwrapping a wrapped GraphQL error should still detect the auth failure")
	}

	// Unprefixed pass-through keeps the concrete type directly.
	located := GraphQLErrors{{Message: "Unauthenticated.", Path: []any{"profile"}}}
	if !IsAuthError(WrapOperation("profile", located)) {
		t.Error("a passed-through GraphQL error should keep its type")
	}
}

// The API classifies every error it can with extensions.code, and a server-side
// handler may attach a trace id. Both used to be decoded and then dropped, so a
// failed mutation read as a bare "Internal server error" with nothing to hand
// to whoever has the logs.
func TestGraphQLError_ErrorRendersCodeAndTrace(t *testing.T) {
	tests := []struct {
		name string
		err  GraphQLError
		want string
	}{
		{
			"code only",
			GraphQLError{Message: `Field "date" of required type "Date!" was not provided.`, Extensions: map[string]any{"code": "BAD_USER_INPUT"}},
			`Field "date" of required type "Date!" was not provided. [BAD_USER_INPUT]`,
		},
		{
			"path, code and trace",
			GraphQLError{Message: "Internal server error", Path: []any{"terminateHire"}, Extensions: map[string]any{"code": "INTERNAL", "trace_id": "8f2c1a"}},
			"terminateHire: Internal server error [INTERNAL, trace 8f2c1a]",
		},
		{
			"trace under a camelCase key",
			GraphQLError{Message: "boom", Extensions: map[string]any{"requestId": "req-42"}},
			"boom [trace req-42]",
		},
		{
			"only the first trace key is used",
			GraphQLError{Message: "boom", Extensions: map[string]any{"trace_id": "a", "requestId": "b"}},
			"boom [trace a]",
		},
		{
			"non-string and empty values are ignored",
			GraphQLError{Message: "boom", Extensions: map[string]any{"code": 42, "trace_id": ""}},
			"boom",
		},
		{
			"no extensions renders as before",
			GraphQLError{Message: "boom", Path: []any{"hires", float64(0), "field"}},
			"hires[0].field: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A gateway in front of the endpoint that demands HTTP Basic auth used to be
// reported as "endpoint returned text/html (expected JSON). Check that the
// endpoint URL is correct." — true but useless. The Bearer token occupies the
// Authorization header, so Basic auth cannot be satisfied; the user has almost
// always reached for the UI hostname instead of the API hostname.
func TestExecute_BasicAuthGatewayIsNamed(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		authenticate string
		contentType  string
		body         string
		wantContains string
		wantAbsent   string
	}{
		{
			"basic auth challenge with HTML body",
			http.StatusUnauthorized, `Basic realm="staging"`, "text/html; charset=utf-8", "<html>Unauthorized</html>",
			"HTTP Basic authentication", "expected JSON",
		},
		{
			"basic auth challenge, odd casing",
			http.StatusUnauthorized, `basic realm="x"`, "text/plain", "nope",
			"UI hostname", "",
		},
		{
			"401 from the API itself keeps the ordinary path",
			http.StatusUnauthorized, "", "application/json", `{"errors":[{"message":"Unauthenticated."}]}`,
			"Unauthenticated", "Basic authentication",
		},
		{
			"Bearer challenge is not Basic",
			http.StatusUnauthorized, `Bearer realm="api"`, "text/html", "<html/>",
			"expected JSON", "Basic authentication",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.authenticate != "" {
					w.Header().Set("WWW-Authenticate", tt.authenticate)
				}
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := New(srv.URL, "token")
			var out map[string]any
			err := c.Execute(context.Background(), "{ viewer { id } }", nil, &out)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tt.wantContains)
			}
			if tt.wantAbsent != "" && strings.Contains(err.Error(), tt.wantAbsent) {
				t.Errorf("error = %q, must not mention %q", err.Error(), tt.wantAbsent)
			}
		})
	}
}

func TestAPIHostHint(t *testing.T) {
	tests := []struct{ endpoint, want string }{
		{"https://demo.sand.aws.worksome.com/graphql", "https://demo-api.sand.aws.worksome.com/graphql"},
		{"https://demo-api.sand.aws.worksome.com/graphql", ""},
		{"https://api.worksome.com/graphql", ""},
		{"http://127.0.0.1:8099/graphql", ""},
		{"not a url", ""},
		{"https://worksome:secure@demo.sand.hz.worksome.com/graphql?a=1#f", "https://demo-api.sand.hz.worksome.com/graphql"},
		{"https://demo.worksome.com:8443/graphql", "https://demo-api.worksome.com:8443/graphql"},
		{"https://API.worksome.com/graphql", ""},
		{"https://demo-API.sand.hz.worksome.com/graphql", ""},
		{"https://DEMO.sand.hz.worksome.COM/graphql", "https://demo-api.sand.hz.worksome.com/graphql"},
	}
	for _, tt := range tests {
		if got := apiHostHint(tt.endpoint); got != tt.want {
			t.Errorf("apiHostHint(%q) = %q, want %q", tt.endpoint, got, tt.want)
		}
	}
}

// The gateway replaces the User-Agent before the API sees it, so User-Agent
// alone cannot attribute CLI traffic past that hop. Apollo's client-awareness
// headers survive it and the gateway already tags its spans with them.
func TestExecute_SendsApolloClientAwarenessHeaders(t *testing.T) {
	tests := []struct {
		name              string
		userAgent         string
		wantName, wantVer string
	}{
		{"name and version", "worksome-cli/0.7.0", "worksome-cli", "0.7.0"},
		{"no version", "worksome-cli", "worksome-cli", ""},
		{"version containing a slash", "worksome-cli/1.0/rc1", "worksome-cli", "1.0/rc1"},
		{"platform detail stays out of the version", "worksome-cli/0.7.0 (darwin/arm64)", "worksome-cli", "0.7.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("apollographql-client-name"); got != tt.wantName {
					t.Errorf("client-name = %q, want %q", got, tt.wantName)
				}
				if got := r.Header.Get("apollographql-client-version"); got != tt.wantVer {
					t.Errorf("client-version = %q, want %q", got, tt.wantVer)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"data":{"hello":"world"}}`)
			})
			defer srv.Close()

			c := New(srv.URL, "test-token", WithUserAgent(tt.userAgent))
			if err := c.Execute(context.Background(), "query Accounts { accounts { id } }", nil, nil); err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
		})
	}
}

// The User-Agent names the platform so Datadog can break usage down by OS, but
// the Apollo client-version header must stay a bare version.
func TestClientNameVersion(t *testing.T) {
	tests := []struct{ ua, wantName, wantVer string }{
		{"worksome-cli/0.7.0 (darwin/arm64)", "worksome-cli", "0.7.0"},
		{"worksome-cli/0.7.0 (linux/amd64)", "worksome-cli", "0.7.0"},
		{"worksome-cli/dev (windows/arm64)", "worksome-cli", "dev"},
		{"worksome-cli/0.7.0", "worksome-cli", "0.7.0"},
		{"worksome-cli", "worksome-cli", ""},
		{"worksome-cli (darwin/arm64)", "worksome-cli", ""},
		{"worksome-cli/ (darwin/arm64)", "worksome-cli", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		name, ver := clientNameVersion(tt.ua)
		if name != tt.wantName || ver != tt.wantVer {
			t.Errorf("clientNameVersion(%q) = %q, %q; want %q, %q", tt.ua, name, ver, tt.wantName, tt.wantVer)
		}
	}
}

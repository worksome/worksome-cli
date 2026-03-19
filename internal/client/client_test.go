package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
		fmt.Fprint(w, `{"data":{"greeting":"world"}}`)
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
		fmt.Fprint(w, `{
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
		fmt.Fprint(w, `{"data":{"ok":true}}`)
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
		fmt.Fprint(w, `{"data":{"ping":"pong"}}`)
	})
	defer srv.Close()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0) // Remove timestamps for deterministic output.
	defer log.SetOutput(nil)

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
		fmt.Fprint(w, pages[pageNum-1])
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
		fmt.Fprint(w, `{
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
		fmt.Fprint(w, `{"data":{"mutation":"ok"}}`)
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
		fmt.Fprint(w, `{"data":{"ok":true}}`)
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
		fmt.Fprint(w, `{"data":{"ok":true}}`)
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
		fmt.Fprint(w, `<!DOCTYPE html><html><body><h1>Not Found</h1></body></html>`)
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

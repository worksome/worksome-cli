package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCache_HitAndMiss(t *testing.T) {
	cache := NewCache(5 * time.Second)

	query := "query { user { name } }"
	vars := map[string]any{"id": 1}

	// Miss on empty cache.
	if _, ok := cache.Get(query, vars); ok {
		t.Fatal("expected cache miss on empty cache")
	}

	// Set and hit.
	data := json.RawMessage(`{"user":{"name":"Alice"}}`)
	cache.Set(query, vars, data)

	got, ok := cache.Get(query, vars)
	if !ok {
		t.Fatal("expected cache hit after Set")
	}
	if string(got) != string(data) {
		t.Errorf("cached data = %s, want %s", got, data)
	}
}

func TestCache_Expiry(t *testing.T) {
	cache := NewCache(1 * time.Millisecond)

	query := "query { user { name } }"
	cache.Set(query, nil, json.RawMessage(`{"user":{"name":"Bob"}}`))

	// Wait for the entry to expire.
	time.Sleep(5 * time.Millisecond)

	if _, ok := cache.Get(query, nil); ok {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestCache_DifferentVarsAreDifferentKeys(t *testing.T) {
	cache := NewCache(5 * time.Second)

	query := "query { user { name } }"
	cache.Set(query, map[string]any{"id": 1}, json.RawMessage(`{"name":"Alice"}`))
	cache.Set(query, map[string]any{"id": 2}, json.RawMessage(`{"name":"Bob"}`))

	got1, ok := cache.Get(query, map[string]any{"id": 1})
	if !ok {
		t.Fatal("expected hit for id=1")
	}
	if string(got1) != `{"name":"Alice"}` {
		t.Errorf("got %s, want Alice", got1)
	}

	got2, ok := cache.Get(query, map[string]any{"id": 2})
	if !ok {
		t.Fatal("expected hit for id=2")
	}
	if string(got2) != `{"name":"Bob"}` {
		t.Errorf("got %s, want Bob", got2)
	}
}

func TestCache_NilVars(t *testing.T) {
	cache := NewCache(5 * time.Second)

	query := "query { users { name } }"
	cache.Set(query, nil, json.RawMessage(`{"users":[]}`))

	got, ok := cache.Get(query, nil)
	if !ok {
		t.Fatal("expected hit with nil vars")
	}
	if string(got) != `{"users":[]}` {
		t.Errorf("got %s", got)
	}
}

func TestExecute_CacheHit(t *testing.T) {
	var calls atomic.Int32
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"greeting":"world"}}`)
	})
	defer srv.Close()

	cache := NewCache(5 * time.Second)
	c := New(srv.URL, "token", WithCache(cache))

	query := "query { hello }"

	// First call: cache miss, hits the server.
	var r1 struct {
		Greeting string `json:"greeting"`
	}
	if err := c.Execute(context.Background(), query, nil, &r1); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if r1.Greeting != "world" {
		t.Errorf("r1.Greeting = %q, want world", r1.Greeting)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 server call, got %d", calls.Load())
	}

	// Second call: cache hit, server should not be called again.
	var r2 struct {
		Greeting string `json:"greeting"`
	}
	if err := c.Execute(context.Background(), query, nil, &r2); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if r2.Greeting != "world" {
		t.Errorf("r2.Greeting = %q, want world", r2.Greeting)
	}
	if calls.Load() != 1 {
		t.Errorf("expected server to be called once (cached), got %d", calls.Load())
	}
}

func TestExecute_MutationBypassesCache(t *testing.T) {
	var calls atomic.Int32
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"createUser":{"id":"1"}}}`)
	})
	defer srv.Close()

	cache := NewCache(5 * time.Second)
	c := New(srv.URL, "token", WithCache(cache))

	mutation := "mutation { createUser(name: \"Alice\") { id } }"

	// Execute mutation twice: both should hit the server.
	for i := range 2 {
		if err := c.Execute(context.Background(), mutation, nil, nil); err != nil {
			t.Fatalf("Execute #%d: %v", i+1, err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 server calls for mutations, got %d", got)
	}
}

func TestExecute_AnonymousQueryIsCached(t *testing.T) {
	var calls atomic.Int32
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"ok":true}}`)
	})
	defer srv.Close()

	cache := NewCache(5 * time.Second)
	c := New(srv.URL, "token", WithCache(cache))

	// Anonymous query (starts with "{") should be cacheable.
	query := "{ health }"
	for range 3 {
		if err := c.Execute(context.Background(), query, nil, nil); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 server call (anonymous query cached), got %d", got)
	}
}

func TestExecute_NoCacheOption(t *testing.T) {
	var calls atomic.Int32
	srv := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"ok":true}}`)
	})
	defer srv.Close()

	// No WithCache option: every call hits the server.
	c := New(srv.URL, "token")

	query := "query { health }"
	for range 3 {
		if err := c.Execute(context.Background(), query, nil, nil); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("expected 3 server calls without cache, got %d", got)
	}
}

func TestIsQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"query { user { name } }", true},
		{"query GetUser($id: ID!) { user(id: $id) { name } }", true},
		{"{ health }", true},
		{"  query { user { name } }", true},
		{"\n  { health }", true},
		{"mutation { createUser { id } }", false},
		{"mutation CreateUser($name: String!) { createUser(name: $name) { id } }", false},
		{"subscription { userCreated { id } }", false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			if got := isQuery(tt.query); got != tt.want {
				t.Errorf("isQuery(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

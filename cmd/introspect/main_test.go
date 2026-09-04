package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchIntrospectionOmitsAuthorizationWithoutToken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{name: "no token", token: "", want: ""},
		{name: "with token", token: "abc", want: "Bearer abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				_, _ = w.Write([]byte(`{"data":{"__schema":{"types":[]}}}`))
			}))
			defer srv.Close()

			if _, err := fetchIntrospection(srv.URL, tc.token); err != nil {
				t.Fatalf("fetchIntrospection: %v", err)
			}
			if got != tc.want {
				t.Errorf("Authorization = %q, want %q", got, tc.want)
			}
		})
	}
}

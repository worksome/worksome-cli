package main

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/worksome/worksome-cli/internal/buildinfo"
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

// The nightly schema-drift workflow runs this tool against the API, so its
// traffic has to attribute to our tooling rather than to Go's default agent.
func TestFetchIntrospectionIdentifiesItself(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"data":{"__schema":{"types":[]}}}`))
	}))
	defer srv.Close()

	if _, err := fetchIntrospection(srv.URL, ""); err != nil {
		t.Fatalf("fetchIntrospection: %v", err)
	}

	ua := got.Get("User-Agent")
	if !strings.HasPrefix(ua, "worksome-cli-introspect/") {
		t.Errorf("User-Agent = %q, want a worksome-cli-introspect/... prefix", ua)
	}
	if !strings.Contains(ua, runtime.GOOS) {
		t.Errorf("User-Agent = %q, want the platform named", ua)
	}
	// Apollo's client awareness is what survives the gateway, which replaces
	// the User-Agent before the API sees it.
	if name := got.Get("apollographql-client-name"); name != "worksome-cli-introspect" {
		t.Errorf("apollographql-client-name = %q, want worksome-cli-introspect", name)
	}
	if ver := got.Get("apollographql-client-version"); ver != buildinfo.Version {
		t.Errorf("apollographql-client-version = %q, want %q", ver, buildinfo.Version)
	}
}

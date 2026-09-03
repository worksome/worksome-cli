package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// Client is a GraphQL client that sends queries to a Worksome API endpoint.
type Client struct {
	endpoint   string
	token      string
	httpClient *http.Client
	verbose    bool
	timeout    time.Duration
	userAgent  string
	cache      *Cache
	warnw      io.Writer
	fields     []string
}

// Option configures the Client.
type Option func(*Client)

// WithVerbose enables or disables verbose request/response logging to stderr.
func WithVerbose(v bool) Option {
	return func(c *Client) {
		c.verbose = v
	}
}

// WithTimeout sets the timeout for HTTP requests. If zero, the default 30s
// timeout is used. This is ignored when a custom HTTP client is provided via
// WithHTTPClient.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.timeout = d
	}
}

// WithUserAgent sets the User-Agent header for HTTP requests.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		c.userAgent = ua
	}
}

// WithHTTPClient overrides the default HTTP client used for requests.
// When set, any timeout configured via WithTimeout is ignored.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// WithCache enables response caching for read queries. Mutations are never
// cached. Pass a *Cache created with NewCache to share the cache across
// multiple clients, or create one per client.
func WithCache(cache *Cache) Option {
	return func(c *Client) {
		c.cache = cache
	}
}

// WithWarnWriter sets where non-fatal warnings (such as field errors in a
// partial response) are written. Defaults to os.Stderr so warnings stay out of
// piped stdout.
func WithWarnWriter(w io.Writer) Option {
	return func(c *Client) {
		c.warnw = w
	}
}

// WithFields narrows the GraphQL selection set of every operation to the given
// dot-notation field paths (e.g. "id", "worker.name"). Operations whose shape
// cannot be narrowed safely are sent unchanged.
func WithFields(fields []string) Option {
	return func(c *Client) {
		c.fields = slices.Clone(fields)
	}
}

// New creates a new GraphQL Client for the given endpoint and bearer token.
func New(endpoint, token string, opts ...Option) *Client {
	c := &Client{
		endpoint: endpoint,
		token:    token,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.userAgent == "" {
		c.userAgent = "worksome-cli"
	}
	if c.warnw == nil {
		c.warnw = os.Stderr
	}
	// If no custom HTTP client was provided, create a default one with the
	// configured (or default 30s) timeout.
	if c.httpClient == nil {
		timeout := c.timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		c.httpClient = &http.Client{Timeout: timeout}
	}
	return c
}

// graphqlRequest is the JSON body sent to the GraphQL endpoint.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphqlResponse is the top-level JSON envelope returned by the GraphQL endpoint.
type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors GraphQLErrors   `json:"errors,omitempty"`
}

// GraphQLError represents a single error returned by a GraphQL API.
type GraphQLError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Error implements the error interface. The response path is included when the
// API reports one, since a bare "Internal server error" repeated once per field
// says nothing about which field failed. The extensions code and any trace id
// follow in brackets, so a failure can be classified and looked up in the
// server's logs without re-running under --verbose.
func (e GraphQLError) Error() string {
	msg := e.Message
	if loc := e.Location(); loc != "" {
		msg = loc + ": " + msg
	}
	if tag := e.tag(); tag != "" {
		msg += " [" + tag + "]"
	}
	return msg
}

// traceKeys are the extension keys under which GraphQL servers commonly report
// an id that can be found in their logs. Lighthouse sets none by default; a
// platform-side error handler may add one, and it should reach the user when
// it does.
var traceKeys = []string{"trace_id", "traceId", "request_id", "requestId", "event_id", "eventId", "correlationId"}

// tag renders the machine-readable parts of an error's extensions: the code
// (BAD_USER_INPUT, FORBIDDEN, ...) and the first trace id present.
func (e GraphQLError) tag() string {
	var parts []string
	if code, ok := e.Extensions["code"].(string); ok && code != "" {
		parts = append(parts, code)
	}
	for _, k := range traceKeys {
		if id, ok := e.Extensions[k].(string); ok && id != "" {
			parts = append(parts, "trace "+id)
			break
		}
	}
	return strings.Join(parts, ", ")
}

// Location renders the error's response path in dotted form, e.g.
// ["hires","data",0,"triggersApproval"] becomes "hires.data[0].triggersApproval".
// It returns an empty string when the API reported no path.
func (e GraphQLError) Location() string {
	var b strings.Builder
	for _, seg := range e.Path {
		switch v := seg.(type) {
		case string:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(v)
		case float64: // list indices arrive as JSON numbers
			fmt.Fprintf(&b, "[%d]", int(v))
		default:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			fmt.Fprint(&b, v)
		}
	}
	return b.String()
}

// GraphQLErrors is a slice of GraphQLError that implements the error interface.
type GraphQLErrors []GraphQLError

// maxReportedErrors caps how many individual errors are rendered. A single page
// can carry one error per row per field, which is noise past the first few.
const maxReportedErrors = 5

// Error returns a combined error message from all GraphQL errors.
func (errs GraphQLErrors) Error() string {
	shown := errs
	var suffix string
	if len(errs) > maxReportedErrors {
		shown = errs[:maxReportedErrors]
		suffix = fmt.Sprintf("; (and %d more)", len(errs)-maxReportedErrors)
	}
	msgs := make([]string, len(shown))
	for i, e := range shown {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ") + suffix
}

// Warning renders the errors as a multi-line stderr notice for the case where
// the API returned usable data alongside them.
func (errs GraphQLErrors) Warning() string {
	var b strings.Builder
	fmt.Fprintf(&b, "warning: the API returned %s alongside the data:\n", pluralise(len(errs), "error"))
	shown := errs
	if len(errs) > maxReportedErrors {
		shown = errs[:maxReportedErrors]
	}
	for _, e := range shown {
		fmt.Fprintf(&b, "  %s\n", e.Error())
	}
	if len(errs) > len(shown) {
		fmt.Fprintf(&b, "  (and %d more)\n", len(errs)-len(shown))
	}
	return b.String()
}

func pluralise(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// IsAuthError reports whether the errors contain an authentication failure.
func (errs GraphQLErrors) IsAuthError() bool {
	for _, e := range errs {
		if strings.Contains(e.Message, "Unauthenticated") {
			return true
		}
	}
	return false
}

// IsAuthError is a convenience helper that checks whether an error is a GraphQL
// authentication error (i.e. contains "Unauthenticated").
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	if gqlErrs, ok := err.(GraphQLErrors); ok {
		return gqlErrs.IsAuthError()
	}
	return false
}

const (
	maxRetries   = 3
	baseBackoff  = 1 * time.Second
	backoffScale = 2
)

// isQuery returns true when the GraphQL operation is a read query (not a
// mutation or subscription). It checks for a leading "query" keyword or an
// anonymous query (starts with "{").
func isQuery(query string) bool {
	q := strings.TrimSpace(query)
	return strings.HasPrefix(q, "query") || strings.HasPrefix(q, "{")
}

// Execute sends a GraphQL query and unmarshals the response data into result.
// It retries on transient network errors with exponential backoff (1s, 2s, 4s).
// When a Cache is configured via WithCache, read queries are served from the
// cache on hit and stored on miss. Mutations are never cached.
func (c *Client) Execute(ctx context.Context, query string, variables map[string]any, result any) error {
	if len(c.fields) > 0 {
		pruned, err := pruneQuery(query, c.fields)
		if err != nil {
			return err
		}
		query = pruned
	}

	cacheable := c.cache != nil && isQuery(query)

	// Check cache before making a network request.
	if cacheable {
		if data, ok := c.cache.Get(query, variables); ok {
			if c.verbose {
				log.Printf("[graphql] cache hit for query")
			}
			if result != nil {
				if err := json.Unmarshal(data, result); err != nil {
					return fmt.Errorf("decoding cached data: %w", err)
				}
			}
			return nil
		}
	}

	reqBody := graphqlRequest{
		Query:     query,
		Variables: variables,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	if c.verbose {
		log.Printf("[graphql] --> POST %s\n%s", c.endpoint, string(payload))
	}

	var lastErr error
	for attempt := range maxRetries {
		var respBody []byte
		respBody, lastErr = c.doRequest(ctx, payload)
		if lastErr != nil {
			// Don't retry non-transient errors (HTTP status codes, context cancellation).
			if ctx.Err() != nil {
				return fmt.Errorf("request cancelled: %w", ctx.Err())
			}
			var he *httpError
			if errors.As(lastErr, &he) {
				return lastErr
			}
			// A certificate that doesn't verify won't start verifying on
			// attempt three. Common in slim containers with no CA bundle.
			var cve *tls.CertificateVerificationError
			if errors.As(lastErr, &cve) {
				return lastErr
			}
			if c.verbose {
				log.Printf("[graphql] attempt %d/%d failed: %v", attempt+1, maxRetries, lastErr)
			}
			backoff := baseBackoff * time.Duration(pow(backoffScale, attempt))
			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return fmt.Errorf("request cancelled during backoff: %w", ctx.Err())
			}
		}

		if c.verbose {
			log.Printf("[graphql] <-- %s", string(respBody))
		}

		var gqlResp graphqlResponse
		if err := json.Unmarshal(respBody, &gqlResp); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}

		// GraphQL reports field-level failures in "errors" while still
		// returning every field that did resolve. Per the spec, data is null
		// only when the request could not be executed at all — that is the
		// case that deserves a non-zero exit. Anything else is a partial
		// success: surface the failures on stderr and hand back the data.
		partial := len(gqlResp.Errors) > 0
		if partial && !hasData(gqlResp.Data) {
			return gqlResp.Errors
		}
		if partial {
			_, _ = fmt.Fprint(c.warnw, gqlResp.Errors.Warning())
		}

		// Store successful query responses in the cache. A partial response is
		// not cached: the missing fields would be served as though they were
		// real for the lifetime of the entry.
		if cacheable && !partial && hasData(gqlResp.Data) {
			c.cache.Set(query, variables, gqlResp.Data)
		}

		if result != nil && gqlResp.Data != nil {
			if err := json.Unmarshal(gqlResp.Data, result); err != nil {
				return fmt.Errorf("decoding data: %w", err)
			}
		}

		return nil
	}

	return fmt.Errorf("request failed after %d attempts: %w", maxRetries, lastErr)
}

// httpError is a non-retryable error indicating an unexpected HTTP status code.
type httpError struct {
	StatusCode int
	Body       string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, e.Body)
}

// doRequest performs a single HTTP POST and returns the raw response body.
func (c *Client) doRequest(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		contentType := resp.Header.Get("Content-Type")
		bodyStr := string(body)
		// A 401 demanding Basic auth is a gateway in front of the endpoint, not
		// a bad token: the Bearer token occupies the Authorization header, so
		// the CLI cannot satisfy it. Typically the UI hostname was used where
		// the API hostname was meant. Say so, instead of "check the URL".
		if resp.StatusCode == http.StatusUnauthorized &&
			strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp.Header.Get("WWW-Authenticate"))), "basic") {
			return nil, &httpError{
				StatusCode: resp.StatusCode,
				Body:       "this endpoint is behind HTTP Basic authentication, which the CLI cannot pass — you may be using the UI hostname instead of the API hostname",
			}
		}
		// Detect non-JSON responses (HTML error pages, etc.) and show a clean message
		if !strings.Contains(contentType, "json") || strings.HasPrefix(strings.TrimSpace(bodyStr), "<") {
			return nil, &httpError{
				StatusCode: resp.StatusCode,
				Body:       fmt.Sprintf("endpoint returned %s (expected JSON). Check that the endpoint URL is correct.", contentType),
			}
		}
		return nil, &httpError{StatusCode: resp.StatusCode, Body: bodyStr}
	}

	return body, nil
}

// hasData reports whether the response carries a usable data envelope. An
// absent key and a literal JSON null both mean "nothing resolved".
func hasData(data json.RawMessage) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// pow computes base^exp for small non-negative integers.
func pow(base, exp int) int {
	result := 1
	for range exp {
		result *= base
	}
	return result
}

// WrapOperation prefixes an error with the GraphQL operation name for context.
//
// A GraphQL error already renders its own response path, whose first segment
// is usually the operation name, so a naive prefix produced "profile: profile:
// Unauthenticated." The prefix is skipped when it would just be repeated, and
// still applied to transport and decode errors, which carry no location.
func WrapOperation(operation string, err error) error {
	if err == nil {
		return nil
	}
	// Skip the prefix when the message already opens with this operation as a
	// whole path segment: "profile: ..." or "hires.data[0].field: ...". The
	// delimiter check stops "hire" matching an error located at "hires".
	if msg := err.Error(); strings.HasPrefix(msg, operation) {
		switch rest := msg[len(operation):]; {
		case rest == "", rest[0] == ':', rest[0] == '.', rest[0] == '[':
			return err
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

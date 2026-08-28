# Worksome CLI — Design Specification

## Overview

A multiplatform CLI wrapper around the Worksome GraphQL API, built in Go. Designed for both human users and AI agents. Full API coverage via code generation from a vendored GraphQL schema, with a clear sync workflow for API updates.

- **Endpoint:** `https://api.worksome.com/graphql`
- **Auth:** Personal Access Tokens (Bearer)
- **API surface:** ~80 queries, ~117 mutations across 14+ domains

## Project Structure

```
worksome-cli/
├── cmd/
│   ├── worksome/          # Main CLI entrypoint
│   │   └── main.go
│   └── generate/          # Codegen tool
│       └── main.go
├── internal/
│   ├── client/            # GraphQL client (auth, HTTP, error handling)
│   ├── config/            # Config file management (~/.worksome/config.yaml)
│   ├── output/            # Output formatting (JSON, table, TTY detection)
│   ├── codegen/           # Schema parser + template engine
│   │   ├── parser.go      # GraphQL schema → intermediate representation
│   │   ├── generator.go   # IR → Go code via templates
│   │   └── templates/     # Go text/templates for types, commands, tests
│   └── generated/         # All codegen output (committed to repo)
│       ├── types/         # Go structs for GraphQL types, enums, inputs
│       ├── queries/       # Query/mutation functions
│       └── commands/      # Cobra command definitions
├── schema/
│   └── schema.graphql     # Vendored GraphQL schema (source of truth)
├── test/
│   ├── fixtures/          # Generated response fixtures
│   └── integration/       # Integration tests against live/mock API
├── Makefile               # sync-schema, generate, build, test
├── go.mod
└── go.sum
```

**Key decisions:**
- `internal/generated/` is committed — reviewable code, not a build artifact
- `cmd/generate/` is a separate binary so codegen dependencies don't leak into the main CLI
- `schema/schema.graphql` is the single source of truth for all generation

## Authentication & Configuration

**Config file:** `~/.worksome/config.yaml`

```yaml
profiles:
  default:
    token: "wks_pat_abc123..."
    endpoint: "https://api.worksome.com/graphql"
  staging:
    token: "wks_pat_xyz789..."
    endpoint: "https://api.staging.worksome.com/graphql"
current_profile: default
```

**Auth commands:**
- `worksome auth login` — prompts for PAT, validates with `viewer` query, stores in config
- `worksome auth status` — shows current profile, token validity, authenticated user
- `worksome auth switch <profile>` — switches between profiles

**Precedence (highest to lowest):** CLI flag `--token` → env var `WORKSOME_API_TOKEN` → config file profile

**Security:**
- Config file created with `0600` permissions
- Token never logged or printed in full (masked in `auth status`)
- No token stored in shell history (interactive prompt, not CLI argument)

## GraphQL Client

**Core client (`internal/client/`):**

```go
type Client struct {
    endpoint   string
    token      string
    httpClient *http.Client
}

func (c *Client) Execute(ctx context.Context, query string, variables map[string]any, result any) error
```

**Responsibilities:**
- POST requests with `{"query": "...", "variables": {...}}` body
- `Authorization: Bearer <token>` header
- GraphQL `errors[]` surfaced as Go errors
- Pagination support (offset-based: `first`, `page`)
- Request/response logging via `--verbose`

**Pagination:**
- Default: first page, 10 items (API default)
- `--first 50` — control page size
- `--page 3` — specific page
- `--all` — iterates all pages, streams results

**Error handling:**
- Auth errors → message suggesting `worksome auth login`
- GraphQL validation errors → show error message and path
- Network errors → retry with backoff (max 3 attempts)
- Rate limiting → respect `Retry-After` header

## Codegen Pipeline

**Flow:** `schema.graphql` → Parser → Intermediate Representation → Templates → Go code

### Parser (`internal/codegen/parser.go`)

Uses `vektah/gqlparser/v2` to parse the vendored schema into an IR:
- **Resources**: grouped by domain via naming convention analysis
- **Types**: object types, input types, enums, interfaces, unions, scalars
- **Operations**: each query/mutation with arguments, return type, deprecation status

### Resource grouping heuristic

- Queries named `<resource>` (singular) and `<resources>` (plural) define a resource group
- Mutations matched by prefix: `create<Resource>`, `update<Resource>`, `delete<Resource>`, plus domain verbs (`terminate`, `cancel`, `approve`, etc.)
- Edge cases handled via `schema/overrides.yaml`:

```yaml
# Force operations into specific resource groups
resources:
  bank-details:
    queries: ["bankDetail"]
    mutations: ["storeBankDetails"]
  viewer:
    queries: ["viewer", "profile"]
    mutations: []
# Operations to exclude from CLI generation
ignore:
  - "internalDebugQuery"
```

### Templates (`internal/codegen/templates/`)

| Template | Output |
|---|---|
| `types.go.tmpl` | Go structs for objects/inputs, enums as string constants |
| `queries.go.tmpl` | Client functions: `GetHire()`, `ListHires()`, etc. |
| `commands.go.tmpl` | Cobra commands wired to client, flags from input fields |
| `tests.go.tmpl` | Table-driven tests per command with mock HTTP |

### Nullability

- Required fields (`String!`) → Go value type (`string`)
- Nullable fields (`String`) → Go pointer type (`*string`)
- Nullable input fields use `omitempty` JSON tags so unset fields are excluded from variables
- For output types, nil pointers render as empty/`null` in JSON and `—` in table output

### Union & interface types

- GraphQL interfaces → Go interfaces with a marker method `Is<InterfaceName>()`
- GraphQL unions → same pattern, with concrete types implementing the marker
- JSON deserialization uses `__typename` field to dispatch to the correct Go struct
- The codegen always requests `__typename` on interface/union fields

### Scalar mapping

| GraphQL | Go |
|---|---|
| String | string |
| Int | int |
| Float | float64 |
| Boolean | bool |
| ID | string |
| DateTime | time.Time |
| Date | time.Time |
| DecimalTwo | float64 |
| Percentage | float64 |
| Upload | N/A (file path flag) |
| JSON/Dictionary | map[string]any |

### Running codegen

```bash
make generate          # parse schema → generate all code
make sync-schema       # pull latest schema via introspection
make sync              # sync-schema + generate
```

### Schema sync mechanism

`make sync-schema` supports two modes:

1. **From introspection (default):** Runs a GraphQL introspection query against the live API endpoint using the configured token. Requires `WORKSOME_API_TOKEN` to be set.
2. **From a local schema dump:** Copies a schema file from a local checkout. Opt in with `SYNC_MODE=platform`; the source path is the `PLATFORM_SCHEMA` Makefile variable.

### Codegen error handling

- Unknown or unsupported schema constructs produce warnings on stderr but don't fail the build
- Template rendering errors are fatal and report the problematic type/operation name
- The `make verify-generated` target catches any drift between schema and committed code

## CLI Command Structure

**Root command:** `worksome`

**Global flags:**
- `--profile <name>` — config profile
- `--token <token>` — override token
- `--endpoint <url>` — override API endpoint
- `--output json|table` — force output format
- `--dry-run` — show the GraphQL query and variables without executing (mutations only)
- `--verbose` — request/response details
- `--no-color` — disable colored output

### Command pattern

```
worksome <resource> <action> [flags]
worksome hires list --status ACTIVE --first 20
worksome hires get <id>
worksome hires create --input hire.json
worksome hires terminate --id <id> --reason "Project ended"
worksome jobs list --all
worksome viewer
```

### Action mapping

| GraphQL pattern | CLI action | Example |
|---|---|---|
| `<resources>(...)` query | `list` | `worksome hires list` |
| `<resource>(id)` query | `get` | `worksome hires get <id>` |
| `create<Resource>` mutation | `create` | `worksome jobs create` |
| `update<Resource>` mutation | `update` | `worksome jobs update` |
| `delete<Resource>` mutation | `delete` | `worksome projects delete` |
| `<verb><Resource>` mutation | `<verb>` | `worksome hires terminate` |

### Input handling for mutations

- Simple inputs → individual flags: `--status ACTIVE --currency EUR`
- Complex inputs → JSON file: `--input create-job.json` or stdin pipe
- Hybrid: flags override fields from `--input` file

### Output formatting

- TTY auto-detection: table for terminal, JSON when piped
- Explicit override: `--output json|table`

### Autocompletion

- Cobra built-in shell completion: bash, zsh, fish, powershell
- `worksome completion <shell>` generates the script
- Enum flags get completion values from schema

## Testing Strategy

### 1. Codegen tests (`internal/codegen/`)

- Unit tests for parser: minimal schema → assert correct IR
- Unit tests for generator: IR → assert generated Go compiles and matches expected
- Snapshot tests: generate from full schema, compare against committed `internal/generated/` — drift = codegen broken or schema changed without regenerating

### 2. Command tests (`internal/generated/commands/`)

Generated alongside commands from `tests.go.tmpl`:
- Table-driven tests with mock HTTP server per command
- Verify: correct GraphQL query sent, flags → variables mapping, output formatting, error handling

```go
func TestHiresList(t *testing.T) {
    tests := []struct {
        name     string
        flags    []string
        response string
        wantOut  string
        wantErr  bool
    }{
        {name: "list active hires", flags: []string{"--status", "ACTIVE"}, ...},
        {name: "json output", flags: []string{"--output", "json"}, ...},
        {name: "auth error", response: `{"errors":[...]}`, wantErr: true},
    }
}
```

### 3. Integration tests (`test/integration/`)

- Gated behind `WORKSOME_INTEGRATION_TEST=1`
- Smoke tests: `auth status`, `viewer`, `hires list --first 1`
- CRUD cycles for safe resources (create project → get → update → delete)
- Hand-written, not generated

### CI pipeline

- `make test` — codegen + command tests (no network)
- `make test-integration` — integration tests (requires token)
- `make lint` — golangci-lint
- `make verify-generated` — re-run codegen, diff against committed, fail if stale

## Build & Distribution

**Cross-compilation via `goreleaser`:**
- `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`

**Installation methods:**
- Homebrew: `brew install worksome/tap/worksome-cli`
- Direct download: GitHub releases with checksums
- Go install: `go install github.com/worksome/worksome-cli/cmd/worksome@latest`

**Versioning:**
- SemVer, tagged releases
- New operations → minor bump
- Removed operations → major bump
- `worksome version` shows version, commit, schema date

**GitHub Actions CI:**
- On PR: `lint` → `test` → `verify-generated` → `build`
- On tag: `goreleaser` release + Homebrew formula update

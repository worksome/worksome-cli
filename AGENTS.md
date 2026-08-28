# Agent Instructions for worksome-cli

## Project Overview

This is a Go CLI that wraps the Worksome GraphQL API. Most code is **generated** from a vendored GraphQL schema via a codegen pipeline. Understanding what is generated vs hand-written is critical before making changes.

## Key Concept: Generated vs Hand-Written

### Generated (DO NOT edit directly)
- `internal/generated/commands/commands.go` — Cobra commands for all resources
- `internal/generated/commands/root.go` — RegisterAll function
- `internal/generated/queries/queries.go` — GraphQL query/mutation functions
- `internal/generated/types/enums.go` — Enum types
- `internal/generated/types/types.go` — Object types
- `internal/generated/types/inputs.go` — Input types

To change generated code, modify the **templates** in `internal/codegen/generator.go` or the **parser** in `internal/codegen/parser.go`, then run `make generate`.

### Hand-Written
- `cmd/worksome/main.go` — Root command, global flags, client factory
- `cmd/worksome/auth.go` — Auth login/status/switch commands
- `cmd/generate/main.go` — Codegen tool entrypoint
- `internal/client/` — GraphQL HTTP client, retry, pagination
- `internal/config/` — Profile management, token resolution
- `internal/output/` — JSON/table formatting, TTY detection
- `internal/codegen/` — Parser, generator, IR types (this produces the generated code)
- `schema/overrides.yaml` — Manual resource grouping overrides

## Codegen Pipeline

```
schema/schema.graphql + schema/overrides.yaml
  → internal/codegen/parser.go (schema → IR)
  → internal/codegen/ir.go (intermediate representation)
  → internal/codegen/generator.go (IR → Go code via templates)
  → internal/generated/*
```

Run: `make generate` or `go run ./cmd/generate/ -schema schema/schema.graphql -overrides schema/overrides.yaml -output internal/generated -module github.com/worksome/worksome-cli`

## Common Tasks

### Adding a new global flag
Edit `cmd/worksome/main.go` in `newRootCmd()`.

### Changing how commands are generated
1. Edit the template strings in `internal/codegen/generator.go` (e.g., `commandsTemplate`, `queriesTemplate`)
2. If you need new data in templates, add fields to IR types in `internal/codegen/ir.go` and populate them in `internal/codegen/parser.go`
3. Run `make generate`
4. Run `make test`

### Adding a new resource override
Edit `schema/overrides.yaml` to map operations to specific resource groups. Then `make generate`.

### Updating the API schema
```bash
make sync-schema    # Introspects $INTROSPECT_ENDPOINT using $WORKSOME_API_TOKEN
                    # (SYNC_MODE=platform copies from a local platform checkout instead)
make generate       # Regenerates all code
make test           # Verify everything compiles and passes
```

### Adding a new built-in command
Add it to `cmd/worksome/` and register in `newRootCmd()` in `main.go`.

## Architecture Rules

- **No circular dependencies** — packages depend downward only
- **Generated code is committed** — treat it as reviewable output, not build artifacts
- **Single source of truth** — the schema file drives everything; don't hardcode API knowledge elsewhere
- **Objects always use pointer types** — to avoid recursive type compilation errors in generated Go types
- **All mutations follow `input: SomeInput!` pattern** — the codegen relies on this for flag generation

## Testing

```bash
make test           # All unit tests (verbose)
make test-short     # Without verbose
go test ./internal/codegen/ -run TestParseSchema -v   # Specific test
```

Tests are in:
- `internal/codegen/parser_test.go` — Schema parsing, resource grouping, type resolution
- `internal/codegen/generator_test.go` — Code generation output verification
- `internal/client/client_test.go` — HTTP client, retries, pagination
- `internal/config/config_test.go` — Config load/save, token precedence
- `internal/output/output_test.go` — JSON/table formatting, TTY detection

## Style

- Go standard formatting (`gofmt`)
- Cobra for CLI commands
- No external test frameworks — standard `testing` package
- Error wrapping with `fmt.Errorf("context: %w", err)`
- Template functions in `generator.go` use `template.FuncMap`
- CLI flags use kebab-case (`--order-by`), GraphQL uses camelCase (`orderBy`)

# Worksome CLI — Architecture

## Overview

```
┌──────────────────────────────────────────────────────────────┐
│                        worksome CLI                          │
│                                                              │
│  cmd/worksome/main.go          cmd/generate/main.go          │
│  ┌──────────────────┐          ┌──────────────────┐          │
│  │   Root Command    │          │   Codegen Tool    │          │
│  │   + Auth Commands │          │                  │          │
│  │   + Version       │          │                  │          │
│  │   + Completion    │          │                  │          │
│  └────────┬─────────┘          └────────┬─────────┘          │
│           │                             │                    │
│  ┌────────▼─────────┐          ┌────────▼─────────┐          │
│  │ Generated Commands│◄─────────│  Codegen Engine   │          │
│  │ (64 resources)    │  output  │  Parser + Gen     │          │
│  └────────┬─────────┘          └────────┬─────────┘          │
│           │                             │                    │
│  ┌────────▼─────────┐          ┌────────▼─────────┐          │
│  │  GraphQL Client   │          │  schema.graphql   │          │
│  │  + Pagination     │          │  + overrides.yaml │          │
│  └────────┬─────────┘          └──────────────────┘          │
│           │                                                  │
│  ┌────────▼─────────┐  ┌──────────────────┐                 │
│  │   Config/Auth     │  │  Output Formatter │                 │
│  │   (~/.worksome/)  │  │  (JSON/Table/TTY) │                 │
│  └──────────────────┘  └──────────────────┘                 │
└──────────────────────────────────────────────────────────────┘
                          │
                          ▼
               Worksome GraphQL API
          https://api.worksome.com/graphql
```

## Package Dependency Graph

```
cmd/worksome
├── internal/config           (auth, profiles)
├── internal/client           (GraphQL HTTP client)
├── internal/output           (JSON/table formatting)
└── internal/generated
    ├── commands              (Cobra commands per resource)
    ├── queries               (GraphQL query/mutation functions)
    └── types                 (Go structs, enums, inputs)

cmd/generate
└── internal/codegen
    ├── ir.go                 (intermediate representation types)
    ├── parser.go             (schema → IR)
    └── generator.go          (IR → Go code via templates)
```

## Code Generation Pipeline

The codegen pipeline is the architectural centerpiece. It enables full API coverage with minimal maintenance.

### Flow

```
schema/schema.graphql     schema/overrides.yaml
        │                         │
        ▼                         ▼
  ┌──────────────────────────────────┐
  │           Parser                  │
  │  (vektah/gqlparser/v2)           │
  │                                  │
  │  1. Parse GraphQL schema         │
  │  2. Identify enums, types,       │
  │     inputs, paginators           │
  │  3. Group operations into        │
  │     resource groups              │
  │  4. Apply overrides              │
  └──────────────┬───────────────────┘
                 │
                 ▼
  ┌──────────────────────────────────┐
  │      Intermediate Representation  │
  │                                  │
  │  Schema {                        │
  │    Resources []Resource          │
  │    Enums     []Enum              │
  │    Objects   []Object            │
  │    Inputs    []InputObject       │
  │    ...                           │
  │  }                               │
  └──────────────┬───────────────────┘
                 │
                 ▼
  ┌──────────────────────────────────┐
  │          Generator                │
  │  (text/template → go/format)     │
  │                                  │
  │  types/enums.go    (3,622 lines) │
  │  types/types.go    (2,587 lines) │
  │  types/inputs.go   (1,692 lines) │
  │  queries/queries.go(3,091 lines) │
  │  commands/commands.go(8,285 lines│
  │  commands/root.go  (88 lines)    │
  │                                  │
  │  Total: ~19,365 lines generated  │
  └──────────────────────────────────┘
```

### Resource Grouping

The parser automatically groups GraphQL operations into CLI resource groups:

| Pattern | Example | CLI Command |
|---------|---------|-------------|
| `<resources>(first, page, ...)` | `hires(status: ...)` → `HirePaginator` | `worksome hires list` |
| `<resource>(id: ID!)` | `hire(id: ID!) → Hire` | `worksome hires get <id>` |
| `create<Resource>(input: ...)` | `createJob(input: CreateJobInput!)` | `worksome jobs create` |
| `update<Resource>(input: ...)` | `updateJob(input: UpdateJobInput!)` | `worksome jobs update` |
| `delete<Resource>(input: ...)` | `deleteProject(input: ...)` | `worksome projects delete` |
| `<verb><Resource>(input: ...)` | `terminateHire(input: ...)` | `worksome hires terminate` |

Operations that don't follow conventions are mapped via `schema/overrides.yaml`.

### Type Mapping

| GraphQL | Go | Notes |
|---------|-----|-------|
| `String!` | `string` | Required → value type |
| `String` | `*string` | Nullable → pointer |
| `[Hire!]!` | `[]Hire` | Required list → slice |
| `Hire!` (object) | `*Hire` | Objects always pointer (avoids recursive types) |
| Enums | `type X string` | String constants with `Valid<Enum>()` helper |
| Inputs | `struct` | With `json:"...,omitempty"` for nullable fields |
| DateTime/Date/Time | `string` | ISO format strings |
| JSON/Dictionary | `map[string]any` | Arbitrary JSON |

## Authentication

```
Token Resolution (highest priority first):
  1. --token flag
  2. WORKSOME_API_TOKEN env var
  3. ~/.worksome/config.yaml → profiles[current_profile].token
```

Config file is written with `0600` permissions. Tokens are never printed in full (`MaskToken` shows only last 4 chars).

## Output Formatting

```
                    ┌──────────┐
     stdout is TTY? │ Detector │
                    └────┬─────┘
                         │
                   ┌─────┴─────┐
                   │           │
                   ▼           ▼
              ┌────────┐  ┌────────┐
              │ Table   │  │ JSON   │
              │ (human) │  │ (pipe) │
              └────────┘  └────────┘
                         │
                --output flag overrides auto-detection
```

## Schema Sync

```bash
make sync-schema    # Introspect the API (no credentials needed)
make generate       # Parse schema → generate all Go code
make sync           # Both in one step
make verify-generated  # CI check: fail if generated code is stale
```

## Testing Strategy

| Layer | Tests | Coverage |
|-------|-------|----------|
| `internal/codegen/` | Parser IR, generator output, full-schema snapshot | Schema parsing, code generation |
| `internal/client/` | Mock HTTP server, retries, pagination, auth errors | Network layer |
| `internal/config/` | Load/save round-trip, token precedence, permissions | Configuration |
| `internal/output/` | JSON/table formatting, TTY detection, field extraction | Output |
| `test/integration/` | (future) Live API smoke tests | End-to-end |

## Key Design Decisions

1. **Codegen over manual commands**: With ~200 operations, manual CLI code would be unmaintainable. Codegen from schema ensures 1:1 API coverage and simple updates.

2. **Committed generated code**: Generated files are checked into git, not built at CI time. This makes the output reviewable and the build reproducible without needing the codegen tool.

3. **Separate codegen binary**: `cmd/generate/` is a standalone tool so its dependencies (`gqlparser`) don't leak into the main CLI binary.

4. **Objects always as pointers**: All object-to-object references use Go pointers to avoid recursive type compilation errors (e.g., Milestone ↔ MilestoneDetail).

5. **Dynamic queries with `map[string]any`**: Instead of typed query responses, the generated query functions return `map[string]any`. This keeps the codegen simple and flexible — the CLI just forwards JSON to the output formatter.

6. **Overrides file**: `schema/overrides.yaml` handles the ~5% of operations that don't follow naming conventions, without complicating the parser's heuristics.

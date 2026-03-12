# Self-Evaluation: worksome-cli v0.1.0

## What's Working

### Strengths
- **Full API coverage**: 80 resource groups generated from the GraphQL schema covering all 197+ operations
- **Codegen pipeline is solid**: Parser correctly handles enums, objects, inputs, paginators, unions, interfaces
- **Clean architecture**: Separate packages for client, config, output, codegen, with no circular deps
- **Schema sync workflow**: `make sync-schema && make generate` provides a clear update path
- **Test coverage**: 38 tests across 4 packages covering critical paths
- **CLI UX**: Auto-detected output format, shell completion, profiles, dry-run support
- **Security**: Config file with 0600 permissions, masked token display, no token in shell history

### Numbers
- ~19,365 lines of generated code
- ~480 lines of codegen engine (parser + generator)
- ~160 lines of config management
- ~200 lines of GraphQL client
- ~180 lines of output formatting
- ~580 lines of tests
- 38 passing tests
- 0 compiler warnings

## What Needs Improvement

### High Priority

1. **Generated queries use `{ id }` as default selection set** — This means `worksome hires get <id>` returns only the `id` field. The codegen should generate deeper selection sets by analyzing the return type's fields and including all scalar/enum fields. This is the #1 usability issue.

2. **No `--all` pagination support** — The spec called for `--all` to auto-paginate through all pages. The client has `ExecuteAll` but it's not wired into generated commands yet.

3. **Mutation input only via `--input` file** — Complex mutations require a JSON file. There's no inline flag-to-input mapping for simple mutations. The codegen should introspect input types and generate flags for scalar fields.

4. **Some resource grouping is imperfect** — Mutations like `attributeRecruiterToHire` and `removeRecruiterFromHire` should be under `hires` but end up as separate resources (`recruiter-to-hire`, `recruiter-from-hire`). The grouping heuristic needs refinement for multi-word suffixes.

### Medium Priority

5. **No table output for list commands** — Currently everything renders as JSON. The table formatter is built but not wired — commands need `Column` definitions per resource, which the codegen could generate from the type's scalar fields.

6. **Missing `get` subcommand on some resources** — Resources like `hire` that only have the singular query (not part of a plural group due to naming) may lack the `get` action. The parser's singular detection needs more test cases.

7. **No request timeout configuration** — The HTTP client uses Go's default (no timeout). Should add a 30s default with `--timeout` flag.

8. **GraphQL query strings have minimal selections** — List queries only select `{ id }` in the data field. They should select all scalar fields of the return type for usefulness.

### Low Priority

9. **No CI/CD pipeline yet** — The Makefile targets are ready but no `.github/workflows/` file exists.

10. **No `goreleaser` configuration** — Cross-compilation setup is pending.

11. **Integration tests not implemented** — The `test/integration/` directory is empty.

12. **Upload scalar not handled** — File upload mutations won't work as the client only supports JSON POST, not multipart.

## Architecture Assessment

| Aspect | Rating | Notes |
|--------|--------|-------|
| **Modularity** | Good | Clean package boundaries, no circular deps |
| **Extensibility** | Good | Overrides file handles edge cases, templates are customizable |
| **Maintainability** | Good | Single source of truth (schema), clear codegen pipeline |
| **Security** | Good | Token handling, file permissions, no secrets in output |
| **Test coverage** | Fair | Core logic tested, but generated commands untested |
| **UX** | Fair | Help text is great, but default query results are too minimal |
| **Performance** | Good | Compilation is fast, binary is self-contained |

## Recommended Next Steps

1. **Fix selection sets** — Generate full scalar field selections for get and list queries
2. **Wire `--all` pagination** into list commands
3. **Generate flags for simple mutation inputs**
4. **Add table column definitions** per resource for table output
5. **Add GitHub Actions CI** pipeline
6. **Improve resource grouping** for multi-word operations

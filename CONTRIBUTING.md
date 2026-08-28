# Contributing

Thanks for taking the time. The one thing worth reading before anything else:

> **Most of this codebase is generated.** If you edit a file under
> `internal/generated/` your change will be silently overwritten, and CI will
> fail because the committed output no longer matches the schema.

## What is generated, and what to edit instead

Everything in `internal/generated/` is produced from `schema/schema.graphql`
by `cmd/generate`. To change it, change what produces it:

| To change | Edit |
|---|---|
| A command's flags, help text, or behaviour | the templates in `internal/codegen/generator.go` |
| How operations are grouped into resources | `schema/overrides.yaml` |
| How the schema is interpreted (types, scalars, paginators) | `internal/codegen/parser.go` |
| The API surface itself | nothing here — the schema comes from the API |

Then run `make generate` and commit the regenerated output alongside your
change. CI regenerates and diffs, so the two must be in step.

## Getting set up

```bash
make build      # binary with version metadata
make test       # unit tests
make lint       # golangci-lint
make generate   # regenerate from the schema
```

Go 1.26+ (see `go.mod`). No other tooling is required.

## Updating the schema

```bash
export WORKSOME_API_TOKEN=...   # a Personal Access Token
make sync-schema                # introspects the API
make generate
make test
```

`make sync-schema` reads the token from the environment only — deliberately,
so it never lands in your shell history or the process list. If the API has
introduced a scalar we don't know about, generation fails and tells you which:

```
schema declares scalar(s) with no Go mapping: SomeNewScalar
add them to scalarMap in internal/codegen/parser.go
```

That is the expected way to find out, not a bug.

If the API renames or regroups something in a way that would break an existing
command, add an entry to `aliases` in `schema/overrides.yaml` rather than
accepting the break.

## Tests

Every change to hand-written code needs a test. The bar we try to hold: revert
your fix and confirm the test actually fails. A test that passes either way is
worse than none, because it looks like coverage.

Standard library `testing` only — no test frameworks.

## Pull requests

- Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`)
- Explain *why* in the commit body; the diff already shows what
- Keep generated output in the same commit as the change that produces it
- CI must be green: build, tests, lint, and the generated-code check

## Security

Don't open an issue for security problems — see [SECURITY.md](SECURITY.md).

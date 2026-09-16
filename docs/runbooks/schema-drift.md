# Runbook — Schema drift

The [Schema drift](../../.github/workflows/schema-drift.yml) job runs nightly. It
re-introspects the production API, regenerates `internal/generated/`, runs the
tests, and opens or updates `chore/schema-sync`. A failure means the API moved in
a way the generator cannot absorb on its own — nothing is broken for users, but
the CLI stops tracking the API until someone resolves it.

Reproduce any of the below locally with:

```
make sync   # introspects the live API, then regenerates
go test ./... -race
```

## Colliding command names

```
colliding command names:
  "endJob" and "endJobs" both generate "jobs" "end"; set command_names in the overrides file
```

Two operations derived the same command. The generator refuses rather than emit
two Go identifiers with the same name — that used to surface as an unrelated
build failure in the test step.

Decide which operation keeps the established name (the one already shipped,
always) and name the newcomer in `command_names` in `schema/overrides.yaml`:

```yaml
command_names:
  endJobs: "end-many"  # `endJob` already owns `jobs end`
```

Then `make generate`. Renaming the established command instead is a breaking
change for anyone scripting against it.

## A scalar has no Go mapping

```
schema declares scalar(s) with no Go mapping: Weekday
add them to scalarMap in internal/codegen/parser.go
```

The API added a custom scalar. Map it to the Go type it serialises as in
`scalarMap`, then `make generate`.

## A stale overrides entry

```
invalid aliases in overrides: alias target "companies" is not a generated resource
command_names entry "endJobs" matches no generated operation
```

The operation or resource the entry names is gone from the API. Delete the
entry — but first check whether its disappearance removes a command people use,
in which case the answer may be an `aliases` entry pointing at whatever replaced
it.

## A command disappeared from the diff

Not an error — the job succeeds and the drift PR quietly drops a command. Review
the generated diff before merging. If the API renamed or regrouped something,
add an `aliases` entry in `schema/overrides.yaml` so the old invocation keeps
working rather than accepting the break.

## A field nulls its whole parent

Introspection does not return applied directives, so the vendored schema carries
none of the API's field-level authorization. A guarded non-null field takes the
parent object down with it when the guard denies, and the command returns null
for everyone outside the guard. Add the `Type.field` to `ignore_fields` in
`schema/overrides.yaml`.

## Introspection itself failed

```
Error fetching schema: ...
```

The API is down, or the endpoint moved. Check the API before touching anything
here; `INTROSPECT_ENDPOINT` in the `Makefile` is the endpoint in use.

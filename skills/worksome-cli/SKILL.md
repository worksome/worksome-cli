---
name: worksome-cli
description: >
  This skill should be used when the user asks anything that needs live data
  from the Worksome platform, or names the CLI directly — "worksome cli",
  "connect to worksome", "query the worksome API". Trigger phrases include
  asking about "hires", "our hires", "timesheets", "payment requests",
  "invoices", "jobs we've posted", "bids", "contracts", "IR35" or "worker
  classifications", "compliance requirements", "projects", "approval rules",
  "spend", or "how many workers do we have". Covers installing the
  worksome-cli binary, authenticating it with a Personal Access Token, and
  driving the GraphQL API without wasting tokens or requests.
metadata:
  version: "0.4.0"
---

# worksome-cli

Drive the Worksome GraphQL API through the `worksome` CLI: every operation in
the API, generated from a vendored schema so help text cannot drift from it.
The README carries the exact counts.

## Bootstrap

Run these two checks before the first Worksome command in a session. Both are
cheap and idempotent — skip them once they have passed.

### 1. Is the binary present?

```bash
command -v worksome >/dev/null || curl -fsSL https://raw.githubusercontent.com/worksome/worksome-cli/main/scripts/install.sh | bash
```

The script prints `installed worksome <version> at <path>` or `already installed at <path>`. It prefers
the prebuilt release tarball (seconds), falls back to `go install` (minutes, and
only if Go is present). It installs to the first writable directory among
`/usr/local/bin`, `~/.local/bin`, `~/bin`.

How often this runs depends on where the session lives, and the two cases behave
very differently:

- **On the user's own machine** (Claude Code locally, or a persistent desktop
  workspace): once, ever. The binary stays installed.
- **In an ephemeral cloud container**: once per session, because the container is
  reclaimed afterwards.

On macOS the installer may land the binary in `~/.local/bin`, which is not on
`PATH` by default. If a later call reports `worksome: command not found`, use the
absolute path or tell the user to add `export PATH="$HOME/.local/bin:$PATH"` to
their shell profile.

### 2. Is it authenticated?

```bash
worksome auth status
```

Token resolution order, and how to satisfy it:

1. **`WORKSOME_API_TOKEN` in the environment** — the intended path. If it is set,
   the CLI uses it with no config file and no further setup. Confirm with
   `worksome viewer get`.
2. **`~/.worksome/config.yaml`** — written by a previous `worksome auth login`,
   at mode 0600. On the user's own machine this file is **permanent**: one
   `auth login` and every future session on that machine is authenticated, with
   no environment variable needed. In an ephemeral cloud container it lives only
   as long as the container.
3. **Neither** — ask the user for a token. Point them at
   <https://use.worksome.com/integrations/api-tokens>; tokens are valid six
   months and the value is shown exactly once, at creation.

**Before asking for a new token, check whether one already exists.** A Worksome
PAT is a plain bearer credential — `Authorization: Bearer <token>` — with no
device binding of any kind, so **one token works on any number of machines and
sessions simultaneously**. If the user has authenticated the CLI anywhere else,
the right move is to reuse that token here, not to issue another. Never tell a
user they need a token per machine.

Prefer keeping the token out of the conversation. In order:

```bash
# Best: it is already exported, or the user points at a file on disk
export WORKSOME_API_TOKEN="$(cat /path/to/token-file)"

# Acceptable: they pasted it — persist it, because each Bash call is a fresh
# shell and environment variables do not carry over between calls
worksome auth login --token "<pasted>"
```

**On the user's own machine, recommend the one-time fix rather than repeating
this every session.** Either line below, run once in their terminal, ends the
problem permanently — and running it themselves keeps the token out of the
transcript entirely:

```bash
worksome auth login --token "<token>"              # writes ~/.worksome/config.yaml, 0600
echo 'export WORKSOME_API_TOKEN="<token>"' >> ~/.zshrc   # or the env-var route
```

Do not ask a user to re-paste a token into a second conversation when they can
run one command instead.

Do **not** try to read a token out of a browser page — the Chrome extension
strips JWTs from anything a page returns, by design. Ask the user to move it
across instead.

### Preflight, and what it cannot tell you

`worksome viewer get` is the cheap liveness-and-identity check: it returns the
authenticated user's id, name and email. It does **not** report the account or
the permissions the token carries — the API exposes no role or permission
fields. So it cannot predict whether a given call will be allowed. Discover
permissions by attempting the action: **a permission error is a correct answer
about the token's role, not a bug to work around.** Many operations are
restricted to one party (company, worker, or recruiter) and the help text says
so, e.g. "Only companies can cancel their hires."

## Driving the CLI

Commands follow `worksome <resource> <action> [flags]`.

**Discovery is built in — use it instead of guessing.** `worksome --help` lists
all 82 resource groups; `worksome <resource> --help` lists that resource's
actions and its real flags. Flags differ per resource: `hires list` accepts
`--first`, while `accounts get` is not paginated and rejects it. Check `--help`
before inventing a flag.

**Keep responses small.** This matters more than anything else here — a bare
`list` returns full objects and burns context for no benefit.

```bash
worksome hires list --first 20 --fields id,status,worker.name --filter status=ACTIVE
```

- `--fields` narrows the GraphQL **selection set** — the server is only asked
  for those fields. A field name that does not exist is an error, not an empty
  result (except on a few operations whose shape cannot be narrowed, where it is
  dropped silently).
- `--filter key=value[,key=value]` narrows results server-side.
- `--columns` only affects table rendering; it does not reduce what is fetched.

**Output is JSON when stdout is not a TTY**, which is always the case from a
tool call — so piping to `jq` works with no flag. Pass `--output json`
explicitly anyway when the parse must be deterministic.

**Pagination is explicit.** A `list` returns one page. `--first N` / `--page N`
to walk it. `--all` auto-paginates, which on a large resource means many
sequential requests — reach for it only when the whole set is genuinely needed,
and say so before running it.

**Branch on exit codes, not stderr text.** `0` on success, non-zero for any
failure (auth, validation, GraphQL, network). Message text is not a stable
interface.

**Partial responses still exit 0.** GraphQL can fail individual fields while
resolving the rest. When that happens the data goes to stdout, the exit code
stays `0`, and the failed fields are named on stderr with their response path
(`hires.data[0].triggersApproval: ...`). A failed field appears in the data as
`null` — so stderr is the *only* way to distinguish it from a legitimately null
value. Capture stderr separately when that distinction matters.

**IDs are opaque base64.** `SGlyZToxMjM=` decodes to `Hire:123`,
`VXNlcjo0NTY=` to `User:456`. Decode one to sanity-check that an ID refers to
the type you think it does before passing it to a mutation:

```bash
echo -n 'SGlyZToxMjM=' | base64 -d    # -> Hire:123
```

**Silence the update check** in long or scripted runs: `WORKSOME_NO_UPDATE_CHECK=1`.
It only fires when stdout *and* stderr are both terminals, so tool calls never
see it anyway.

**Profiles and endpoints.** Default endpoint is
`https://api.worksome.com/graphql` — production. `--profile <name>` selects a
saved profile; `--endpoint <url>` overrides for one call. If the user mentions
staging or a sandbox, ask which endpoint rather than assuming.

## Traps: fields that look right and are not

Each of these produced a confidently wrong answer for an agent before it was
written down. Read them before answering a question about money, approvals,
classification or "how many".

**`status: ACTIVE` does not mean anyone is working.** A hire stays ACTIVE until
someone ends it, and nobody does. On one account 17 of 26 ACTIVE hires had no
payment in over a year; one had never invoiced in seven. For "who is working
for us", read `lastPaymentRequestDate` on each hire and set your own window
(90 days is a reasonable "live"). There is no server-side filter for it yet.

**Two ledgers, not one.** A *payment request* is what the worker is paid:
`billedAmount`, `billedAmountWithExpenses`, settled on `paidAt`, state in
`workerPayoutStatus`. An *invoice* is what the company pays Worksome:
`grossAmount` = `netAmount` + `taxAmount`, outstanding in `grossOpenAmount`,
settled on `markedPaidAt`. "What did we pay contractors" is payment requests.
"What do we owe" is invoices. Do not add them together.

**`isOverDue` on an invoice is computed from `dueDate` alone.** It is `true` on
invoices that were paid early. For "what is actually overdue" use
`grossOpenAmount > 0` and `dueDate` before today.

**Never sum across currencies.** Payment requests and invoices carry a
`currency`; a multi-entity account has several. The API has no exchange rate.
Group by currency and say so, or ask which rate to use.

**Bucket cash by `paidAt`, not `date`.** `date` is when the request was
raised and `startDate`/`endDate` the period billed. Cash out the door is
`paidAt`, which is null until settled.

**Approval status is `hasPendingApproval`.** `triggersApproval` and
`currentApprovalState` take a `trigger` argument the CLI cannot pass and are
not selectable. `hasPendingApproval` is a plain Boolean on every hire. To find
all hires awaiting approval, list hires with `--fields id,hasPendingApproval`
and filter client-side; there is no server filter.

**`--statuses UNAPPROVED` returns nothing.** The field and the filter disagree
on the server: records report `status: UNAPPROVED` (drafts included) but the
filter excludes drafts. `APPROVED`, `REJECTED` and `CANCELLED` filter fine.
For unapproved ones, pull and filter on the `status` field yourself.

**Classification is per hire.** `classifications list --hire <hire-id>` is the
only way to read a result: it returns `type`, `status`, `acceptedStatus` and
`result { label title }` (e.g. "W-2 (Employee)"). The `classification` object
nested on a hire exposes only `id, type, status, title, description` —
`classification.result` is an unknown field there. To find hires that use
classification at all, `hires list --uses-classification USING` narrows
server-side; then read each one.

**Discover a type's fields with a bogus `--fields` value.** `--fields nope`
fails before any request and the error lists what is available:

```bash
worksome invoices list -n 1 --fields nope
# Error: invoices: --fields: unknown field "nope" (available: id, number, pdfUrl,
#   currency, grossAmount, grossOpenAmount, taxAmount, netAmount, markedPaidAt, ... and 11 more)
```

It is the cheapest way to learn a shape: no tokens spent on a full object.
The list caps at twelve names; the same error on a nested path
(`--fields worker.nope`) lists that level. Filters are in `--help`.

**A mutation that errors may still have happened.** `hires terminate` has
returned "Internal server error" after persisting the termination. After any
non-zero exit on a mutation, re-read the record before retrying; a retry can
double-apply.

## Mutations

This plugin runs mutations without a confirmation gate — that is the configured
behaviour, and no operation is blocked.

**Default posture is read-only.** `get` and `list` are always fine. Everything
else changes state and runs only when the user has asked for that action on
that record, in this conversation: `create`, `create-draft`, `update`,
`delete`, `remove`, `end`, `terminate`, `cancel`, `reject`, `approve`, `mark`,
`invite`, `share`, `onboard`, `duplicate`, `run`, `retry`, `store`, `upload`,
`change`, `manage`, `generate`, and any action under `payment-requests`,
`invoices`, `batches` or `approvals`. "Clean up the dormant hires" is not
authority to terminate 17 contracts; it is a request for the list. (`auth
login`, `logout` and `switch` only touch local config.)

Competence still applies. Before any operation that changes or destroys state —
`terminate`, `cancel`, `reject`, `delete`, `end`, `update`, `create`, anything
under `payment-requests`, `invoices` or `approvals` — do two things:

1. **Resolve the target and name it in plain language.** IDs are opaque base64,
   so a typo is indistinguishable from a valid ID. Fetch the object first and
   state what is about to change: "terminating hire `SGlyZToxMjM=` — Jane Doe,
   ACME contract, status READY". This is not a gate; it is knowing what you are
   doing.
2. **Preview the shape with `--dry-run`** when the operation is unfamiliar or
   takes an `--input` file. It prints the operation name and variables and makes
   no API call:

```bash
worksome hires terminate --input terminate.json --dry-run
```

Mutations take scalar fields as flags, or a JSON document via `--input file`
(`-` for stdin). Flags override values from the file:

```bash
worksome hires terminate --input base.json --reason PROJECT_COMPLETED_EARLY
```

If a mutation is destructive, irreversible, and the user's instruction is
ambiguous about which record they mean, ask which one — that is a question about
their intent, not a permission gate.

## References

- `references/resources.md` — the hiring-lifecycle domain model and a catalogue
  of resource groups with their real actions. Read this when choosing which
  resource answers a question, or when a resource name is unfamiliar.
- `references/recipes.md` — worked command patterns for the questions that come
  up most: active hires, unapproved timesheets, invoice totals, classification
  status, approval bottlenecks.

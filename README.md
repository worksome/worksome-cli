# worksome-cli

A multiplatform CLI for the [Worksome GraphQL API](https://docs.worksome.com/). Full API coverage via code generation from a vendored schema — 64 resource groups, 174 operations. Designed for both human users and AI agents.

## What is Worksome?

[Worksome](https://www.worksome.com) is a Freelance Management System (FMS). Companies use it to source, contract, onboard, and pay external workers — freelancers, independent contractors, and agency workers — while staying compliant with local employment and tax law. The classification side matters as much as the payment side: getting a contractor's status wrong (IR35 in the UK, worker classification in the US) is a legal and financial risk, so the platform tracks it as first-class data.

This CLI wraps the same GraphQL API that powers the product, so anything the platform does to a job, a hire, a timesheet, or an invoice can be scripted.

## Domain model

The API is organised around a hiring lifecycle. Quoted descriptions below are taken from the GraphQL schema itself:

```
company ──creates──▶ job ──receives──▶ bid
                      │                 │
              (optionally inside     accept-bid
                  a project)            │
                                        ▼
                                      hire ──┬──▶ contracts       (the legal documents)
                                        │     ├──▶ compliance      (requirements to clear)
                                        │     └──▶ classifications (IR35, SDS/WCR)
                                        ▼
                                    timesheet
                                        ▼
                               payment-request ──approved──▶ invoice
                                        │                  (company pays Worksome)
                                        └──grouped into──▶ batch  (bulk operations)
```

| Resource | What it is |
|---|---|
| `organisation`, `company` | The hiring side. An organisation can contain several companies. |
| `worker` | The person being hired. |
| `jobs` | "A job that a company has created on Worksome." |
| `projects` | "A project with a budget containing one or more jobs." |
| `bids` | "Represents a bid sent on a job." Accepting one hires the worker. |
| `hires` | "A representation of a hiring on a job between parties" — links a worker, a company and a job. Explicitly *not* a legal document. |
| `contracts` | The actual legal document, reached from a hire. |
| `compliance`, `gate` | "A compliance requirement that must be met for business processes to proceed." |
| `classifications` | "A worker classification (SDS/WCR) for a hire" — IR35, US Worker Classification, and similar. |
| `timesheets` | Work logged against a hire. |
| `payment-requests` | A worker's request to be paid; carries a hire and a timesheet. |
| `invoices` | "An invoice gets created when one or more worker payment requests gets approved by a company user. The invoice defines the amount due and is to be paid to Worksome by the company." |
| `batches` | Groups items for bulk actions. Mainly for internal operational workflows — "For most external integrations, batches are not usually needed – you'll likely want to work directly with the individual items instead." |
| `approvals`, `workflows` | Approval rules, states and variables that gate actions. |
| `viewer` | The currently authenticated user — the cheapest way to check who a token belongs to. |

## Notes for AI agents

The CLI is designed to be driven programmatically:

- **Discovery is built in.** `worksome --help` lists every resource group; `worksome <resource> --help` lists that resource's actions. All help text is generated from the GraphQL schema, so it cannot drift from the API.
- **Permissions are documented per action.** Many operations are restricted to one party — company, worker, or recruiter — and the help text says so (e.g. "Only companies can cancel their hires"). A permission error is usually a correct answer about the token's role, not a bug to work around.
- **JSON without asking.** Output is JSON whenever stdout is not a TTY, so pipelines need no `--output json`. Pass it explicitly if you can't be sure of the environment.
- **Keep responses small.** `--fields id,worker.name` selects output fields and `--filter status=ACTIVE` narrows results — useful when a full page of objects would be more than you need.
- **Preview mutations.** `--dry-run` prints the operation name and variables without calling the API. Use it to confirm a mutation's shape before executing it.
- **Pagination is explicit.** List commands return a single page. `--all` auto-paginates, which on large resources means many sequential requests — prefer `--first`/`--page` unless you genuinely need everything.
- **Exit codes over stderr parsing.** `0` on success, non-zero for any failure (auth, validation, GraphQL, network). Branch on the exit code rather than matching on message text, which is not a stable interface.
- **`viewer get` is a cheap preflight** to confirm a token works and see which account and permissions it carries before attempting real work.

## Install

```bash
# From source (includes version and commit metadata)
make install

# Or build a local binary
make build

# Plain go install (no version metadata)
go install ./cmd/worksome/
```

Pushing a `v*` tag builds and attaches binaries for macOS, Linux, and Windows to the [GitHub release](https://github.com/worksome/worksome-cli/releases). A Homebrew cask (`brew install worksome/tap/worksome`) is prepared and will be published automatically once the repository is public.

## Quick Start

```bash
# Authenticate with a Personal Access Token
worksome auth login

# List hires (first page)
worksome hires list

# Get a specific hire
worksome hires get <id>

# List all hires (auto-paginate)
worksome hires list --all

# Create a job using flags
worksome jobs create --company <id> --name "Backend Engineer"

# Create a job from a JSON file
worksome jobs create --input job.json

# Pipe to jq (auto-detects non-TTY, outputs JSON)
worksome hires list --status ACTIVE | jq '.[].id'
```

## Authentication

The CLI uses Personal Access Tokens. Token resolution order:

1. `--token` flag
2. `WORKSOME_API_TOKEN` environment variable
3. Config file (`~/.worksome/config.yaml`)

```bash
worksome auth login           # Interactive setup
worksome auth status          # Show current auth state
worksome auth list            # List configured profiles
worksome auth switch <name>   # Switch profile
worksome auth logout [name]   # Remove a profile and its credentials
```

### Profiles

Multiple profiles are supported for different accounts or environments:

```bash
worksome auth login                    # Default profile
worksome --profile staging auth login  # Named profile
worksome --profile staging hires list  # Use a specific profile
```

## Usage

Commands follow a resource-action pattern:

```
worksome <resource> <action> [flags]
```

### Queries

```bash
worksome hires get <id>           # Get by ID
worksome hires list               # List (paginated)
worksome hires list --first 50    # Custom page size
worksome hires list --page 3      # Specific page
worksome hires list --all         # Fetch all pages
worksome hires list --status ACTIVE --search "john"
worksome hires list --watch          # Poll and refresh periodically
```

### Mutations

Mutations accept input via CLI flags (for scalar fields) or a JSON file:

```bash
# Using flags
worksome jobs create --company <id> --name "My Job"

# Using a JSON file (use `-` for stdin)
worksome hires terminate --input terminate.json

# Mix both (flags override file values)
worksome hires terminate --input base.json --reason "PROJECT_COMPLETED_EARLY"

# Dry run (prints the operation name and variables, makes no API call)
worksome jobs create --company <id> --name "Test" --dry-run
```

### Output

Output format is auto-detected:
- **TTY** (terminal): formatted for humans
- **Piped/redirected**: JSON

Override with `--output`:

```bash
worksome hires list --output json     # Force JSON
worksome hires list --output table    # Force table
worksome hires list --columns id,status,currency  # Select table columns
worksome hires list --fields id,worker.name       # Select output fields
worksome hires list --filter "status=ACTIVE"      # Shorthand for filter flags
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--token` | API token (overrides config) |
| `--endpoint` | Custom API endpoint |
| `--profile` | Config profile name |
| `--output` | Output format: `json`, `table` |
| `--verbose` | Show request/response details |
| `--columns` | Comma-separated list of columns for table output |
| `--fields` | Comma-separated list of fields to include in output |
| `--filter` | Key=value filter pairs (e.g., `status=ACTIVE,currency=DKK`) |
| `--no-color` | Disable colored output |
| `--dry-run` | Print the operation name and variables without calling the API |
| `--timeout` | Request timeout in seconds (default 30) |

## Shell Completion

```bash
# Bash
source <(worksome completion bash)

# Zsh
source <(worksome completion zsh)

# Fish
worksome completion fish | source

# PowerShell
worksome completion powershell | Out-String | Invoke-Expression
```

## Schema Sync & Code Generation

The CLI is generated from a vendored GraphQL schema. When the API changes:

```bash
make sync-schema    # Introspect the API (needs WORKSOME_API_TOKEN)
make generate       # Regenerate Go code
make sync           # Both in one step
```

### Verify generated code is up to date

```bash
make verify-generated
```

## Development

```bash
make build          # Build binary with version metadata
make install        # Install to $GOPATH/bin with version metadata
make test           # Run unit tests
make lint           # Run linter
make generate       # Regenerate from schema
make sync-schema    # Sync schema via introspection
make sync           # Sync schema + regenerate in one step
make clean          # Remove build artifacts
```

### Project Structure

```
cmd/worksome/       CLI entrypoint, auth, completion commands
cmd/generate/       Standalone codegen tool
cmd/introspect/     Schema introspection sync tool
internal/
  client/           GraphQL HTTP client with retry and pagination
  codegen/          Schema parser + code generator
  config/           Profile and token management
  generated/        Generated code (committed, reviewed)
    commands/       Cobra commands (64 resource groups)
    queries/        GraphQL query/mutation functions
    types/          Go types, enums, input objects
  output/           JSON/table formatter with TTY detection
schema/
  schema.graphql    Vendored GraphQL schema (source of truth)
  overrides.yaml    Manual resource grouping overrides
```

## Architecture

See [docs/architecture.md](docs/architecture.md) for detailed diagrams and design decisions.

## License

MIT — see [LICENSE.md](LICENSE.md).

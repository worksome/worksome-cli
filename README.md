# worksome-cli

A multiplatform CLI for the [Worksome GraphQL API](https://docs.worksome.com/). Full API coverage via code generation from a vendored schema — <!-- resources -->77<!-- /resources --> resource groups, <!-- operations -->198<!-- /operations --> operations. Designed for both human users and AI agents.

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

> [!WARNING]
Worksome's API is powerful and allow for potentially dangerous operations, such as terminating hires or operating with payment requests. **Be cautious and oversee the operations done by AI agents using `worksome-cli` or the API**: you are responsible for any API calls made to Worksome to your account using your token.



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

### The skill

[`skills/worksome-cli/`](skills/worksome-cli/) is a ready-made skill: a `SKILL.md`
that teaches an agent to install the CLI, authenticate it, and drive the API
without wasting requests, plus two references — the hiring-lifecycle domain
model and worked recipes for the questions that come up most (active hires,
unapproved timesheets, invoice totals, classification status, approval
bottlenecks). It is what an agent needs to go from "there is a binary" to
answering "how many contractors do we have right now" correctly.

**Claude Code.** This repository is a plugin. From a checkout:

```bash
claude plugin install --plugin-dir /path/to/worksome-cli
```

The skill loads on demand when a conversation touches hires, timesheets,
payment requests, invoices, classifications and so on; it installs the binary
the first time it is actually needed, not at session start.

**Any other agent** (Cursor, Codex, a shell agent). Copy `skills/worksome-cli/`
into wherever your agent reads skills or rules from, or point it at
`SKILL.md` directly. Everything the skill relies on is a shell command.

> [!IMPORTANT]
You need to be an account administrator to be able to create tokens. Tokens work as passwords, and should be maintained with the same level of security and confidentiality as them.

The one thing no skill can supply is a credential. Create a Personal Access
Token at <https://use.worksome.com/integrations/api-tokens> and either export
it as `WORKSOME_API_TOKEN` or run `worksome auth login --token <token>` once;
tokens last six months and one token works on any number of machines.

### Driving the CLI

The CLI is designed to be driven programmatically:

- **Discovery is built in.** `worksome --help` lists all <!-- resources -->77<!-- /resources --> resource groups; `worksome <resource> --help` lists that resource's actions. All help text is generated from the GraphQL schema, so it cannot drift from the API.
- **Permissions are documented per action.** Many operations are restricted to one party — company, worker, or recruiter — and the help text says so (e.g. "Only companies can cancel their hires"). A permission error is usually a correct answer about the token's role, not a bug to work around.
- **JSON without asking.** Output is JSON whenever stdout is not a TTY, so pipelines need no `--output json`. Pass it explicitly if you can't be sure of the environment.
- **Keep responses small.** `--fields id,worker.name` narrows the GraphQL selection set itself — the server is only asked for those fields — and `--filter status=ACTIVE` narrows results. Useful when a full page of objects would be more than you need. A field name that does not exist is an error, not an empty result — except on the few operations whose shape can't be narrowed, where it is still dropped silently.
- **Preview mutations.** `--dry-run` prints the operation name and variables without calling the API. Use it to confirm a mutation's shape before executing it.
- **Pagination is explicit.** List commands return a single page. `--all` auto-paginates, which on large resources means many sequential requests — prefer `--first`/`--page` unless you genuinely need everything.
- **Exit codes over stderr parsing.** `0` on success, non-zero for any failure (auth, validation, GraphQL, network). Branch on the exit code rather than matching on message text, which is not a stable interface.
- **Partial responses still succeed.** GraphQL can fail individual fields while resolving the rest. When that happens the data is written to stdout and the exit code stays `0`; the failed fields are reported on stderr with their response path (`hires.data[0].triggersApproval: ...`). Only a wholly unresolved request is an error. A field that failed still appears in the data as `null`, so stderr is the only way to tell it apart from a legitimately null value — read it if that distinction matters.
- **No update notices in non-interactive use.** The daily release check only runs when both stdout and stderr are terminals, so nothing is ever injected into piped or redirected output. An agent driving the CLI from an interactive session could still see it; `WORKSOME_NO_UPDATE_CHECK=1` disables it outright.
- **`viewer get` is a cheap preflight** to confirm a token works and identify the user it belongs to (id, name, email, and verification flags). It does **not** report the account or the permissions the token carries — the API exposes no role or permission fields — so it cannot tell you whether a given call will be allowed. Treat it as a liveness and identity check only, and discover permissions the way the point above describes: attempt the action and read the permission error as an answer.

### Running in a container

The binaries are statically linked (`CGO_ENABLED=0`), so they run unmodified on glibc, musl (Alpine), and distroless images.

```dockerfile
FROM debian:stable-slim
# Required: the CLI talks HTTPS and slim images ship no CA bundle.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY worksome /usr/local/bin/worksome
ENV WORKSOME_API_TOKEN=...
```

Two things that catch people out:

- **CA certificates.** Without them every request fails with `tls: failed to verify certificate: x509: certificate signed by unknown authority`. `debian:slim` and `distroless/static` need `ca-certificates` installed or copied in; Alpine already ships a bundle. Alternatively point `SSL_CERT_FILE` at a bundle.
- **No `HOME` required.** With `WORKSOME_API_TOKEN` set, the CLI never reads or writes the config file, so it works with `HOME` unset, with a read-only root filesystem, and as a non-root user. Only `worksome auth login` writes to disk.

## Install

### One line (macOS, Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/worksome/worksome-cli/main/scripts/install.sh | bash
```

The script downloads the release for your OS and architecture, verifies it
against the release's `checksums.txt`, and puts the binary in the first
writable directory of `/usr/local/bin`, `~/.local/bin`, `~/bin`. It is
idempotent, and it falls back to `go install` if no prebuilt binary fits. Pin a
version with `WORKSOME_VERSION=v0.6.2`, choose the directory with
`WORKSOME_INSTALL_DIR`, or reinstall over an existing binary with
`WORKSOME_FORCE=1`. It is 130 lines of plain bash; [read it first](scripts/install.sh).

### Homebrew (macOS, Linux)

```bash
brew tap worksome/tap
brew trust --cask worksome/tap/worksome
brew install worksome/tap/worksome
```

The `brew trust` step is required. Homebrew refuses to load casks from third-party taps until you explicitly trust them, so without it the install fails with `Refusing to load cask worksome/tap/worksome from untrusted tap`. See [Tap Trust](https://docs.brew.sh/Tap-Trust).

### Binaries

Prebuilt binaries for macOS, Linux, and Windows (amd64 and arm64) are attached to every [GitHub release](https://github.com/worksome/worksome-cli/releases), with `checksums.txt` to verify them.

### From source

```bash
# Includes version and commit metadata
make install

# Or build a local binary
make build

# Plain go install (no version metadata)
go install github.com/worksome/worksome-cli/cmd/worksome@latest
```

Verify any of the above with:

```bash
worksome version
```

### Upgrading

```bash
worksome version --check
```

Reports whether a newer release exists and prints the upgrade command for how
this binary was actually installed — `brew upgrade --cask worksome`, `go install
...@latest`, or a download link.

On an interactive terminal the CLI also checks once a day in the background and
prints a one-line notice on stderr when a new release is out. It is deliberately
invisible to non-interactive use: the check is skipped entirely unless **both**
stdout and stderr are terminals, so piped output, scripts and non-interactive
containers never see it and never pay for it. It is also skipped when `CI` is set, on `dev` builds,
and when `WORKSOME_NO_UPDATE_CHECK` is set to anything.

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
worksome hires list --fields id,worker.name       # Request only these fields
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
| `--fields` | Comma-separated list of fields to request from the API and display |
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
    commands/       Cobra commands (<!-- resources -->77<!-- /resources --> resource groups)
    queries/        GraphQL query/mutation functions
    types/          Go types, enums, input objects
  output/           JSON/table formatter with TTY detection
schema/
  schema.graphql    Vendored GraphQL schema (source of truth)
  overrides.yaml    Manual resource grouping overrides
```

## Architecture

See [docs/architecture.md](docs/architecture.md) for detailed diagrams and design decisions.

## Contributing

Most of this codebase is generated from the GraphQL schema — see
[CONTRIBUTING.md](CONTRIBUTING.md) before editing anything under
`internal/generated/`.

## Security

Found a vulnerability? Please report it privately rather than opening an issue:
[SECURITY.md](SECURITY.md).

## License

MIT — see [LICENSE.md](LICENSE.md).

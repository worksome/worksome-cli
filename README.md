# worksome-cli

A multiplatform CLI for the [Worksome GraphQL API](https://docs.worksome.com/). Full API coverage via code generation from a vendored schema — 80 resource groups, 197+ operations. Designed for both human users and AI agents.

## Install

```bash
# From source
go install github.com/worksome/worksome-cli/cmd/worksome@latest

# Or build locally
make build
```

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
worksome auth switch <name>   # Switch profile
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
```

### Mutations

Mutations accept input via CLI flags (for scalar fields) or a JSON file:

```bash
# Using flags
worksome jobs create --company <id> --name "My Job"

# Using a JSON file
worksome hires terminate-hire --input terminate.json

# Mix both (flags override file values)
worksome hires terminate-hire --input base.json --reason "COMPLETED"

# Dry run (shows the query without executing)
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
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--token` | API token (overrides config) |
| `--endpoint` | Custom API endpoint |
| `--profile` | Config profile name |
| `--output` | Output format: `json`, `table` |
| `--verbose` | Show request/response details |
| `--no-color` | Disable colored output |
| `--dry-run` | Show query without executing |

## Shell Completion

```bash
# Bash
source <(worksome completion bash)

# Zsh
source <(worksome completion zsh)

# Fish
worksome completion fish | source
```

## Schema Sync & Code Generation

The CLI is generated from a vendored GraphQL schema. When the API changes:

```bash
make sync-schema    # Copy latest schema from platform repo
make generate       # Regenerate Go code
make sync           # Both in one step
```

### Verify generated code is up to date

```bash
make verify-generated
```

## Development

```bash
make build          # Build binary
make test           # Run unit tests
make lint           # Run linter
make generate       # Regenerate from schema
make clean          # Remove build artifacts
```

### Project Structure

```
cmd/worksome/       CLI entrypoint, auth, completion commands
cmd/generate/       Standalone codegen tool
internal/
  client/           GraphQL HTTP client with retry and pagination
  codegen/          Schema parser + code generator
  config/           Profile and token management
  generated/        Generated code (committed, reviewed)
    commands/       Cobra commands (80 resource groups)
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

Proprietary - Worksome internal use.

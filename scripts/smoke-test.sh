#!/usr/bin/env bash
# Smoke test: run every generated command against the real API and classify results.
# Usage: ./scripts/smoke-test.sh [worksome-binary] [--profile <name>]
#
# Categories:
#   OK          — command succeeded
#   PERMISSION  — API returned auth/permission error (expected for some resources)
#   VALIDATION  — GraphQL validation error (BUG in our generated queries)
#   OTHER       — other errors (network, etc.)

set -euo pipefail

WORKSOME="${1:-$(go env GOPATH)/bin/worksome}"
DUMMY_ID="00000000-0000-0000-0000-000000000000"
PROFILE_ARGS=()

# Parse optional --profile flag
shift || true
while [[ $# -gt 0 ]]; do
    case "$1" in
        --profile)
            PROFILE_ARGS=(--profile "$2")
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

ok=0
perm=0
validation=0
other=0
total=0

declare -a validation_cmds=()
declare -a other_cmds=()
declare -a perm_cmds=()
declare -a ok_cmds=()

run_cmd() {
    local label="$1"
    shift
    local output
    total=$((total + 1))

    output=$("$@" 2>&1) && rc=0 || rc=$?

    if [ $rc -eq 0 ]; then
        printf "${GREEN}  OK${NC}          %s\n" "$label"
        ok=$((ok + 1))
        ok_cmds+=("$label")
    elif echo "$output" | grep -q "GRAPHQL_VALIDATION_FAILED"; then
        printf "${RED}  VALIDATION${NC}  %s\n" "$label"
        local msg
        msg=$(echo "$output" | grep -o '"message":"[^"]*"' | head -1)
        printf "              → %s\n" "$msg"
        validation=$((validation + 1))
        validation_cmds+=("$label|$msg")
    elif echo "$output" | grep -qi -E "access|permission|unauthorized|forbidden|owner|Only the owner"; then
        printf "${YELLOW}  PERMISSION${NC}  %s\n" "$label"
        perm=$((perm + 1))
        perm_cmds+=("$label")
    else
        printf "${CYAN}  OTHER${NC}       %s\n" "$label"
        local truncated
        truncated=$(printf "%.200s" "$output")
        printf "              → %s\n" "$truncated"
        other=$((other + 1))
        other_cmds+=("$label|$truncated")
    fi
}

echo "=== Worksome CLI Smoke Test ==="
echo "Binary: $WORKSOME"
if [ ${#PROFILE_ARGS[@]} -gt 0 ]; then
    echo "Profile: ${PROFILE_ARGS[1]}"
fi
echo ""

# Get all resource commands (skip auth, version, completion, help)
resources=$(${WORKSOME} --help 2>&1 | grep -E '^\s{2}\w' | awk '{print $1}' | grep -v -E '^(worksome|auth|version|completion|help)$')

for res in $resources; do
    # Check what subcommands exist
    subcmds=$("$WORKSOME" "$res" --help 2>&1 | grep -E '^\s{2}(list|get|create|update|delete|approve|reject|cancel|terminate|share|send|generate|run|set|store|upload|change|verify|onboard|retry|mark|duplicate|open|end|attach|detach|invite|remove|block|reinvite|accept|action|attribute|manage)\b' | awk '{print $1}' || true)

    if [ -z "$subcmds" ]; then
        # Hoisted command (no subcommands with known verbs) — try running it with --dry-run first to see if it's a real command
        has_run=$("$WORKSOME" "$res" --help 2>&1 | grep -c "RunE\|--input\|--dry-run" || true)
        if [ "$has_run" -gt 0 ] || ! "$WORKSOME" "$res" --help 2>&1 | grep -q "Available Commands"; then
            run_cmd "$res (hoisted)" "$WORKSOME" "$res" "${PROFILE_ARGS[@]}" --dry-run
        fi
        continue
    fi

    for sub in $subcmds; do
        case "$sub" in
            list)
                run_cmd "$res list" "$WORKSOME" "$res" list "${PROFILE_ARGS[@]}" -n 1
                ;;
            get)
                run_cmd "$res get" "$WORKSOME" "$res" get "${PROFILE_ARGS[@]}" "$DUMMY_ID"
                ;;
            *)
                # For mutations, just do a dry-run to verify they parse
                run_cmd "$res $sub (dry-run)" "$WORKSOME" "$res" "$sub" "${PROFILE_ARGS[@]}" --dry-run
                ;;
        esac
    done
done

echo ""
echo "=== Results ==="
printf "  Total:      %d\n" "$total"
printf "  ${GREEN}OK:${NC}         %d\n" "$ok"
printf "  ${YELLOW}Permission:${NC} %d\n" "$perm"
printf "  ${RED}Validation:${NC} %d  ← BUGS to fix\n" "$validation"
printf "  ${CYAN}Other:${NC}      %d\n" "$other"

if [ ${#validation_cmds[@]} -gt 0 ]; then
    echo ""
    echo "=== Validation Errors (BUGS) ==="
    for entry in "${validation_cmds[@]}"; do
        cmd="${entry%%|*}"
        msg="${entry#*|}"
        echo "  - $cmd"
        [ -n "$msg" ] && echo "    $msg"
    done
fi

if [ ${#other_cmds[@]} -gt 0 ]; then
    echo ""
    echo "=== Other Errors ==="
    for entry in "${other_cmds[@]}"; do
        cmd="${entry%%|*}"
        msg="${entry#*|}"
        echo "  - $cmd"
        echo "    $msg"
    done
fi

if [ ${#perm_cmds[@]} -gt 0 ]; then
    echo ""
    echo "=== Permission Errors ==="
    for cmd in "${perm_cmds[@]}"; do
        echo "  - $cmd"
    done
fi

exit $validation

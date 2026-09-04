#!/usr/bin/env bash
# Smoke test: run every generated command against the real API and classify results.
# Usage: ./scripts/smoke-test.sh [worksome-binary] [--profile <name>]
#
# Categories:
#   OK          — command succeeded
#   PERMISSION  — API returned auth/permission error (expected for some resources)
#   SKIPPED     — the probe itself was incomplete (mutation run with no --input,
#                 or a required flag not supplied). Expected, not a defect.
#   SERVER      — API returned a 5xx. Real, but a platform bug rather than a CLI
#                 one, so it is reported loudly and does not fail the run.
#   VALIDATION  — GraphQL validation error (BUG in our generated queries)
#   OTHER       — anything else (BUG, or a genuinely new failure worth reading)
#
# Exits non-zero when VALIDATION, OTHER or JSONFLAG is non-empty, so it can gate
# CI. PERMISSION, SKIPPED and SERVER are expected against a real account and do
# not fail the run.

set -euo pipefail

# check_json_flags parses the --dry-run variables with jq. Without it every
# JSON-flag check fails and the run exits 1 blaming the CLI for a missing tool,
# so refuse to start rather than report 41 false bugs.
# Run it rather than just looking for it on PATH: a jq that exists but errors
# fails the same way as a missing one, and both are indistinguishable from real
# bugs once the checks start.
if ! echo '{}' | jq -e . >/dev/null 2>&1; then
    echo "Error: a working jq is required (used to inspect --dry-run variables)" >&2
    exit 2
fi

WORKSOME="${1:-$(go env GOPATH)/bin/worksome}"
DUMMY_ID="00000000-0000-0000-0000-000000000000"
PROFILE_ARGS=()

# Parse optional --profile flag
shift || true
while [[ $# -gt 0 ]]; do
    case "$1" in
        --profile)
            if [[ $# -lt 2 || -z "$2" ]]; then
                echo "Error: --profile requires a value" >&2
                exit 1
            fi
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
skipped=0
server=0
jsonflag=0
jsonchecked=0
total=0

declare -a validation_cmds=()
declare -a skipped_cmds=()
declare -a server_cmds=()
declare -a jsonflag_cmds=()
declare -a other_cmds=()
declare -a perm_cmds=()
declare -a ok_cmds=()

run_cmd() {
    local label="$1"
    shift
    local output
    total=$((total + 1))

    local rc=0
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
    # Caveat: this cannot tell "the probe did not supply it" from "codegen made
    # the flag required when it should not be". A regression of the latter kind
    # lands here as expected noise. Narrow the pattern if that ever bites.
    elif echo "$output" | grep -qi -E "no input provided|required flag\(s\)"; then
        printf "${CYAN}  SKIPPED${NC}     %s\n" "$label"
        skipped=$((skipped + 1))
        skipped_cmds+=("$label")
    elif echo "$output" | grep -qi -E "internal server error|5[0-9][0-9] |bad gateway|service unavailable"; then
        printf "${YELLOW}  SERVER${NC}      %s\n" "$label"
        local smsg
        smsg=$(printf "%.160s" "$output")
        printf "              -> %s\n" "$smsg"
        server=$((server + 1))
        server_cmds+=("$label|$smsg")
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

# kebabToCamel converts a CLI flag name to its GraphQL variable name.
kebab_to_camel() {
    echo "$1" | awk -F- '{printf "%s", $1; for (i=2; i<=NF; i++) printf "%s%s", toupper(substr($i,1,1)), substr($i,2)}'
}

# check_json_flags exercises every input-object flag on a command and asserts the
# value reaches the GraphQL variables as JSON rather than a bare string.
#
# This is the #21 class: the flag was registered as a String and sent verbatim,
# so the server rejected the type on every call and nothing local ever noticed.
# --dry-run prints the variables without calling the API, so this needs no token
# and no valid ids — "{}" is never a legitimate string value for an input object.
check_json_flags() {
    local res="$1" sub="$2"
    local flags flag varname out vars
    flags=$("$WORKSOME" "$res" "$sub" --help 2>&1 | grep -oE '^\s+--[a-z0-9-]+ string .*\(JSON for ' | grep -oE '\-\-[a-z0-9-]+' || true)
    [ -z "$flags" ] && return 0

    for flag in $flags; do
        varname=$(kebab_to_camel "${flag#--}")
        total=$((total + 1))
        jsonchecked=$((jsonchecked + 1))
        local label="$res $sub $flag (json)"

        if ! out=$("$WORKSOME" "$res" "$sub" ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} "$flag" '{}' --dry-run 2>&1); then
            printf "${RED}  JSONFLAG${NC}    %s\n" "$label"
            printf "              → rejected valid JSON: %.160s\n" "$out"
            jsonflag=$((jsonflag + 1))
            jsonflag_cmds+=("$label|rejected valid JSON")
            continue
        fi

        # Drop the "[dry-run] query Name" header; the rest is the variables object.
        vars=$(echo "$out" | tail -n +2)
        # "{}" went in, so an object must come out. Asserting the exact type
        # rather than "not a string" also catches a null or a number.
        if echo "$vars" | jq -e --arg v "$varname" '(.[$v] | type) == "object"' >/dev/null 2>&1; then
            printf "${GREEN}  OK${NC}          %s\n" "$label"
            ok=$((ok + 1))
            ok_cmds+=("$label")
        else
            local got
            got=$(echo "$vars" | jq -r --arg v "$varname" 'if has($v) then (.[$v] | type) else "absent" end' 2>/dev/null || echo "unparseable")
            printf "${RED}  JSONFLAG${NC}    %s\n" "$label"
            printf "              → %s serialised as %s, want object\n" "$varname" "$got"
            jsonflag=$((jsonflag + 1))
            jsonflag_cmds+=("$label|serialised as $got")
        fi
    done
}

echo "=== Worksome CLI Smoke Test ==="
echo "Binary: $WORKSOME"
if [ ${#PROFILE_ARGS[@]} -gt 0 ]; then
    echo "Profile: ${PROFILE_ARGS[1]}"
fi
echo ""

# Get all resource commands (skip auth, version, completion, help)
resources=$("${WORKSOME}" --help 2>&1 | grep -E '^\s{2}\w' | awk '{print $1}' | grep -v -E '^(worksome|auth|version|completion|help)$')

for res in $resources; do
    # Check what subcommands exist
    subcmds=$("$WORKSOME" "$res" --help 2>&1 | grep -E '^\s{2}(list|get|create|update|delete|approve|reject|cancel|terminate|share|send|generate|run|set|store|upload|change|verify|onboard|retry|mark|duplicate|open|end|attach|detach|invite|remove|block|reinvite|accept|action|attribute|manage)\b' | awk '{print $1}' || true)

    if [ -z "$subcmds" ]; then
        # Hoisted command (no subcommands with known verbs) — try running it with --dry-run first to see if it's a real command
        has_run=$("$WORKSOME" "$res" --help 2>&1 | grep -c "RunE\|--input\|--dry-run" || true)
        if [ "$has_run" -gt 0 ] || ! "$WORKSOME" "$res" --help 2>&1 | grep -q "Available Commands"; then
            run_cmd "$res (hoisted)" "$WORKSOME" "$res" ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} --dry-run
        fi
        continue
    fi

    for sub in $subcmds; do
        case "$sub" in
            list)
                run_cmd "$res list" "$WORKSOME" "$res" list ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} -n 1
                check_json_flags "$res" list
                ;;
            get)
                run_cmd "$res get" "$WORKSOME" "$res" get ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} "$DUMMY_ID"
                ;;
            *)
                # For mutations, just do a dry-run to verify they parse
                run_cmd "$res $sub (dry-run)" "$WORKSOME" "$res" "$sub" ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} --dry-run
                ;;
        esac
    done
done

echo ""
echo "=== Results ==="
printf "  Total:      %d\n" "$total"
printf "  ${GREEN}OK:${NC}         %d\n" "$ok"
printf "  ${YELLOW}Permission:${NC} %d  (expected)\n" "$perm"
printf "  ${CYAN}Skipped:${NC}    %d  (expected — incomplete probe)\n" "$skipped"
printf "  ${YELLOW}Server 5xx:${NC} %d  <- platform bugs, not CLI\n" "$server"
printf "  ${RED}Validation:${NC} %d  <- BUGS to fix\n" "$validation"
printf "  ${RED}JSON flags:${NC} %d failed of %d exercised\n" "$jsonflag" "$jsonchecked"
printf "  ${RED}Other:${NC}      %d  <- read these\n" "$other"

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

if [ ${#server_cmds[@]} -gt 0 ]; then
    echo ""
    echo "=== Server errors (platform-side — file against the API, not the CLI) ==="
    for entry in "${server_cmds[@]}"; do
        echo "  - ${entry%%|*}"
        echo "    ${entry#*|}"
    done
fi

if [ ${#jsonflag_cmds[@]} -gt 0 ]; then
    echo ""
    echo "=== Input-object flags not sent as JSON (BUGS) ==="
    for entry in "${jsonflag_cmds[@]}"; do
        echo "  - ${entry%%|*}"
        echo "    ${entry#*|}"
    done
fi

if [ ${#perm_cmds[@]} -gt 0 ]; then
    echo ""
    echo "=== Permission Errors ==="
    for cmd in "${perm_cmds[@]}"; do
        echo "  - $cmd"
    done
fi

# A guard that stops guarding must fail, not pass quietly. check_json_flags
# discovers flags from the "(JSON for X)" help text; if that wording changes it
# finds nothing, exercises nothing, and "JSON flags: 0" would read as success.
# The schema always has at least one input-object flag, so zero means broken
# discovery rather than a clean run.
if [ "$jsonchecked" -eq 0 ]; then
    echo ""
    printf "${RED}FAILED${NC}: no input-object flags discovered — the '(JSON for' help text\n"
    printf "        this check greps for has probably changed. Fix check_json_flags.\n"
    exit 1
fi

# Exit status, not a count: `exit $validation` wrapped to 0 at 256 failures.
# PERMISSION, SKIPPED and SERVER are expected against a real account and are
# deliberately left out of the sum; VALIDATION, OTHER and JSONFLAG are the ones
# that mean a bug in this repo.
failures=$((validation + jsonflag + other))
if [ "$failures" -gt 0 ]; then
    echo ""
    printf "${RED}FAILED${NC}: %d validation, %d json-flag, %d other\n" "$validation" "$jsonflag" "$other"
    exit 1
fi

echo ""
printf "${GREEN}PASSED${NC}: %d ok, %d permission, %d skipped (all expected)\n" "$ok" "$perm" "$skipped"
[ "$server" -gt 0 ] && printf "${YELLOW}NOTE${NC}: %d server 5xx — platform-side, listed above\n" "$server"
exit 0

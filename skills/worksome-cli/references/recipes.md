# Recipes

Every command below was run against the live API and returned data. Field names
vary per resource — do not copy a field list from one resource to another.

## Discovering valid fields

The fastest way to learn a resource's fields is to ask for a wrong one. The
error lists the real ones:

```bash
worksome jobs list --fields id,bogus
# Error: jobs: --fields: unknown field "bogus"
#   (available: id, number, name, contactName, contactEmail, contactPhone,
#    skills, description, market, status, address, location, ... and 59 more)
```

Same for actions and flags: `worksome <resource> --help`.

## Verified commands

```bash
# Companies under the account
worksome companies list --fields id,name
# -> Worksome, Worksome UK, Worksome USA Inc., ...

# Hires with the worker and hiring company named
worksome hires list --first 20 --fields id,status,worker.name,company.name

# Active hires only
worksome hires list --first 50 --fields id,worker.name --filter status=ACTIVE

# Jobs — note the field is `name`, not `title`
worksome jobs list --first 20 --fields id,name,status

# Timesheets — no `status` field; use dates and the hire
worksome timesheets list --first 20 --fields id,worker,hire,startDate,endDate

# Payment requests, with status (UNAPPROVED / ...)
worksome payment-requests list --first 20 --fields id,status

# Invoices
worksome invoices list --first 20 --fields id

# Who am I
worksome viewer get
```

## Field-name gotchas found so far

| Resource | Watch out |
|---|---|
| `jobs` | The name field is `name`, not `title`. 62+ fields available. |
| `timesheets` | No `status` field. Available: `id`, `worker`, `hire`, `startDate`, `endDate`, `createdAt`. |
| `accounts` | Not paginated — `--first` is rejected. |
| `payment-requests` | `--order-by` clauses use `field`, **not** `column`: `[{"field":"ISSUED_AT","order":"DESC"}]`. |

## Timesheet approval lives on payment requests

A timesheet carries no approval state — `timesheets` exposes only `id`, `worker`,
`hire`, `startDate`, `endDate`, `createdAt`, and its only filters are
`--accounts` and paging. Approval is a **payment request** concept. So "the
latest approved timesheet" means "the latest approved payment request that has a
timesheet attached":

```bash
worksome payment-requests list --first 60 --statuses APPROVED --has-timesheet \
  --order-by '[{"field":"ISSUED_AT","order":"DESC"}]' \
  --fields id,number,issuedAt,manuallyApprovedAt,autoApprovedAt,worker.name,company.name,currency,billedAmount,timesheet.id,timesheet.startDate,timesheet.endDate,approvedBy.name
```

Statuses: `APPROVED`, `UNAPPROVED`, `REJECTED`, `CANCELLED`.

**Sorting by approval date requires client-side work.** `PaymentRequestOrderByColumn`
offers only `ISSUED_AT`, `PRIORITY`, `WORKER`, `TOTAL_AMOUNT` — there is no
approval-date ordering. The real approval timestamps are `manuallyApprovedAt`
and `autoApprovedAt` (one or the other, not both). Fetch a window ordered by
`ISSUED_AT DESC`, then sort locally:

```bash
... --output json | jq -r '[.[] | . + {appr: ([.manuallyApprovedAt, .autoApprovedAt] | map(select(.)) | sort | last)}]
  | map(select(.appr)) | sort_by(.appr) | reverse | .[0:5]
  | .[] | "\(.appr)  #\(.number)  \(.worker.name)  \(.currency) \(.billedAmount)"'
```

A request issued weeks ago can be approved today, so issue-date order is **not**
approval order — widen `--first` if the answer must be exhaustive, and say which
window was searched.

## Useful payment-request filters

`--statuses`, `--has-timesheet`, `--has-expenses`, `--has-batch`,
`--has-purchase-order-number`, `--hires`, `--jobs`, `--clients`, `--accounts`,
`--currencies`, `--rate-types`, `--request-types` (TIME/AMOUNT/EXPENSES),
`--requested-date-range`, `--billing-start-date-range`,
`--billing-end-date-range`, `--timesheet-period`, `--is-payrolled`, `--search`,
`--viewer-role` (PAYER/PAYEE/ANY). Date-range flags take JSON for
`DateRangeInput`.

## Composing with jq

Output is JSON whenever stdout is not a terminal, so no flag is needed from a
tool call:

```bash
worksome hires list --all --fields id,status | jq -r 'group_by(.status)[] | "\(.[0].status): \(length)"'
```

`--all` paginates sequentially — one request per page. Prefer `--first`/`--page`
unless the whole set is genuinely needed.

## Nested fields

Dotted paths select nested objects and narrow the GraphQL selection set:

```bash
worksome hires list --first 5 --fields id,worker.name,company.name,job.name
```

## Decoding an ID before acting on it

```bash
echo -n 'SGlyZToxMjM=' | base64 -d     # -> Hire:123
echo -n 'Sm9iOjI1'     | base64 -d     # -> Job:25
```

Do this before passing an ID to a mutation — a mistyped base64 string can still
be valid base64 for a different record.

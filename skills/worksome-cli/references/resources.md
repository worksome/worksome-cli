# Worksome resources

Worksome is a Freelance Management System: companies source, contract, onboard
and pay external workers while staying compliant with local employment and tax
law. Classification matters as much as payment — getting a contractor's status
wrong (IR35 in the UK, worker classification in the US) is a legal and financial
risk, so it is first-class data.

## Domain model

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
| `organisation`, `companies` | The hiring side. An organisation can contain several companies. |
| `worker` | The person being hired. |
| `jobs` | A job a company has created on Worksome. |
| `projects` | A project with a budget containing one or more jobs. |
| `bids` | A bid sent on a job. Accepting one hires the worker. |
| `hires` | The link between a worker, a company and a job. Explicitly **not** a legal document. |
| `contracts` | The actual legal document, reached from a hire. |
| `compliance`, `gate` | A requirement that must be met before a business process can proceed. |
| `classifications` | A worker classification (SDS/WCR) for a hire — IR35, US Worker Classification. |
| `timesheets` | Work logged against a hire. |
| `payment-requests` | A worker's request to be paid; carries a hire and a timesheet. |
| `invoices` | Created when a company approves payment requests. Defines the amount the company owes Worksome. |
| `batches` | Groups items for bulk actions. Mainly internal operational workflows — for most integrations, work with individual items instead. |
| `approvals`, `approval-rules`, `approvers`, `workflows` | Approval flows, conditions, assignments and states that gate actions. |
| `viewer` | The currently authenticated user. Cheapest way to check who a token belongs to. |

## Actions by resource

Verified against `worksome <resource> --help`. Always re-check with `--help`
before use — help text is generated from the schema, so it is authoritative.

### Core lifecycle

**`hires`** — `list`, `get`, `create-draft`, `share`, `reject`, `cancel`,
`terminate`, `attribute-recruiter-to`, `attribute-supplier-to`,
`remove-recruiter-from`
- All parties of a hire can read it. Only companies can create, cancel or
  attribute. Draft hires must be completed in the Worksome UI before they
  become active.

**`jobs`** — `list`, `get`, `create`, `update`, `duplicate`, `end`,
`set-internal-budget-on`
- `create` makes a `DRAFT` with just title, skills and owner; `update` sets the
  full details and publishes. Only companies can create or update.

**`bids`** — `list`, `get` (read-only; hire by accepting via `accept-bid`)

**`projects`** — `list`, `get`, `create`, `update`, `delete`, `end`, `open`,
`attach-jobs-to`, `detach-job-from`

**`contracts`** — `list`, `get` (read-only)

**`classifications`** — `list` (per hire), `get`

**`compliance`** — `get` (per hire; optional `names` argument to narrow)

### Time and money

**`timesheets`** — `list`, `get`, `create`, `create-custom`, `update`, `delete`
- Only workers can create, update or delete. `create-custom` accepts a custom
  data format defined by the input schema.

**`payment-requests`** — `list`, `get`, `create`, `update`, `delete`

**`invoices`** — `list`, `get` (read-only), plus `invoice-row get`

**`batches`** — `list`/`get`, and `batch-action` to run an action on a batch

**`bank-details`** — `update`

### Identity and access

**`viewer`** — `get`
**`accounts`** — `get` (not paginated; no `--first`)
**`companies`** — `list`, `get`
**`organisation`** — `get`
**`worker`** — `get`, `update`
**`multi-factors`**, `password`, `email`, `user-groups`

### Approvals and workflow

**`approvals`** — `list`, `get`, `create`, `update` (updating creates a new
version). Requires the `manage-approvals` team permission.
**`approval-rules`**, `approval-approvables` (runtime instances created when a
trigger fires), `approval-states` (approvals, rejections, change requests),
`approvers`, `workflows`, `workflow-variables`

### Recruitment and talent

`accept-bid`, `job-candidates`, `job-candidate-status`,
`job-candidate-preferred`, `job-candidate-step`, `forward-candidate`,
`withdraw-forwarded-candidate`, `withdraw-job-candidate`, `job-shares`,
`recruiters`, `recruiter-candidates`, `company-recruiters`,
`company-recruiter-regions`, `trusted-contacts`,
`organisation-trusted-contacts`, `block-trusted-contact`,
`reinvite-trusted-contact`, `invite-link`, `partner`, `incoming-jobs`,
`supplier-candidates`, `company-suppliers`, `candidate-to-onboard`

### Administration and platform

`custom-fields`, `inherited-custom-fields`, `supplier-shared-custom-fields`,
`worker-custom-field-values`, `webhooks`, `webhook-events`,
`webhook-event-logs`, `export`, `files`, `files-as-uploaded`,
`onboarding-documents`, `submit-compliance`, `worker-identification`,
`employments`, `employment-changes`, `conversations`, `note`, `milestones`,
`skills`, `industries`, `countries`, `timesheet-registration`,
`worksome-intelligence-consent`

## Bulk data

`export create` returns an export URL and a row count. For anything larger than
a few hundred records, prefer an export over `list --all` — `--all` issues one
request per page, sequentially.

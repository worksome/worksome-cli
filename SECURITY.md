# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately through GitHub:

1. Go to the [Security tab](https://github.com/worksome/worksome-cli/security)
2. Click **Report a vulnerability**

This opens a private advisory visible only to you and the maintainers. You do
not need to be a member of the Worksome organisation to use it.

If you cannot use GitHub for any reason, contact any Worksome employee and ask
to be put in touch with the platform team — but the private advisory is the
fastest route and keeps everything in one place.

## What to include

- What an attacker can do, not just what looks wrong
- The steps to reproduce it, and the version (`worksome version`)
- Whether it needs a valid API token, and with what permissions

## What to expect

We aim to acknowledge a report within three working days and to keep you
updated as we investigate. If we accept the report we will agree disclosure
timing with you, and credit you in the advisory unless you would rather we
did not.

## Supported versions

Only the [latest release](https://github.com/worksome/worksome-cli/releases/latest)
is supported. Fixes ship in a new release rather than as patches to older
tags, so upgrading is the remedy:

```bash
worksome version --check
```

## Scope

This policy covers the CLI in this repository. Vulnerabilities in the Worksome
API itself, or in the platform behind it, are not in scope here — report those
through the same private advisory and we will route them internally.

### Things that are working as intended

- **Your API token is stored in `~/.worksome/config.yaml`** with `0600`
  permissions in a `0700` directory. It is stored in plain text, as most CLIs
  do; anyone who can read your home directory as your user can read it. Use
  `WORKSOME_API_TOKEN` if you would rather it never touch disk.
- **`--verbose` prints request and response bodies** to stderr, which will
  include whatever data the API returned. It does not print the `Authorization`
  header, so it will not leak your token — but it can contain personal data, so
  take care before pasting it into a bug report.

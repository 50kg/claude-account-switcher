# Security

The switcher handles Claude sign-in tokens, so security reports matter a lot here.

## Reporting a problem

Please **do not open a public issue.** Report it privately instead:
[Report a vulnerability](https://github.com/SnlperStripes/claude-account-switcher/security/advisories/new).

Include what you found, how to reproduce it, and which version you tested. You should get an answer within a week.

## What counts

Anything that could expose or misuse a saved sign-in, for example:

- Tokens or cookies written in plain text outside the explicitly user-created `.cas-account` export or logged. OAuth tokens must only go to `api.anthropic.com`; the desktop web session cookie for subscription dates must only go to `claude.ai`.
- The Chinese maintenance fork's user-created `.cas-account` file is intentionally unencrypted. Report any export that omits its warning, includes another account or chats, writes outside the path selected by the user, or leaves an unintended plaintext temporary file.
- Other users or programs on the machine being able to read the saved sign-ins more easily than Claude's own files
- A release binary that does not match its `.sha256` file or the source

## Supported versions

Only the latest release gets fixes.

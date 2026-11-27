# Security

The switcher handles Claude sign-in tokens and web session cookies, so security reports matter a lot here.

## Reporting a problem

Please **do not open a public issue.** Report it privately instead:
[Report a vulnerability](https://github.com/50kg/claude-account-switcher/security/advisories/new).

Include what you found, how to reproduce it, and which version you tested. You should get an answer within a week.

## What counts

Anything that could expose or misuse a saved sign-in, for example:

- Tokens or cookies logged, or written in plain text anywhere other than the explicitly user-created `.cas-account` export.
- OAuth tokens sent anywhere other than `api.anthropic.com`. The desktop web session cookie used for subscription dates may only go to `api.anthropic.com` (direct check) or to `claude.ai` (the verification window the user opens).
- A `.cas-account` export that omits its warning, includes another account or chats, writes outside the path the user selected, or leaves an unintended plaintext temporary file. The file itself is intentionally unencrypted; that is the only plaintext exception.
- Other users or programs on the machine being able to read the saved sign-ins more easily than Claude's own files.
- The self-update installing a file that does not match the release's published `.sha256`, or a release binary that does not match its `.sha256` file or the source.

## Supported versions

Only the latest release gets fixes.

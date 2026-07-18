# Contributing

## Public data rules

This repository must be safe to clone and publish without exposing a real QQ
account, group, conversation, local path, API endpoint, cookie, or secret.

- Never paste production logs, database rows, chat exports, QR codes, cookies,
  screenshots, or generated media into source files or test fixtures.
- Use `Alice`, `Bob`, and `Carol` for people in tests.
- Use five-digit synthetic identifiers: `10000-19999` for users and bots,
  `20000-29999` for groups, and `30000-39999` for messages.
- Use `example.com`, `example.test`, or an in-process test server for network
  fixtures. Do not use a private relay or deployment hostname.
- Use obvious placeholders such as `test-api-key`; never use a real token even
  when a test is meant to verify redaction.
- Put deployment-specific values in environment variables, the WebUI-backed
  SQLite configuration, or an ignored local configuration file.

Run `make audit-public` before committing. If a real secret was committed,
rotate it first; deleting it from the latest revision does not remove it from
Git history.

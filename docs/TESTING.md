# Testing

## Running

```sh
go test -cover .          # tests with coverage summary
go run ./scripts/check    # every gate
```

## Policy

- Statement coverage must stay at or above 80%.
- Tests exercise the public API only.
- Table-driven tests are preferred.
- Tests are deterministic: no network usage, no wall clock dependence.
- Each bug fix starts with a failing test.

## Coverage

- SMTP conformance: Verifies command sequences (MAIL before RCPT, etc.), DATA handling (dot-stuffing), and size limits.
- MIME: Decodes multipart/alternative, quoted-printable, base64, and encoded subjects.
- Store: FIFO eviction logic.
- HTTP: API return codes, sandboxed iframe CSP headers, and template escaping.
- Timeouts: Asserts `421` timeout responses for slow connections.
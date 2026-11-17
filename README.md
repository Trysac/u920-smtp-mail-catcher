# smtp-mail-catcher

[![CI](https://github.com/Trysac/u920-smtp-mail-catcher/actions/workflows/ci.yml/badge.svg)](https://github.com/Trysac/u920-smtp-mail-catcher/actions/workflows/ci.yml)

Applications under development often send real e-mail by accident, or developers cannot see what a password reset or invoice message looks like without a real mailbox. This package provides an in-memory SMTP server that never relays, stores a bounded number of messages, decodes multipart MIME, and serves them through a JSON API and an HTML view that cannot run captured scripts.

## Installation

```bash
go get github.com/Trysac/u920-smtp-mail-catcher
```

## Usage Example

```go
srv := smtpmailcatcher.NewServer(smtpmailcatcher.Config{})
l, _ := net.Listen("tcp", "127.0.0.1:1025")
go srv.Serve(l)
http.ListenAndServe("127.0.0.1:1080", srv.HTTPHandler())
```

## Public API Summary

- `NewServer(cfg Config) *Server`: Creates the server instance.
- `Serve(l net.Listener) error`: Starts the SMTP loop.
- `HTTPHandler() http.Handler`: Returns the inspector UI and API handler.
- `Messages() []Message`: Returns a copy of the message store (newest first).
- `Config`: Controls limits (`MaxMessages`, `MaxMessageBytes`, `MaxRecipients`, `CommandTimeout`).
- `Error`: Returned by the API with code `not_found` when accessing invalid message IDs.

## Why not MailHog or Mailpit?

This utility is an embeddable Go package with a bounded store that is usable directly from integration tests, whereas external tools require running separate infrastructure processes.

## Security

See [docs/SECURITY.md](docs/SECURITY.md) for the threat model and limits.
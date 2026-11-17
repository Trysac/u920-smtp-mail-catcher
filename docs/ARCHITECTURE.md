# Architecture

`smtp-mail-catcher` provides an in-memory SMTP receiver and HTTP inspection server designed for testing and development environments.

## Design Overview

1. **SMTP Connection Lifecycle**:
   - Connections are received by `Server.Serve(net.Listener)`.
   - Each connection runs a handler with per-command `CommandTimeout` deadlines.
   - The server implements a strict SMTP subset (RFC 5321) including EHLO (SIZE, 8BITMIME, PIPELINING), MAIL FROM, RCPT TO, DATA, RSET, NOOP, QUIT, and VRFY.
   - Lines are enforced to 1000 octets including CRLF. DATA transparency (dot-stuffing) is applied per RFC 5321 section 4.5.2.

2. **MIME Parser**:
   - Uses standard library `net/mail`, `mime`, and `mime/multipart`.
   - Encoded words in headers are decoded for UTF-8, US-ASCII, and ISO-8859-1.
   - Multipart bodies are recursively parsed (depth limit 10, max 100 parts).
   - Text parts are automatically identified; others are treated as attachments.

3. **In-Memory Store**:
   - Bounded ring storage keeping `MaxMessages` (oldest evicted).
   - Protected by `sync.RWMutex` for concurrent access.

4. **HTTP Interface**:
   - REST API: `GET /api/messages`, `GET /api/messages/{id}`, `DELETE /api/messages`.
   - Web Interface: Uses `html/template` to prevent XSS. HTML email bodies are rendered in a sandboxed `<iframe>` pointing to `/messages/{id}/html` which serves content with a strict CSP (`default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox`) and `X-Content-Type-Options: nosniff`.
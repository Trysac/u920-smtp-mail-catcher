# Security Model

## Defended Threats

1. **Script Execution via HTML Bodies**:
   Captured emails are rendered in a sandboxed `<iframe>`. The `/messages/{id}/html` endpoint returns the body with `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox` and `X-Content-Type-Options: nosniff`.

2. **Cross-Site Scripting (XSS)**:
   The UI uses `html/template` for contextual escaping of subjects and metadata.

3. **Header Injection**:
   Attachment filenames are sanitized to remove quotes and control characters.

4. **Resource Exhaustion**:
   Limits are enforced on:
   - Message size (`MaxMessageBytes`, default 10 MiB).
   - Recipient count (`MaxRecipients`, default 100).
   - Command line length (1,000 bytes).
   - MIME parsing (depth 10, 100 parts).
   - Storage (FIFO eviction via `MaxMessages`).

5. **Slowloris Attacks**:
   Every command read enforces `CommandTimeout`.

## Not Defended

- **Authentication**: Not implemented; intended for local/CI use.
- **Encryption**: TLS and STARTTLS are not implemented.
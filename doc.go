// Package smtpmailcatcher implements an embeddable, in-memory SMTP mail catcher
// designed for local integration tests and developer workflows.
//
// The server speaks RFC 5321 SMTP, accepts all recipients without relaying,
// bounds memory usage through strict message and recipient limits, decodes MIME
// multipart messages (including subject encoded-words and base64/quoted-printable parts),
// and provides an isolated HTTP viewer and JSON API.
//
// Threat Model:
//   - Defended: Script execution from captured HTML bodies via sandboxed iframes and strict CSP.
//   - Defended: Cross-Site Scripting (XSS) in HTML listings via html/template automatic contextual escaping.
//   - Defended: Header injection through attachment filenames (quotes and ASCII control characters stripped).
//   - Defended: Memory exhaustion via caps on message size, recipients, line length, multipart nesting, and store count.
//   - Defended: Slowloris connections via per-command read timeouts.
//   - Not Defended: Authentication on SMTP and HTTP endpoints (intended strictly for development and localhost testing).
//   - Not Defended: TLS and STARTTLS transport encryption.
package smtpmailcatcher

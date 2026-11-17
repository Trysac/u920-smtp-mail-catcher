# Use standard library only with sandboxed iframe and CSP for captured email rendering

Status: accepted (generated, pending human review)

## Context

Applications send development emails with arbitrary HTML, attachments, and scripts. We require an embeddable, zero-dependency SMTP sink in Go that allows viewing captured messages safely while preventing XSS and memory exhaustion.

## Decision

Implement the SMTP server using Go's standard library (net, net/mail, mime, html/template). Store messages in a bounded FIFO ring buffer. Display HTML email bodies through an iframe sandbox attribute alongside Content-Security-Policy headers.

## Consequences

Zero external dependencies ensures rapid build and test times with no supply-chain overhead. Security is maintained through browser isolation rather than error-prone HTML sanitisation.

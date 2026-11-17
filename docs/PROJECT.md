# Project record

## Objective

Development SMTP server that captures every message, parses MIME parts and attachments and shows them in a safe local web interface.

## Scope

- SMTP server: RFC 5321 subset, never relays, accepts any recipient.
- MIME: Recursive multipart decoding, attachment extraction.
- Storage: Bounded in-memory ring buffer.
- Interface: JSON API and HTML inspection UI.

## Out of Scope

- SMTP relaying, STARTTLS, AUTH, persistent storage across restarts, message search, HTML sanitizing (isolation via sandbox is used instead).

## Effort metrics

| Metric | Value |
|---|---|
| Tokens (input / output) | 8620058 / 56099 |
| Hours (human) | pending measurement |
| Cost (USD) | 1.9901 |

## Factory run u920 (measured from API usage)

| Date (UTC) | Phase | Model | Batch | Input | Output | Cache write | Cache read | Cost (USD) |
|---|---|---|---|---|---|---|---|---|
| 2026-11-09 | generation | google/gemini-3.8-flash | yes | 5488 | 22901 | 0 | 0 | 0.0900 |
| 2026-11-09 | fix-1 | google/gemini-3.1-flash-lite | no | 129653 | 2077 | 0 | 260642 | 0.0420 |
| 2026-11-09 | fix-2 | google/gemini-3.1-flash-lite | no | 49999 | 1283 | 0 | 267666 | 0.0211 |
| 2026-11-09 | fix-3 | google/gemini-3.1-flash-lite | no | 20983 | 495 | 0 | 0 | 0.0060 |
| 2026-11-09 | fix-4 | google/gemini-3.1-flash-lite | no | 20983 | 50 | 0 | 0 | 0.0053 |
| 2026-11-09 | fix-1 | google/gemini-3.8-flash | no | 258531 | 2883 | 0 | 576245 | 0.2479 |
| 2026-11-09 | fix-2 | google/gemini-3.8-flash | no | 268965 | 2431 | 0 | 608088 | 0.2564 |
| 2026-11-09 | fix-3 | google/gemini-3.8-flash | no | 202177 | 3132 | 0 | 641037 | 0.2115 |
| 2026-11-09 | fix-4 | google/gemini-3.8-flash | no | 246415 | 2343 | 0 | 593857 | 0.2381 |
| 2026-11-17 | fix-1 | google/gemini-3.8-flash | no | 139395 | 1085 | 0 | 726521 | 0.1631 |
| 2026-11-17 | fix-2 | google/gemini-3.8-flash | no | 117857 | 1688 | 0 | 752754 | 0.1512 |
| 2026-11-17 | fix-3 | google/gemini-3.8-flash | no | 134212 | 1563 | 0 | 844883 | 0.1699 |
| 2026-11-17 | fix-4 | google/gemini-3.8-flash | no | 94250 | 10548 | 0 | 727755 | 0.1648 |
| 2026-11-17 | audit | google/gemini-3.8-flash | no | 133746 | 898 | 0 | 519776 | 0.1427 |
| 2026-11-17 | threat-model | google/gemini-3.8-flash | no | 78435 | 583 | 0 | 194888 | 0.0756 |
| 2026-11-17 | docs | google/gemini-3.1-flash-lite | no | 4857 | 2139 | 0 | 0 | 0.0044 |

Total: $1.9901. Prices: vertex line, as measured by its adapter. Providers serve the current version of each model id, so the date tells which version answered; versions, launch stages and price sources per line are in the factory's model register (docs/MODELS.md).

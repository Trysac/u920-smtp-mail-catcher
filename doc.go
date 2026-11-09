// Package smtpmailcatcher is a small, dependency-free Go library: Development SMTP server that captures every message, parses MIME parts and attachments and shows them in a safe local web interface
//
// The public API is every exported identifier of this root package. Functions are pure: they do
// no I/O, keep no global mutable state and report invalid input through returned errors.
package smtpmailcatcher

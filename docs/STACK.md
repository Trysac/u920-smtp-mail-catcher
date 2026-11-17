# Stack

## Used

| Tool | Why |
|---|---|
| Go >= 1.23 | Standard library only; module dependencies are zero. |
| Go modules | Built-in versioning. |
| go test | Full test suite, examples, race detection, and coverage. |
| go vet | Static analysis. |
| govulncheck | Vulnerability scanning. |

## Discarded

| Tool | Why not |
|---|---|
| golangci-lint | `go vet` provides sufficient checks for this size. |
| testify | Keep module dependency-free; use standard `testing`. |
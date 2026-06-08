# Contributing to MuxCore

Thank you for contributing. This document covers the development setup, code conventions, and PR process.

---

## Development Setup

### Prerequisites

- Go 1.26.x ([download](https://go.dev/dl/))
- Docker + docker-compose (for `make dev`)
- golangci-lint ([install](https://golangci-lint.run/usage/install/)) — optional but recommended
- protoc + protoc-gen-go + protoc-gen-go-grpc — only needed if editing `.proto` files

### Clone and build

```bash
git clone https://github.com/Muxcore-Media/core.git
cd core
make build           # produces ./muxcored
```

### Configure

```bash
cp .env.example .env
# Edit .env with your local settings
```

For local development without TLS:

```bash
MUXCORE_INSECURE_DISABLE_TLS=true ./muxcored
```

### Install pre-commit hooks

```bash
git config core.hooksPath .githooks
```

The hook runs `gofmt`, `go vet`, gitleaks secrets scan, and tests on changed files before every commit.

---

## Running Tests

```bash
make test          # run all core tests with race detection
make coverage      # generate coverage.html
```

Tests must not depend on external services (database, Redis, etc.). Use mocks from `sdk/go/mock/`.

---

## Linting

```bash
make lint          # golangci-lint if installed, else go vet
```

The CI pipeline runs golangci-lint with the config in `.golangci.yml`. Fix all lint errors before opening a PR.

---

## Code Conventions

- **No comments explaining what the code does** — name things well instead.
- **Comments only for non-obvious WHY** — hidden invariants, workarounds, CWE references.
- **No test mocks for core internals** — use real implementations. Integration-style tests are fine.
- **No `os.Exit` from library code** — only `main` exits.
- **Structured logging via `log/slog`** — no `fmt.Println` or `log.Printf` in non-test code.
- **Context propagation** — every function that does I/O takes `ctx context.Context` as its first argument.
- **Error wrapping** — use `fmt.Errorf("operation %q: %w", name, err)` — never discard errors silently.

---

## Protobuf

If you add or modify `.proto` files, regenerate Go code:

```bash
make proto
```

Regenerated files in `proto/gen/` must be committed alongside the `.proto` changes.

---

## Branch Naming

```
feat/<short-description>      # new feature
fix/<short-description>       # bug fix
docs/<short-description>      # documentation only
refactor/<short-description>  # no behaviour change
test/<short-description>      # tests only
```

---

## Pull Request Process

1. Branch from `master`.
2. Make your changes with tests.
3. Run `make ci` locally — it must pass.
4. Update `core.wiki/` pages and `docs/` if your change affects user-facing behaviour, configuration, or architecture.
5. Update `SECURITY.md` if your change has security implications.
6. Open a PR against `master`.
7. Address review comments.
8. Squash-merge is preferred for small PRs; regular merge for large feature branches.

---

## Security Vulnerabilities

Do **not** open a public issue for security vulnerabilities. See [SECURITY.md](SECURITY.md) for the private reporting process.

---

## License

By contributing, you agree that your contributions will be licensed under the GPL-3.0 license.

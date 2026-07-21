# Contributing to TidyMyMac

Thanks for helping make TidyMyMac better! This guide covers the conventions the
project follows so contributions stay consistent. Read
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) first — it's the source of truth
for how the code is organized.

## Golden rule: safety first

TidyMyMac deletes user files. The entire project is built around one invariant:

> **Files are never touched without explicit user confirmation.**

Every change must preserve it:

- `Scan` has **no side effects** — it only reads and reports. It never deletes,
  moves, or writes.
- Dry-run is the **default**. Deletion happens only with `--execute`.
- `Clean` receives only the `[]FileEntry` the user reviewed and confirmed.
- Long-running scans and cleans honor `ctx.Done()`.

If a change could weaken any of these, it will not be merged.

## Getting started

```bash
git clone https://github.com/viniciussouzao/tidymymac
cd tidymymac
make build      # compile to bin/tidymymac
make run        # build and launch the TUI
```

Requires **Go 1.26+**.

## Project layout

| Path | What lives there |
|---|---|
| `cmd/` | Cobra CLI commands (thin adapters) |
| `internal/cleaner/` | Core domain: `Cleaner` interface, registry, implementations |
| `internal/commands/` | Reusable scan/clean orchestration shared by CLI and TUI |
| `internal/tui/` | BubbleTea TUI (Elm architecture) + `styles/` |
| `internal/history`, `explain`, `scriptgen`, `buildinfo` | Supporting packages |
| `pkg/utils/` | Public disk/format helpers |

## Coding standards

- **Formatting**: run `gofmt`/`goimports`. CI and the linter expect formatted code.
- **Linting**: run `golangci-lint run ./...` (config in `.golangci.yml`) and make
  sure your change does **not introduce new issues** — the repo still carries some
  pre-existing lint debt being paid down incrementally. Auto-fix trivial issues
  with `golangci-lint run --fix ./...`.
- **Vet**: `go vet ./...` must pass.
- **Exported symbols** (types, functions, constructors) must have GoDoc comments.
- **Errors are explicit** — never silently ignore an error. A failure on a single
  file is non-fatal: collect it into the result and continue, don't abort the batch.
- **No manual goroutines/channels in the TUI** — use `tea.Cmd` and handle results
  in `Update`.
- **Styling** goes through `internal/tui/styles` — never hardcode hex colors in
  screens or CLI output. Add to `styles.go` if something is missing.

## Adding a new cleaner

Adding a category is purely additive (full walkthrough in
[ARCHITECTURE.md → Extending TidyMyMac](docs/ARCHITECTURE.md#extending-tidymymac)):

1. Implement the `Cleaner` interface in `internal/cleaner/<name>.go`.
2. Add the `Category` constant and its `DisplayName()` case in `category.go`.
3. Register it in `DefaultRegistry()`.
4. Add it to the cleaner table in `README.md`.

Cleaners that shell out (docker, brew, tmutil) must **degrade gracefully** to an
empty `ScanResult` when the tool is absent.

## Testing

```bash
make test        # go test ./... -race -short
```

- Tests are **co-located** as `*_test.go`.
- Prefer **table-driven tests** with named cases.
- Use `t.TempDir()` and synthetic file trees — never touch the real user
  filesystem or require `sudo`.
- Cover **both** dry-run and execute paths, plus context cancellation and
  partial-failure cases.
- Assert on data, not on styled/colored output strings.

## Git & branch conventions

- Base branch is **`main`**.
- Branch names use a type prefix:
  - `feat/<short-desc>` — new feature or cleaner
  - `fix/<short-desc>` — bug fix
  - `ci/<short-desc>` — CI/workflow changes
  - `docs/<short-desc>` — documentation
- Commit messages: short, imperative present tense (e.g. `add downloads cleaner`).

## Pull requests

PRs follow a structured template so reviewers can quickly assess scope and safety.
Match the format of recent merged PRs — the sections are:

1. `# Title` — Title Case, matches the PR title
2. `## 🎯 Overview` — what changed, why, and which layer it affects
3. `## ✨ What's New` (features) or `## 🐛 What's Fixed` (fixes) — numbered
   subsections as `**Component** (`path`)` with bullet details
4. `### 🔄 Workflow Integration` — how it flows through
   Dashboard → Scanning → Review → Confirm/Execute → Summary
5. `## 🛠️ Technical Details` → `### 📁 Files Changed (N files, +X/-Y)`
   (New Files / Modified Files with per-file line counts), Dependencies, Code Quality
6. `## 🛡️ Safety Checklist` — the five safety guarantees
7. `## 🧪 Testing` — build, `go test ./...`, `go vet`, lint, coverage notes
8. `## ✅ Checklist` — the standard task list

**Before opening a PR**, confirm locally:

```bash
gofmt -l .                    # should print nothing
golangci-lint run ./...       # should be clean
go vet ./...
go build ./...
make test
```

Be honest in the checklists — if a check wasn't run or an item isn't met, mark it
`❌` with a one-line reason rather than checking it off.

## License

By contributing you agree your contributions are licensed under the project's
MIT License.

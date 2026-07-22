# Partial OS Information Rendering Design

## Goal

Render partially available macOS information without empty spaces or empty
parentheses while preserving the current output for complete information.

## Scope

The change is limited to the machine health panel in
`internal/tui/screens/health.go`. It does not change system information
collection, parsing, panel layout, or the other code-review findings.

## Design

Add an unexported `formatOSInfo` helper that receives a `sysinfo.Info` value and
returns the display value that follows the `OS:` label.

The helper joins the available OS name and version with a single space. It adds
the build in parentheses only when the build is available. If the build is the
only available field, the result is the parenthesized build. If all three fields
are empty, the health renderer keeps using the existing dimmed `unknown` value.

Expected output:

| Available fields | Rendered line |
| --- | --- |
| Name, version, build | `OS: macOS Sequoia 15.1 (24B83)` |
| Name, version | `OS: macOS Sequoia 15.1` |
| Version, build | `OS: 15.1 (24B83)` |
| Build only | `OS: (24B83)` |
| None | `OS: unknown` |

`renderHealthBlock` will use the helper when `OSVersionKnown` is true. The
helper will also defensively return an empty string for an inconsistent known
state with no populated fields; the renderer will then fall back to `unknown`.

## Testing

Add table-driven unit tests in `internal/tui/screens/health_test.go` for complete
and partial combinations, including the defensive empty case. Assertions will
target the plain formatting helper so ANSI styling does not make the tests
brittle, in accordance with the repository testing guidelines.

Run formatting, the focused screen tests, the complete race-enabled test suite,
`go vet`, `golangci-lint`, and `go build` after implementation.

# TidyMyMac – GitHub Copilot Instructions

These instructions guide GitHub Copilot when generating code for this repository.

The goal is to ensure generated code follows the architecture, style, and design decisions of the TidyMyMac project.

---

# Project Overview

TidyMyMac is a terminal-based macOS storage cleanup tool.

The goal of the project is to help users understand and reclaim disk space from their Mac by identifying and safely removing unnecessary files such as:

- application caches
- logs
- temporary files
- docker artifacts
- Xcode derived data
- incomplete downloads
- large user files
- trash

The tool is inspired by CleanMyMac but designed as an open-source developer-friendly CLI/TUI.

Primary goals:

- transparency
- safety
- explainability
- modular architecture

The tool should **never delete files without explicit user confirmation**.

---

# Technology Stack

Language:
- Go

CLI Framework:
- cobra

TUI Framework:
- bubbletea (Charmbracelet ecosystem)

UI libraries:
- lipgloss
- bubbles

JSON handling:
- Go standard library

Logging:
- structured logging when appropriate

Prefer Go standard library whenever possible.

---

# Architecture

The project follows a modular architecture.

Main components:

scanner → analyzer → cleaner → tui

Responsibilities:

scanner:
    discovers candidate files for cleanup

analyzer:
    calculates disk usage and classifies items

cleaner:
    performs deletion of files after confirmation

tui:
    interactive interface for selection, review, execution and results

Each component must remain independent.

Avoid mixing responsibilities across modules.

---

# Core Features

Main features expected in the project:

- category-based cleanup
- review screen before deletion
- execution screen with progress
- summary screen after cleanup
- explain "System Data" usage on macOS
- persistent cleanup history

Example command:

tidymymac explain system-data

---

# Coding Style

Follow Go best practices.

General rules:

- small functions
- clear naming
- explicit error handling
- minimal global state
- readable code preferred over clever code

Avoid overly complex abstractions.

---

# Error Handling

Errors must always be handled explicitly.

Preferred pattern:
```
result, err := something()
if err != nil {
return err
}
```

Never silently ignore errors.

---

# Safety Rules

Safety is critical.

Never generate code that:

- deletes files automatically without confirmation
- deletes system files
- runs destructive commands without preview

The workflow must always follow:

scan → review → confirm → execute

---

# TUI Design Guidelines

Use BubbleTea idiomatic architecture.

Prefer the model/update/view pattern.

Screens expected:

1. cleaner selection
2. review items
3. execution progress
4. result summary

UI must remain simple and readable.

---

# Performance

The tool must handle large directories efficiently.

Prefer:

- goroutines
- worker pools
- streaming filesystem scans

Avoid loading large datasets entirely into memory.

---

# Filesystem Handling

Use safe filesystem operations.

Prefer:

- os package
- filepath package
- fs abstractions

Paths must always be cross-compatible with macOS.

---

# Persistent History

Cleanup history must be stored in:

~/.tidymymac/history.json

History must track:

- total runs
- total reclaimed bytes
- timestamp
- cleaners used

This data is used for summary statistics.

---

# Code Generation Guidelines

When generating new code:

1. follow the modular architecture
2. keep code readable
3. avoid unnecessary dependencies
4. prefer composable functions
5. document exported functions

---

# Testing

Generated code should be testable.

Prefer:

- dependency injection
- pure functions
- minimal side effects

---

# Documentation

Public functions must include GoDoc comments.

Example:

```go
// ScanCaches scans user cache directories and returns files that can be cleaned.
func ScanCaches() ([]FileCandidate, error) {
    // implementation
}
```

What Copilot Should Avoid

Avoid generating:
	•	GUI frameworks
	•	non-Go implementations
	•	macOS-specific shell scripts when Go can handle it
	•	unsafe deletion logic

⸻

Design Philosophy

TidyMyMac prioritizes:
	•	safety
	•	transparency
	•	developer friendliness
	•	predictable behavior

The tool should feel like a trusted maintenance utility, not a black-box cleaner.
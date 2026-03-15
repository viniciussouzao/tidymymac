# AGENTS.md

Guidelines for AI coding agents (GitHub Copilot, Copilot Chat, and other AI assistants)
working on the **TidyMyMac** project.

This document defines how agents should understand the project, generate code,
and maintain architectural consistency.

---

# Project Overview

TidyMyMac is an open-source macOS storage cleanup utility designed for developers.

It helps users understand and reclaim disk space by identifying and safely removing
unnecessary files such as:

- application caches
- logs
- temporary files
- docker artifacts
- Xcode derived data
- incomplete downloads
- large files
- trash

The project is inspired by CleanMyMac but designed as:

- transparent
- safe
- developer-friendly
- terminal-first

TidyMyMac provides both:

- CLI commands
- interactive TUI interface

---

# Core Design Principles

Agents must follow these principles when generating code:

1. Safety first
2. Never delete files without explicit user confirmation
3. Clear and predictable behavior
4. Transparent analysis of disk usage
5. Modular architecture

TidyMyMac must never behave like a "black-box cleaner".

Users must always understand what will be removed.

---

# Technology Stack

Language:
Go

CLI Framework:
Cobra

TUI Framework:
BubbleTea

Charmbracelet ecosystem:
- bubbletea
- lipgloss
- bubbles

Standard libraries should be preferred whenever possible.

Avoid unnecessary dependencies.

---

# Architecture

The system follows a modular architecture.

scanner → analyzer → cleaner → tui

## Scanner

Discovers potential cleanup candidates.

Examples:

- cache scanner
- docker scanner
- xcode scanner
- logs scanner
- temp file scanner

Scanners must **never delete files**.

They only identify candidates.

---

## Analyzer

Analyzes disk usage and categorizes items.

Responsibilities:

- calculate size
- classify files
- prepare data for UI display

---

## Cleaner

Responsible for deleting files.

Cleaners must:

- receive confirmed targets
- perform safe deletion
- report reclaimed space

Cleaners must never operate without confirmation.

---

## TUI

Interactive terminal interface.

Built using BubbleTea.

Expected screens:

1. cleaner selection
2. review items
3. execution progress
4. summary

---

# Explain System Data Feature

TidyMyMac provides a command:
```
tidymymac explain system-data
```

This feature analyzes directories that commonly contribute
to macOS "System Data".

Examples include:

- ~/Library/Caches
- ~/Library/Logs
- ~/Library/Application Support
- ~/Library/Developer
- ~/Library/Containers
- /private/var/tmp
- /private/var/log

The goal is to produce a transparent breakdown of disk usage.

---

# Persistent History

Cleanup history must be stored at:
```
~/.tidymymac/history.json
```

The file tracks:

- number of runs
- total reclaimed space
- timestamp of runs
- cleaners executed

This data is used for statistics and summaries.

---

# Expected CLI Commands

Examples:
```
tidymymac
tidymymac scan
tidymymac clean
tidymymac explain system-data
tidymymac stats
tidymymac history
```

The CLI should integrate with the TUI.

Running `tidymymac` without arguments should launch the TUI.

---

# Safety Rules

Agents must never generate code that:

- deletes files automatically
- deletes system directories
- runs destructive commands without preview
- modifies user files outside the cleanup scope

The workflow must always follow:

scan → review → confirm → execute

---

# Coding Standards

Follow idiomatic Go.

Key rules:

- small functions
- clear naming
- explicit error handling
- minimal global state

Example error handling:

```go
result, err := ScanCaches()
if err != nil {
    return err
}
```

Never ignore errors.

Performance Guidelines

Filesystem scanning must be efficient.

Prefer:
	•	goroutines
	•	worker pools
	•	streaming file traversal

Avoid loading entire directory trees into memory.

⸻

TUI Guidelines

Follow BubbleTea architecture:

Model
Update
View

UI should prioritize:
	•	clarity
	•	simplicity
	•	predictable navigation

Avoid overly complex UI behavior.

⸻

Code Organization

Preferred structure:
```
cmd/tidymymac

internal/
  scanner/  
  cleaner/
  tui/
  history/
pkg/
```

Internal packages should not expose unnecessary APIs.

⸻

Testing Guidelines

Code should be testable.

Prefer:
	•	dependency injection
	•	pure functions
	•	minimal side effects

Avoid tightly coupled components.

⸻

Documentation

Public functions must include GoDoc comments.

Example:
```
// ScanCaches scans user cache directories and returns removable files.
func ScanCaches() ([]FileCandidate, error)
```

What Agents Should Avoid

Avoid generating:
	•	GUI frameworks
	•	shell scripts when Go is sufficient
	•	destructive filesystem logic
	•	overly complex abstractions

⸻

Design Philosophy

TidyMyMac should feel like a trusted system maintenance tool.

Key values:
	•	transparency
	•	safety
	•	performance
	•	simplicity
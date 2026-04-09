# TidyMyMac — Troubleshooting

Common issues encountered while running TidyMyMac and how to fix them.

---

## Table of Contents

- [Trash (or other categories) reports 0 bytes even though it is not empty](#trash-or-other-categories-reports-0-bytes-even-though-it-is-not-empty)

---

## Trash (or other categories) reports 0 bytes even though it is not empty

### Symptom

You run `tidymymac` (or `tidymymac scan`) and a category that you *know* has files to clean — most commonly **Trash**, but also **iOS Backups**, **Application Caches**, **System Logs**, or **Time Machine Snapshots** — shows up as `0 B` / `0 files`:

```
Trash Files          0 files        0 B
iOS Backups          0 files        0 B
```

Meanwhile, Finder clearly shows items in the Trash, and `du -sh ~/.Trash` from the same terminal may also return `0B` or permission errors.

### Cause

TidyMyMac reads these paths directly from disk. On modern macOS, several user directories are protected by **TCC (Transparency, Consent, and Control)** and are invisible to any process whose parent terminal does not have **Full Disk Access**. Paths commonly affected include:

- `~/.Trash` and per-volume `.Trashes`
- `~/Library/Application Support/MobileSync/Backup` (iOS backups)
- Parts of `~/Library/Caches` and `~/Library/Logs`
- Local Time Machine snapshot metadata exposed via `tmutil`

When Full Disk Access is missing, macOS silently returns an empty listing rather than a permission error — so the scan completes "successfully" with a size of `0 B`. This is a macOS privacy behavior, not a bug in TidyMyMac.

The key detail: **the permission is granted to the terminal application that launches TidyMyMac**, not to the `tidymymac` binary itself. If you run TidyMyMac from iTerm2, iTerm2 needs Full Disk Access. If you run it from the built-in Terminal, Terminal needs it. The same applies to Ghostty, Warp, Alacritty, Kitty, WezTerm, VS Code's integrated terminal, JetBrains IDEs' terminals, and any other host.

### Fix

Grant Full Disk Access to your terminal application:

1. Open **System Settings** → **Privacy & Security** → **Full Disk Access**.
2. Click the **+** button (you may be asked to authenticate).
3. Navigate to `/Applications` (or wherever your terminal lives) and add your terminal app — e.g. **iTerm**, **Terminal**, **Ghostty**, **Warp**, **Alacritty**, **Kitty**, **WezTerm**, **Visual Studio Code**, your JetBrains IDE, etc.
4. Make sure the toggle next to the terminal is **on**.
5. **Fully quit and relaunch the terminal** — not just close the window. macOS only picks up the new permission when the process is restarted. On iTerm2 / Terminal / most apps: `Cmd+Q`. For IDE-hosted terminals, quit the whole IDE.
6. Run `tidymymac scan` again. The previously-empty categories should now report their real size.

### Verifying the fix

A quick, side-effect-free check before re-running the full scan:

```bash
# Should list your actual trashed files, not return "Operation not permitted"
ls -la ~/.Trash

# Should return a non-zero size if you have trashed items
du -sh ~/.Trash
```

If these commands still return `0B` or permission errors, the terminal you're running them from does not yet have Full Disk Access — revisit step 5 (full quit and relaunch) before opening an issue.

### Why TidyMyMac does not prompt for this automatically

TidyMyMac is a CLI and cannot request Full Disk Access on its own: on macOS, TCC prompts are tied to GUI application bundles, not to individual binaries run from a shell. Requesting the permission would require shipping a `.app` wrapper, which is explicitly outside the V1 scope. The trade-off is documented here instead so users who hit the empty-scan footgun have a clear path forward.

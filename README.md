# 🧹 TidyMyMac

An open-source macOS storage cleanup utility for developers. Scan, review, and reclaim disk space — safely, transparently, and from the terminal.

> Inspired by CleanMyMac, but open-source and built for developers who want to know exactly what gets deleted.

## ✨ Features

- 🖥️ Interactive TUI to browse and select what to clean
- 🛡️ Dry-run by default — nothing is deleted without your explicit confirmation
- 🔌 Modular cleaners for different categories of junk
- 📊 Progress reporting and summary of reclaimed space

### 🗂️ Cleaners

| Category | What it targets |
|---|---|
| 🗑️ Temporary Files | `/tmp`, `/var/tmp`, user temp directories |
| 📦 Application Caches | `~/Library/Caches` |
| 📋 System Logs | `~/Library/Logs`, `/Library/Logs`, `/var/log` |
| 🍺 Homebrew Cache | Packages cached by `brew` |
| 🐳 Docker Artifacts | Stopped containers, untagged images, orphaned volumes |
| 📱 iOS Backups | iPhone/iPad backups in `~/Library/Application Support/MobileSync/Backup` |

## 🚀 Installation

```bash
# Clone and build
git clone https://github.com/viniciussouzao/tidymymac
cd tidymymac
make build

# Run
./bin/tidymymac
```

> Requires Go 1.21+

## 🛠️ Usage

```bash
# Launch interactive TUI (dry-run, nothing is deleted)
tidymymac

# Actually delete the selected files
tidymymac --execute
```

### 📋 Subcommands

```bash
tidymymac scan      # Scan for junk without launching the TUI
tidymymac clean     # Clean without the TUI (non-interactive)
tidymymac explain   # Explain what each category contains
tidymymac history   # Show past cleanup runs and reclaimed space
```

## 🏗️ Development

```bash
make build   # Compile binary to bin/tidymymac
make test    # Run tests with race detection
make run     # Build and run
make clean   # Remove build artifacts
```

## 🔒 Safety

TidyMyMac is designed with safety as the primary concern:

- ✅ **Dry-run by default**: scanning and reviewing never touches your files
- ✅ **Explicit confirmation required**: deletion only happens with `--execute`
- ✅ **No silent operations**: every file is shown before removal
- ✅ **Errors are non-fatal**: a failure on one file won't stop the rest

## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.
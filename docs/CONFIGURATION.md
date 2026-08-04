# TidyMyMac — Configuration

Everything TidyMyMac reads from `~/.tidymymac/config.yaml`: paths that can never be deleted,
categories disabled by default, and named cleanup profiles.

---

## Table of Contents

- [The config file](#the-config-file)
- [`protected_paths` — the hard block](#protected_paths--the-hard-block)
- [`disabled_categories` — a softer default](#disabled_categories--a-softer-default)
- [`profiles` — named cleanup bundles](#profiles--named-cleanup-bundles)
- [Full example](#full-example)
- [Editing the file by hand](#editing-the-file-by-hand)

---

## The config file

TidyMyMac looks for `~/.tidymymac/config.yaml`. The file is **optional**: if it is missing or
empty, nothing is protected, nothing is disabled and no profiles exist — every command behaves
as if you had never created it.

If the file *is* present but cannot be understood — invalid YAML, an unrecognized key, an
invalid `protected_paths` entry — TidyMyMac **aborts the command** instead of continuing:

```
Error: loading config: config file /Users/you/.tidymymac/config.yaml: unrecognized or malformed content: ...
```

That refusal is deliberate. Falling back to an empty config would mean running with **zero
protection** while you believe `protected_paths` is active, which is the worst possible failure
mode for a safety feature. A typo like `protected_path` (singular) is an error, never a silent
no-op.

The three top-level keys are independent — use any subset:

| Key | Purpose | Edited by |
|---|---|---|
| `protected_paths` | Paths no cleaner may ever delete | `tidymymac protect` / `unprotect` |
| `disabled_categories` | Categories skipped by default | by hand |
| `profiles` | Named bundles of categories + project paths | `tidymymac profile ...` |

---

## `protected_paths` — the hard block

A list of paths that **no cleaner, category, or `--execute` run can ever delete**, regardless
of CLI flags. There is no override flag by design.

```bash
tidymymac protect --path ~/Documents/Work     # add
tidymymac unprotect --path ~/Documents/Work   # remove
tidymymac list protected                      # show current config
```

```yaml
protected_paths:
  - ~/Documents/Work
  - /Users/you/Sites/client-project
```

Protected files are still **shown** by `scan` and by dry-run previews — protection hides
nothing, it only prevents deletion.

### How paths are matched

You do not have to write a path exactly the way a cleaner happens to report it:

- **`~` is expanded** to your home directory.
- **Comparison is case-insensitive**, because APFS is case-insensitive-but-preserving by
  default. Over-protecting is the safer error.
- **macOS firmlinks are aliased both ways**: protecting `/tmp/x` also protects
  `/private/tmp/x`, and vice versa. Same for `/var` and `/etc`.
- **Symlinks are resolved**: if the protected path itself is a symlink, its target is
  protected too.
- **A path that does not exist yet is valid** — protect it now, and it stays protected when
  something creates it later.

### Containment works in both directions

Protecting a directory protects everything under it. Less obvious, and just as important:
protecting something *inside* a directory also protects **that whole directory from being
deleted as a unit**.

Some cleaners record a directory as a single entry without descending into it — a
`node_modules` folder found by a profile, or an oversized folder in `~/Downloads`. Deleting
such an entry would take everything inside it, so if a protected path lives anywhere within,
the entire entry is marked protected and skipped:

```bash
tidymymac protect --path ~/proj/node_modules/my-local-patch
# => the whole ~/proj/node_modules entry is now skipped, not just the patch
```

### What happens when a protected path is found

Two different outcomes, depending on the cleaner:

- **Most cleaners** delete file by file, so the protected entries are simply dropped and the
  rest of the category is cleaned normally.
- **Whole-domain cleaners** — Homebrew, Development Artifacts and Trash — do not delete entry
  by entry. They shell out to a command (`brew cleanup`, `go clean -cache -modcache`, and for
  Trash without Full Disk Access, Finder's "empty trash") that clears their entire domain in
  one shot, with no way to spare a single path. When a protected path is found in one of
  these, the **whole category is skipped** with an explanatory error:

  ```
  - Homebrew Cache: error: skipped: 1 protected path(s) found in this category,
    and Homebrew Cache cannot selectively clean around them
  ```

  This is intentional: skipping the category is the only honest way to honor the protection.

Protection is also applied to `scan --generate-script`: a protected path never makes it into a
generated deletion script, since you run that script later, unsupervised.

---

## `disabled_categories` — a softer default

Categories listed here are skipped when you run `scan` or `clean` **without naming
categories**:

```yaml
disabled_categories:
  - docker
  - trash
```

Unlike `protected_paths`, this is a default, not a block. It is overridden by:

- naming the category explicitly — `tidymymac clean docker` still cleans Docker;
- a profile that lists it — `--profile` uses exactly the categories the profile names.

If *every* category is disabled, running without arguments is an error rather than a silent
no-op:

```
Error: all categories are disabled by config; pass explicit categories to override
```

There is no CLI command to edit this list today — add or remove entries by hand.
Category IDs come from `tidymymac list categories`.

---

## `profiles` — named cleanup bundles

A profile is a named bundle of two things you clean together:

1. **Categories** — any of the built-in cleaner categories, run as-is.
2. **Project paths** — directories swept by the **project-artifacts** cleaner, which reports
   regenerable junk directories (`node_modules`, `dist`, `build`, `out`, `.next`, `.nuxt`,
   `.turbo`, `.parcel-cache`, `coverage`, `__pycache__`, `.pytest_cache`, `.venv`, `venv`,
   `target`, `.gradle`, `.terraform`, `.cache`) and individual files over 500MB.

```yaml
profiles:
  dev:
    categories: [development-artifacts, docker]
    paths:
      - ~/projects/my-app
```

### Managing profiles

```bash
tidymymac profile create dev
tidymymac profile add-category dev development-artifacts
tidymymac profile add-path dev ~/projects/my-app

tidymymac list profiles

tidymymac profile remove-category dev development-artifacts
tidymymac profile remove-path dev ~/projects/my-app
tidymymac profile delete dev
```

`add-category` rejects a category that does not exist, so a typo is caught immediately rather
than on your next scan. Adding something already listed is a no-op that does not even rewrite
the file.

### Running a profile

```bash
tidymymac scan --profile dev
tidymymac clean --profile dev                        # dry-run preview
tidymymac clean --profile dev --execute              # actually delete
tidymymac clean --profile dev --include-large-files --execute
```

Four behaviors worth knowing:

- **`--profile` cannot be combined with positional categories.** Merging them would be
  ambiguous, so it is a hard error that tells you to either add the category to the profile or
  run it separately.
- **An empty profile is an error.** An empty category list means "every category" everywhere
  else in the CLI, which is the opposite of what a freshly created, still-empty profile should
  do — so it refuses rather than cleaning everything.
- **Project paths must be actual projects.** The home directory itself, `/`, `/Users`,
  `/System`, `/Library`, `/Applications`, `/private` and `/Volumes` are rejected — a typo like
  `paths: ["~"]` would otherwise turn a profile into a full-home junk sweep. The check runs
  both when you add a path and when a profile is used, so a hand-edited entry is caught too
  (and only that profile fails — the rest of the CLI keeps working).
- **Oversized files are opt-in for deletion.** `scan` always reports them, and `clean` shows
  them in the preview, but they are only deleted with `--include-large-files`. A
  `node_modules` directory is regenerable by definition; a 500MB file may be a dataset, a VM
  image or an export that nothing regenerates. Junk directories are always deleted.

The project-artifacts cleaner never descends into a junk directory it already matched, and
never touches `.git`, `.hg` or `.svn` — a junk-dir name inside a repository's internals is
repository data, not build output.

Without a profile, `project-artifacts` has no paths to scan and reports nothing. That is
expected, not a failure: the category exists to be pointed somewhere by a profile.

---

## Full example

```yaml
# Paths no cleaner may ever delete. A hard block: no CLI flag overrides it.
# Managed with: tidymymac protect --path <path> / tidymymac unprotect --path <path>
protected_paths:
  - ~/Documents/Work
  - ~/projects/my-app/node_modules/vendored-patch

# Skipped when running scan/clean without naming categories.
# Naming a category explicitly (or using a profile) still runs it.
disabled_categories:
  - trash

# Named bundles: tidymymac scan --profile dev
profiles:
  dev:
    categories: [development-artifacts, docker]
    paths:
      - ~/projects/my-app
      - ~/projects/api

  # A profile may be paths-only; project-artifacts is added automatically.
  side-projects:
    paths:
      - ~/experiments/rust-thing
```

---

## Editing the file by hand

You can — the CLI commands and hand edits produce the same file. Two things to know:

- **Unknown keys are fatal**, as described above. Check `tidymymac list protected` and
  `tidymymac list profiles` after an edit; if the file is broken, every command will say so
  immediately.
- **Your comments survive.** `protect`, `unprotect` and the `profile` commands edit the YAML
  in place rather than regenerating it, so hand-written comments elsewhere in the file are
  preserved. Writes are atomic (temp file + rename) and the result is reloaded once to confirm
  it is still valid, so a crash mid-write cannot leave you with a truncated config.
- **An already-invalid file is not edited.** If the file cannot be loaded, `protect` and the
  `profile` commands refuse to touch it instead of patching around a pre-existing problem —
  fix the file first.

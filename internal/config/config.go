// Package config loads TidyMyMac's persistent safety configuration
// (~/.tidymymac/config.yaml): protected paths that no cleaner may ever
// delete, and categories disabled by default.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/homedir"
)

// Profile is a named, user-authored bundle of cleaner categories plus
// arbitrary project directories to sweep for regenerable junk (see
// cleaner.ProjectArtifactsCleaner). Managed through the "tidymymac profile"
// commands rather than by hand-editing YAML.
type Profile struct {
	Categories []string `yaml:"categories"`
	Paths      []string `yaml:"paths"`
}

// Config holds user-configurable safety settings.
type Config struct {
	ProtectedPaths     []string           `yaml:"protected_paths"`
	DisabledCategories []string           `yaml:"disabled_categories"`
	Profiles           map[string]Profile `yaml:"profiles"`

	normalizedProtected []string
	disabledSet         map[string]struct{}
}

func path() (string, error) {
	dir, err := homedir.AppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// Load reads and validates the config file at ~/.tidymymac/config.yaml.
//
// A missing file is not an error: it yields a zero-value Config (no
// protection, no disabled categories). A malformed file -- invalid YAML, an
// unrecognized key, or an invalid protected_paths entry -- IS an error, and
// callers must abort rather than fall back to an unprotected config: silently
// running as if protected_paths were empty while the user believes it's
// active is the worst possible failure mode for a safety feature.
func Load() (*Config, error) {
	if homedir.IsElevatedWithoutSudoUser() {
		// Resolving the home directory would fall back to root's own home
		// (typically /var/root), which almost certainly has no config file.
		// That would silently yield an empty, unprotected Config while the
		// user believes protected_paths is still active -- refuse instead.
		return nil, fmt.Errorf("running elevated but cannot determine the invoking user's home directory (SUDO_USER is unset or does not resolve to a real user); refusing to load config from root's home, since protected_paths would silently stop applying. Ensure sudo preserves SUDO_USER (avoid \"sudo -H\"), or run without sudo")
	}

	p, err := path()
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}
	return loadFrom(p)
}

// New constructs a Config from explicit fields, running the same
// normalization/validation Load applies to a parsed file. Useful for tests
// and any other in-memory construction of a Config.
func New(protectedPaths, disabledCategories []string) (*Config, error) {
	cfg := &Config{ProtectedPaths: protectedPaths, DisabledCategories: disabledCategories}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadFrom(p string) (*Config, error) {
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", p, err)
	}

	cfg, err := decodeConfig(data)
	if err != nil {
		if errors.Is(err, io.EOF) {
			// Empty (zero-byte or whitespace-only) file: treat like a
			// missing file, not an error.
			return &Config{}, nil
		}
		return nil, fmt.Errorf("config file %s: unrecognized or malformed content: %w", p, err)
	}
	if err := cfg.normalize(); err != nil {
		return nil, fmt.Errorf("config file %s: %w", p, err)
	}
	return cfg, nil
}

// decodeConfig decodes YAML bytes into a Config, hard-failing on any key not
// recognized by the Config struct (KnownFields(true)) -- the direct
// replacement for TOML's meta.Undecoded() check this package used to rely
// on. A typo like "protected_path" (singular) must never silently produce
// zero protection.
func decodeConfig(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// normalize validates and pre-computes the comparison form of every
// protected path entry: tilde-expanded, cleaned, required-absolute, and
// case-folded (APFS is case-insensitive-but-preserving by default, and for a
// hard-block safety feature it's better to over-protect than under-protect).
//
// Profiles are deliberately NOT validated here. A broken profile poses no
// safety risk until it is used, whereas failing Load would block every
// command (root's PersistentPreRunE loads config even for "list categories").
// Profile paths are validated at the two moments that matter instead: write
// time (AddProfilePath) and use time (ResolveProfile).
func (c *Config) normalize() error {
	c.disabledSet = make(map[string]struct{}, len(c.DisabledCategories))
	for _, cat := range c.DisabledCategories {
		c.disabledSet[cat] = struct{}{}
	}

	seen := make(map[string]struct{}, len(c.ProtectedPaths))
	for _, raw := range c.ProtectedPaths {
		clean, err := validateProtectedPathEntry(raw)
		if err != nil {
			return err
		}

		norm := strings.ToLower(clean)
		addRoot(&c.normalizedProtected, seen, norm)

		// macOS mounts several top-level directories as firmlinks to a
		// /private/... path (e.g. /tmp -> /private/tmp, /var -> /private/var).
		// Cleaners walk the shortcut form, so a protected_paths entry written
		// in either form must protect both -- otherwise a path typed in the
		// "wrong" spelling silently fails to match anything scanned.
		for _, alias := range firmlinkAliases(norm) {
			addRoot(&c.normalizedProtected, seen, alias)
		}

		// General symlink case: if the configured path itself resolves to a
		// different location, protect that location too. Best-effort --
		// EvalSymlinks fails for a path that doesn't exist yet, which is a
		// legitimate thing to protect (see Load's doc comment), so an error
		// here is not fatal to Load.
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			addRoot(&c.normalizedProtected, seen, strings.ToLower(resolved))
		}
	}

	return nil
}

func addRoot(roots *[]string, seen map[string]struct{}, root string) {
	if _, dup := seen[root]; dup {
		return
	}
	seen[root] = struct{}{}
	*roots = append(*roots, root)
}

// firmlinkPrefixes are macOS's well-known top-level firmlink shortcuts to
// their /private/... backing path. Cleaners in this codebase walk the
// shortcut form (e.g. "/tmp", "/var/tmp", "/var/log"), never the /private
// form, but a user may reasonably write either spelling in config.
var firmlinkPrefixes = [][2]string{
	{"/tmp", "/private/tmp"},
	{"/var", "/private/var"},
	{"/etc", "/private/etc"},
}

// firmlinkAliases returns the alternate spelling(s) of clean under macOS's
// known firmlink shortcuts, if clean falls under one of them.
func firmlinkAliases(clean string) []string {
	var aliases []string
	for _, pair := range firmlinkPrefixes {
		shortcut, real := pair[0], pair[1]
		switch {
		case clean == shortcut:
			aliases = append(aliases, real)
		case strings.HasPrefix(clean, shortcut+"/"):
			aliases = append(aliases, real+strings.TrimPrefix(clean, shortcut))
		case clean == real:
			aliases = append(aliases, shortcut)
		case strings.HasPrefix(clean, real+"/"):
			aliases = append(aliases, shortcut+strings.TrimPrefix(clean, real))
		}
	}
	return aliases
}

// validateProtectedPathEntry validates a single protected_paths entry the
// same way at load time (normalize's loop) and at write time (write.go's
// AddProtectedPath/RemoveProtectedPath), so the two can't drift. Returns the
// tilde-expanded, cleaned, absolute form -- the caller decides what to store
// (the normalized form for comparison, or the original raw string when
// writing back to disk).
func validateProtectedPathEntry(raw string) (string, error) {
	return validatePathEntry("protected_paths", raw)
}

// validatePathEntry is the shared shape of every path a user can put in
// config: non-empty, tilde-expandable, absolute. field names the setting in
// error messages so protected_paths and profile paths report accurately.
func validatePathEntry(field, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%s contains an empty entry", field)
	}

	expanded, err := expandTilde(raw)
	if err != nil {
		return "", fmt.Errorf("%s entry %q: %w", field, raw, err)
	}

	clean := filepath.Clean(expanded)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("%s entry %q must be an absolute path", field, raw)
	}

	return clean, nil
}

// broadProfileRoots are directories far too broad to be a "project path".
// Scanning one would turn a profile into a full-system junk-dir sweep --
// exactly what a typo like paths: ["~"] would otherwise do.
var broadProfileRoots = []string{
	"/",
	"/Users",
	"/System",
	"/Library",
	"/Applications",
	"/private",
	"/Volumes",
}

// validateProfilePathEntry validates a profile "paths" entry: everything
// validatePathEntry requires, plus a rejection of overly-broad roots. Runs at
// write time (AddProfilePath) and at use time (ResolveProfile), never at load
// time -- see normalize. Returns the tilde-expanded, cleaned, absolute form.
func validateProfilePathEntry(raw string) (string, error) {
	clean, err := validatePathEntry("profile paths", raw)
	if err != nil {
		return "", err
	}

	if isBroadProfileRoot(clean) {
		return "", fmt.Errorf("profile paths entry %q resolves to %s, which is too broad to scan as a project: point it at a specific project directory", raw, clean)
	}

	return clean, nil
}

func isBroadProfileRoot(clean string) bool {
	norm := strings.ToLower(clean)

	for _, root := range broadProfileRoots {
		if norm == strings.ToLower(root) {
			return true
		}
	}

	// The home directory itself: everything the user owns lives under it.
	if home, err := homedir.Resolve(); err == nil {
		if norm == strings.ToLower(filepath.Clean(home)) {
			return true
		}
	}

	return false
}

func expandTilde(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := homedir.Resolve()
	if err != nil {
		return "", fmt.Errorf("expanding ~: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// IsProtected reports whether path is protected, either by an exact match or
// by being nested under a protected directory. nil-safe.
func (c *Config) IsProtected(path string) bool {
	if c == nil {
		return false
	}
	clean := strings.ToLower(filepath.Clean(path))
	for _, root := range c.normalizedProtected {
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// ContainsProtected reports whether a protected path lies strictly *inside*
// path -- IsProtected's containment check, reversed. nil-safe.
//
// Cleaners that record a directory as a single whole unit without descending
// into it (project-artifacts' junk dirs, downloads' folders) need this: such
// an entry is not itself under any protected root, so IsProtected returns
// false, yet deleting it with os.RemoveAll would take the protected path
// nested inside it along too. Only meaningful for directory entries -- see
// Tag.
func (c *Config) ContainsProtected(path string) bool {
	if c == nil {
		return false
	}
	prefix := strings.ToLower(filepath.Clean(path))
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	for _, root := range c.normalizedProtected {
		if strings.HasPrefix(root, prefix) {
			return true
		}
	}
	return false
}

// IsCategoryDisabled reports whether category is listed in
// disabled_categories. nil-safe.
func (c *Config) IsCategoryDisabled(category string) bool {
	if c == nil {
		return false
	}
	_, disabled := c.disabledSet[category]
	return disabled
}

// Tag returns a copy of entries with Protected set on those matching
// protected_paths. It never removes entries: scan output and dry-run
// previews must show protected files, not hide them. nil-safe (returns
// entries unchanged).
//
// A directory entry is also tagged when it *contains* a protected path
// (ContainsProtected): deleting it would remove the protected path with it,
// and a cleaner that records a directory as a whole unit has no way to spare
// just the part inside.
func (c *Config) Tag(entries []cleaner.FileEntry) []cleaner.FileEntry {
	if c == nil || len(c.normalizedProtected) == 0 {
		return entries
	}
	tagged := make([]cleaner.FileEntry, len(entries))
	for i, e := range entries {
		e.Protected = c.IsProtected(e.Path) || (e.IsDir && c.ContainsProtected(e.Path))
		tagged[i] = e
	}
	return tagged
}

// CountProtected returns how many entries are tagged Protected. Callers use
// this to decide whether a cleaner that cannot honor a filtered entry list
// (see cleaner.Cleaner.DeletesWholeDomain) must be skipped entirely rather
// than invoked with a partial list.
func CountProtected(entries []cleaner.FileEntry) int {
	n := 0
	for _, e := range entries {
		if e.Protected {
			n++
		}
	}
	return n
}

// StripProtected drops entries already tagged Protected. This is the hard
// block: it must run immediately before every Cleaner.Clean invocation and
// before any generated deletion script, unconditionally -- protected_paths is
// never overridable via CLI flags.
func StripProtected(entries []cleaner.FileEntry) []cleaner.FileEntry {
	kept := make([]cleaner.FileEntry, 0, len(entries))
	for _, e := range entries {
		if e.Protected {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// ResolveProfile turns profile name into the (categories, registry) pair the
// command layer already consumes, so scan/clean need no profile-specific code
// paths of their own.
//
// The returned categories are exactly Profile.Categories, order preserved and
// bypassing disabled_categories -- the same precedent explicit CLI category
// args already get in resolveCleaners. If Profile.Paths is non-empty, the
// project-artifacts cleaner in the returned registry is replaced with one
// scanning those paths, and "project-artifacts" is appended to the category
// list if the profile didn't already list it.
//
// Every Profile.Paths entry is (re)validated here via
// validateProfilePathEntry: a hand-edited, overly-broad path fails this one
// profile at the moment it is used, never config loading. includeLargeFiles
// is forwarded to the substituted cleaner's deleteLargeFiles -- it only
// affects Clean, so scan passes false.
func (c *Config) ResolveProfile(base *cleaner.Registry, name string, includeLargeFiles bool) (categories []string, registry *cleaner.Registry, err error) {
	if base == nil {
		return nil, nil, fmt.Errorf("resolving profile %q: no cleaner registry provided", name)
	}

	var profile Profile
	found := false
	if c != nil {
		profile, found = c.Profiles[name]
	}
	if !found {
		return nil, nil, fmt.Errorf("profile %q is not configured; run \"tidymymac list profiles\" to see the available ones, or \"tidymymac profile create %s\" to add it", name, name)
	}

	if len(profile.Categories) == 0 && len(profile.Paths) == 0 {
		// An empty selection means "every category" downstream, which is the
		// opposite of what an empty profile should do.
		return nil, nil, fmt.Errorf("profile %q is empty: add categories with \"tidymymac profile add-category %s <category>\" or paths with \"tidymymac profile add-path %s <path>\"", name, name, name)
	}

	categories = append(categories, profile.Categories...)

	paths := make([]string, 0, len(profile.Paths))
	for _, raw := range profile.Paths {
		clean, pathErr := validateProfilePathEntry(raw)
		if pathErr != nil {
			return nil, nil, fmt.Errorf("profile %q: %w", name, pathErr)
		}
		paths = append(paths, clean)
	}

	if len(paths) == 0 {
		return categories, base, nil
	}

	// Rebuild rather than Register over the top of a copy: Register replaces
	// the byID entry but *appends* to the ordered slice, so All() would hand
	// back two project-artifacts cleaners.
	registry = cleaner.NewRegistry()
	substituted := false
	for _, cl := range base.All() {
		if cl.Category() == cleaner.CategoryProjectArtifacts {
			registry.Register(cleaner.NewProjectArtifactsCleaner(paths, 0, includeLargeFiles))
			substituted = true
			continue
		}
		registry.Register(cl)
	}
	if !substituted {
		registry.Register(cleaner.NewProjectArtifactsCleaner(paths, 0, includeLargeFiles))
	}

	if !slices.Contains(categories, string(cleaner.CategoryProjectArtifacts)) {
		categories = append(categories, string(cleaner.CategoryProjectArtifacts))
	}

	return categories, registry, nil
}

// FilterRegistry returns a new Registry containing only the cleaners from r
// whose category is not disabled by cfg. Used where there's no equivalent of
// an explicit CLI category selection to take precedence (the TUI, and the
// default-category-set path of the CLI). nil-safe (returns r unchanged).
func FilterRegistry(r *cleaner.Registry, cfg *Config) *cleaner.Registry {
	if cfg == nil || r == nil {
		return r
	}
	filtered := cleaner.NewRegistry()
	for _, c := range r.All() {
		if cfg.IsCategoryDisabled(string(c.Category())) {
			continue
		}
		filtered.Register(c)
	}
	return filtered
}

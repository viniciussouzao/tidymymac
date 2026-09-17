package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// appUninstallLibraryDirs is a superset of appOrphanLibraryDirs: uninstalling a
// known application also justifies looking at the user's LaunchAgents and at
// Group Containers, which the orphan scan deliberately leaves alone.
var appUninstallLibraryDirs = []string{
	"Application Support",
	"Caches",
	"Containers",
	"Preferences",
	"Logs",
	"Saved Application State",
	"HTTPStorages",
	"WebKit",
	"LaunchAgents",
	"Group Containers",
}

// Match sources produced by the discovery heuristics. The scoring weights that
// go with them are defined by the confidence engine (phase 2).
const (
	matchSourceExactBundleID    = "exact_bundle_id"
	matchSourceKnownAppPath     = "known_app_path"
	matchSourceVendorIdentifier = "vendor_identifier"
	matchSourceNameHeuristic    = "name_heuristic"
)

// MatchReason is a single piece of evidence tying a leftover path to the app
// being uninstalled.
//
// NOTE: this is the minimal shape needed by discovery. The confidence engine
// (phase 2) owns this type and will move it to confidence.go, where Weight is
// filled in from the per-source scoring table; discovery leaves Weight at zero.
type MatchReason struct {
	Source string
	Weight int
	Detail string
}

// rawCandidate is a leftover path plus the evidence that linked it to the
// target, before any scoring happens.
type rawCandidate struct {
	entry  FileEntry
	reason MatchReason
	shared bool // true iff the path lives under Group Containers
}

// DiscoverInstalledApps lists the third-party applications found under the
// given search roots. Empty searchRoots falls back to defaultAppSearchRoots.
// Bundles whose identifier cannot be read, or that are not valid third-party
// identifiers, are skipped rather than reported as errors.
func DiscoverInstalledApps(ctx context.Context, searchRoots []string, bundleIDReader func(context.Context, string) (string, error)) ([]AppTarget, error) {
	if bundleIDReader == nil {
		bundleIDReader = readAppBundleID
	}
	if len(searchRoots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			home = ""
		}
		searchRoots = defaultAppSearchRoots(home)
	}

	var (
		apps    []AppTarget
		seen    = map[string]struct{}{}
		firstEr error
	)

	for _, root := range searchRoots {
		if err := ctx.Err(); err != nil {
			return apps, err
		}

		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsPermission(err) {
					if firstEr == nil {
						firstEr = err
					}
					return fs.SkipDir
				}
				return nil
			}
			if ctx.Err() != nil {
				return fs.SkipAll
			}
			if !d.IsDir() || filepath.Ext(path) != ".app" {
				return nil
			}

			bundleID, readErr := bundleIDReader(ctx, filepath.Join(path, "Contents", "Info.plist"))
			if readErr != nil || !isValidThirdPartyBundleID(bundleID) {
				return fs.SkipDir
			}
			if _, ok := seen[path]; ok {
				return fs.SkipDir
			}
			seen[path] = struct{}{}

			apps = append(apps, AppTarget{
				BundlePath: path,
				BundleID:   bundleID,
				Name:       strings.TrimSuffix(filepath.Base(path), ".app"),
			})
			return fs.SkipDir
		})
		if err != nil && !os.IsNotExist(err) && firstEr == nil {
			firstEr = err
		}
	}

	return apps, firstEr
}

// findLeftoverCandidates walks the known Library roots and returns every item
// that matches the target by one of the four evidence heuristics. It is
// read-only.
func (c *AppUninstaller) findLeftoverCandidates(ctx context.Context) ([]rawCandidate, error) {
	if c.homeDir == "" {
		return nil, nil
	}
	c.setDefaults()

	libraryDir := filepath.Join(c.homeDir, "Library")
	var candidates []rawCandidate

	for _, rel := range appUninstallLibraryDirs {
		if err := ctx.Err(); err != nil {
			return candidates, err
		}

		root := filepath.Join(libraryDir, rel)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}

		shared := rel == "Group Containers"

		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return candidates, err
			}

			path := filepath.Join(root, entry.Name())
			if c.target.BundlePath != "" && path == c.target.BundlePath {
				continue
			}

			reason, ok := c.matchReasonFor(rel, entry.Name())
			if !ok {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			isDir := info.IsDir()
			size := info.Size()
			if isDir {
				size, err = c.pathSizeFetcher(ctx, path)
				if err != nil {
					continue
				}
			}

			reason.Detail = path
			candidates = append(candidates, rawCandidate{
				entry: FileEntry{
					Path:     path,
					Size:     size,
					IsDir:    isDir,
					ModTime:  info.ModTime(),
					Category: CategoryAppUninstall,
				},
				reason: reason,
				shared: shared,
			})
		}
	}

	return candidates, nil
}

// matchReasonFor decides which single heuristic (strongest first) links a
// Library item to the target application.
func (c *AppUninstaller) matchReasonFor(rel, name string) (MatchReason, bool) {
	base := leftoverBaseName(rel, name)
	if base == "" {
		return MatchReason{}, false
	}

	bundleID := c.target.BundleID
	appName := c.target.Name

	if bundleID != "" && base == bundleID {
		return MatchReason{Source: matchSourceExactBundleID}, true
	}

	if appName != "" && base == appName {
		return MatchReason{Source: matchSourceKnownAppPath}, true
	}

	if bundleID != "" && isValidThirdPartyBundleID(base) &&
		bundleVendor(base) != "" && bundleVendor(base) == bundleVendor(bundleID) {
		return MatchReason{Source: matchSourceVendorIdentifier}, true
	}

	if appName != "" {
		normalized := normalizeAppName(appName)
		if normalized != "" && normalizeAppName(base) == normalized {
			return MatchReason{Source: matchSourceNameHeuristic}, true
		}
	}

	return MatchReason{}, false
}

// leftoverBaseName strips the suffix/prefix decorations a given Library root
// adds to the identifier it stores ("com.foo.bar.plist", "group.com.foo.bar").
func leftoverBaseName(rel, name string) string {
	switch rel {
	case "Preferences", "LaunchAgents":
		if !strings.HasSuffix(name, ".plist") {
			return ""
		}
		return strings.TrimSuffix(name, ".plist")
	case "Saved Application State":
		if !strings.HasSuffix(name, ".savedState") {
			return ""
		}
		return strings.TrimSuffix(name, ".savedState")
	case "Group Containers":
		return strings.TrimPrefix(name, "group.")
	default:
		return name
	}
}

// bundleVendor returns the first two components of a bundle identifier, i.e.
// the vendor prefix: "com.vendor" for "com.vendor.foo.helper".
func bundleVendor(bundleID string) string {
	parts := strings.Split(bundleID, ".")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// normalizeAppName lowercases a name and drops everything that is not a letter
// or a digit, so "Foo Bar!" and "foobar" compare equal.
func normalizeAppName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

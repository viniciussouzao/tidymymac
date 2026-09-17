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

// libraryRootContainers and libraryRootGroupContainers are distinct roots: a
// Group Container lives under ~/Library/Group Containers, an app's own sandbox
// container under ~/Library/Containers. No candidate can come from both, which
// is why rawCandidate never carries shared and isContainerData at once.
const (
	libraryRootContainers      = "Containers"
	libraryRootGroupContainers = "Group Containers"
)

// rawCandidate is a candidate path plus the evidence that linked it to the
// target, before any scoring happens. It is either a leftover under ~/Library
// or the target's own .app bundle.
type rawCandidate struct {
	entry  FileEntry
	reason MatchReason
	shared bool // true iff the path lives under Group Containers
	// isContainerData is true iff the path is the app's own sandbox container
	// (~/Library/Containers/<id>), which can hold user documents and is
	// therefore never scored Safe. Group Containers are covered by shared.
	isContainerData bool
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

// appBundleCandidate turns the target's own .app bundle into a removal
// candidate, so uninstalling actually removes the application and not only the
// traces it left behind. It returns ok == false when the target has no bundle
// path or the bundle is no longer on disk. It is read-only: nothing but
// os.Lstat and the size fetcher touches the filesystem.
//
// It is deliberately separate from findLeftoverCandidates, which stays a walk
// of ~/Library and nothing else.
func appBundleCandidate(ctx context.Context, target AppTarget, pathSizeFetcher func(context.Context, string) (int64, error)) (rawCandidate, bool) {
	path := strings.TrimSpace(target.BundlePath)
	if path == "" {
		return rawCandidate{}, false
	}

	// Lstat, not Stat: a symlinked "bundle" must be reported as the link it is,
	// so Clean unlinks it instead of recursing into whatever it points at.
	info, err := os.Lstat(path)
	if err != nil {
		return rawCandidate{}, false
	}

	isDir := info.IsDir()
	size := info.Size()
	if isDir && pathSizeFetcher != nil {
		if measured, sizeErr := pathSizeFetcher(ctx, path); sizeErr == nil {
			size = measured
		}
		// A failed measurement is not a reason to hide the application from the
		// uninstall list; the entry is kept with the stat-reported size.
	}

	reason := newMatchReason(matchSourceAppBundleItself)
	reason.Detail = path

	return rawCandidate{
		entry: FileEntry{
			Path:     path,
			Size:     size,
			IsDir:    isDir,
			ModTime:  info.ModTime(),
			Category: CategoryAppUninstall,
		},
		reason: reason,
		// The bundle is neither shared with another app nor sandbox container
		// data: it is program code, the exact thing the user asked to remove.
		shared:          false,
		isContainerData: false,
	}, true
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

		shared := rel == libraryRootGroupContainers
		containerData := rel == libraryRootContainers

		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return candidates, err
			}

			path := filepath.Join(root, entry.Name())
			// The .app bundle is a candidate in its own right, contributed by
			// appBundleCandidate. Skipping it here keeps it from being listed
			// twice in the pathological case where it sits under ~/Library.
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
				reason:          reason,
				shared:          shared,
				isContainerData: containerData,
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
		return newMatchReason(matchSourceExactBundleID), true
	}

	if appName != "" && base == appName {
		return newMatchReason(matchSourceKnownAppPath), true
	}

	if bundleID != "" && isValidThirdPartyBundleID(base) &&
		bundleVendor(base) != "" && bundleVendor(base) == bundleVendor(bundleID) {
		return newMatchReason(matchSourceVendorIdentifier), true
	}

	if appName != "" {
		normalized := normalizeAppName(appName)
		if normalized != "" && normalizeAppName(base) == normalized {
			return newMatchReason(matchSourceNameHeuristic), true
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

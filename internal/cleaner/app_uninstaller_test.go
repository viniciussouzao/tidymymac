package cleaner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestUninstaller(home string, target AppTarget) *AppUninstaller {
	return &AppUninstaller{
		target:          target,
		homeDir:         home,
		bundleIDReader:  readAppBundleID,
		pathSizeFetcher: func(context.Context, string) (int64, error) { return 4096, nil },
	}
}

func candidateByPath(t *testing.T, candidates []rawCandidate, path string) rawCandidate {
	t.Helper()

	for _, c := range candidates {
		if c.entry.Path == path {
			return c
		}
	}
	t.Fatalf("expected candidate for %q, got %v", path, candidatePaths(candidates))
	return rawCandidate{}
}

func candidatePaths(candidates []rawCandidate) []string {
	paths := make([]string, 0, len(candidates))
	for _, c := range candidates {
		paths = append(paths, c.entry.Path)
	}
	return paths
}

func TestAppUninstallerMetadata(t *testing.T) {
	c := NewAppUninstaller(AppTarget{BundlePath: "/Applications/Foo.app", BundleID: "com.acme.foo", Name: "Foo"})

	if c.Category() != CategoryAppUninstall {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryAppUninstall)
	}
	if c.Name() != "Uninstall Foo" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Uninstall Foo")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
	if c.DeletesWholeDomain() {
		t.Error("DeletesWholeDomain() = true, want false")
	}
	if c.Target().BundleID != "com.acme.foo" {
		t.Errorf("Target().BundleID = %q, want %q", c.Target().BundleID, "com.acme.foo")
	}
}

func TestAppUninstallerImplementsCleaner(t *testing.T) {
	var _ Cleaner = NewAppUninstaller(AppTarget{})
}

func TestFindLeftoverCandidatesMatchReasons(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")

	exact := createDir(t, library, "Application Support", "com.acme.editor")
	knownPath := createDir(t, library, "Application Support", "Acme Editor")
	vendor := createDir(t, library, "Caches", "com.acme.launcher")
	nameOnly := createDir(t, library, "Logs", "acmeeditor")
	prefs := createSparseFile(t, filepath.Join(library, "Preferences"), "com.acme.editor.plist", 128)
	agent := createSparseFile(t, filepath.Join(library, "LaunchAgents"), "com.acme.editor.plist", 64)
	group := createDir(t, library, "Group Containers", "group.com.acme.suite")

	unrelatedVendor := createDir(t, library, "Caches", "com.other.editor")
	unrelatedName := createDir(t, library, "Caches", "Spotify")
	notAPlist := createSparseFile(t, filepath.Join(library, "Preferences"), "com.acme.editor.lockfile", 32)

	c := newTestUninstaller(home, AppTarget{
		BundlePath: "/Applications/Acme Editor.app",
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})

	candidates, err := c.findLeftoverCandidates(t.Context())
	if err != nil {
		t.Fatalf("findLeftoverCandidates() error: %v", err)
	}

	wantReason := map[string]string{
		exact:     matchSourceExactBundleID,
		knownPath: matchSourceKnownAppPath,
		vendor:    matchSourceVendorIdentifier,
		nameOnly:  matchSourceNameHeuristic,
		prefs:     matchSourceExactBundleID,
		agent:     matchSourceExactBundleID,
		group:     matchSourceVendorIdentifier,
	}
	for path, want := range wantReason {
		got := candidateByPath(t, candidates, path)
		if got.reason.Source != want {
			t.Errorf("candidate %q reason = %q, want %q", path, got.reason.Source, want)
		}
		if got.reason.Detail != path {
			t.Errorf("candidate %q reason detail = %q, want the path", path, got.reason.Detail)
		}
		if got.entry.Category != CategoryAppUninstall {
			t.Errorf("candidate %q category = %q, want %q", path, got.entry.Category, CategoryAppUninstall)
		}
	}

	found := map[string]bool{}
	for _, candidate := range candidates {
		found[candidate.entry.Path] = true
	}
	for _, path := range []string{unrelatedVendor, unrelatedName, notAPlist} {
		if found[path] {
			t.Errorf("did not expect candidate %q", path)
		}
	}
}

func TestFindLeftoverCandidatesGroupContainersAlwaysShared(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")

	group := createDir(t, library, "Group Containers", "group.com.acme.suite")
	exactGroup := createDir(t, library, "Group Containers", "group.com.acme.editor")
	nonShared := createDir(t, library, "Caches", "com.acme.editor")

	c := newTestUninstaller(home, AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})

	candidates, err := c.findLeftoverCandidates(t.Context())
	if err != nil {
		t.Fatalf("findLeftoverCandidates() error: %v", err)
	}

	for _, path := range []string{group, exactGroup} {
		got := candidateByPath(t, candidates, path)
		if !got.shared {
			t.Errorf("candidate %q shared = false, want true", path)
		}
	}
	if got := candidateByPath(t, candidates, exactGroup); got.reason.Source != matchSourceExactBundleID {
		t.Errorf("group container with exact bundle id reason = %q, want %q", got.reason.Source, matchSourceExactBundleID)
	}
	if got := candidateByPath(t, candidates, nonShared); got.shared {
		t.Errorf("candidate %q shared = true, want false", nonShared)
	}
}

func TestFindLeftoverCandidatesSameVendorDifferentAppIsNotExact(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")

	launcher := createDir(t, library, "Application Support", "com.acme.launcher")

	c := newTestUninstaller(home, AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})

	candidates, err := c.findLeftoverCandidates(t.Context())
	if err != nil {
		t.Fatalf("findLeftoverCandidates() error: %v", err)
	}

	got := candidateByPath(t, candidates, launcher)
	if got.reason.Source == matchSourceExactBundleID {
		t.Fatalf("leftover of a sibling app must never match %q", matchSourceExactBundleID)
	}
	if got.reason.Source != matchSourceVendorIdentifier {
		t.Errorf("reason = %q, want %q", got.reason.Source, matchSourceVendorIdentifier)
	}
}

func TestFindLeftoverCandidatesIgnoresInstalledBundle(t *testing.T) {
	home := t.TempDir()
	appRoot := filepath.Join(home, "Applications")
	bundle := createTestApp(t, appRoot, "Acme Editor.app", "com.acme.editor")

	c := newTestUninstaller(home, AppTarget{
		BundlePath: bundle,
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})

	candidates, err := c.findLeftoverCandidates(t.Context())
	if err != nil {
		t.Fatalf("findLeftoverCandidates() error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no leftover candidates, got %v", candidatePaths(candidates))
	}
}

func TestAppUninstallerScanAggregates(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")
	createDir(t, library, "Application Support", "com.acme.editor")
	createSparseFile(t, filepath.Join(library, "Preferences"), "com.acme.editor.plist", 128)

	c := newTestUninstaller(home, AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})

	var progressCalls int
	result, err := c.Scan(t.Context(), func(p ScanProgress) {
		progressCalls++
		if p.Category != CategoryAppUninstall {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryAppUninstall)
		}
	})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result.TotalFiles != 2 || len(result.Entries) != 2 {
		t.Fatalf("TotalFiles = %d, entries = %d, want 2 and 2", result.TotalFiles, len(result.Entries))
	}
	if result.TotalSize != 4096+128 {
		t.Errorf("TotalSize = %d, want %d", result.TotalSize, 4096+128)
	}
	if progressCalls == 0 {
		t.Error("expected at least one progress callback")
	}
}

func TestAppUninstallerScanEmptyHomeDir(t *testing.T) {
	c := &AppUninstaller{target: AppTarget{BundleID: "com.acme.editor"}}

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries with empty homeDir, got %d", len(result.Entries))
	}
}

func TestAppUninstallerScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewAppUninstaller(AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})
	if _, err := c.Scan(ctx, nil); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestAppUninstallerCleanDryRunAndDeletion(t *testing.T) {
	tests := []struct {
		name       string
		dryRun     bool
		wantExists bool
	}{
		{name: "dry run keeps files", dryRun: true, wantExists: true},
		{name: "execute removes files", dryRun: false, wantExists: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			filePath := createSparseFile(t, dir, "com.acme.editor.plist", 1024)
			dirPath := filepath.Join(dir, "com.acme.editor")
			if err := os.MkdirAll(dirPath, 0o755); err != nil {
				t.Fatal(err)
			}
			createSparseFile(t, dirPath, "payload.bin", 2048)

			// A real BundlePath plus a not-running checker: a non-dry-run Clean
			// deliberately refuses when the bundle path is unknown, so this
			// test has to give it a target it can actually verify.
			c := NewAppUninstaller(AppTarget{
				BundlePath: "/Applications/Acme Editor.app",
				BundleID:   "com.acme.editor",
				Name:       "Acme Editor",
			})
			c.SetProcessChecker(&fakeProcessChecker{running: false})
			entries := []FileEntry{
				{Path: filePath, Size: 1024, Category: CategoryAppUninstall},
				{Path: dirPath, Size: 2048, IsDir: true, Category: CategoryAppUninstall},
				{Path: filepath.Join(dir, "gone"), Size: 8, Category: CategoryAppUninstall},
			}

			result, err := c.Clean(t.Context(), entries, tt.dryRun, nil)
			if err != nil {
				t.Fatalf("Clean() error: %v", err)
			}
			if result.DryRun != tt.dryRun {
				t.Errorf("DryRun = %v, want %v", result.DryRun, tt.dryRun)
			}
			if result.FilesDeleted != 3 {
				t.Errorf("FilesDeleted = %d, want 3 (missing path counts as success)", result.FilesDeleted)
			}
			if len(result.Errors) != 0 {
				t.Errorf("Errors = %v, want none", result.Errors)
			}

			_, fileErr := os.Stat(filePath)
			_, dirErr := os.Stat(dirPath)
			if tt.wantExists && (fileErr != nil || dirErr != nil) {
				t.Error("dry run must not remove anything")
			}
			if !tt.wantExists && (!os.IsNotExist(fileErr) || !os.IsNotExist(dirErr)) {
				t.Error("entries should have been deleted")
			}
		})
	}
}

func TestAppUninstallerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewAppUninstaller(AppTarget{BundleID: "com.acme.editor"})
	_, err := c.Clean(ctx, []FileEntry{{Path: "/tmp/file", Size: 1, Category: CategoryAppUninstall}}, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDiscoverInstalledApps(t *testing.T) {
	home := t.TempDir()
	appRoot := filepath.Join(home, "Applications")
	createTestApp(t, appRoot, "Acme Editor.app", "com.acme.editor")
	createTestApp(t, appRoot, "Launcher.app", "com.acme.launcher")
	createTestApp(t, appRoot, "TextEdit.app", "com.apple.TextEdit")
	createDir(t, appRoot, "NotAnApp")
	createTestApp(t, filepath.Join(appRoot, "Acme Editor.app", "Contents", "Helpers"), "Helper.app", "com.acme.editor.helper")

	apps, err := DiscoverInstalledApps(t.Context(), []string{appRoot}, readAppBundleID)
	if err != nil {
		t.Fatalf("DiscoverInstalledApps() error: %v", err)
	}

	got := map[string]AppTarget{}
	for _, app := range apps {
		got[app.BundleID] = app
	}

	if len(got) != 2 {
		t.Fatalf("discovered %d apps, want 2: %v", len(got), got)
	}
	editor, ok := got["com.acme.editor"]
	if !ok {
		t.Fatal("expected com.acme.editor to be discovered")
	}
	if editor.Name != "Acme Editor" {
		t.Errorf("Name = %q, want %q", editor.Name, "Acme Editor")
	}
	if editor.BundlePath != filepath.Join(appRoot, "Acme Editor.app") {
		t.Errorf("BundlePath = %q, want the bundle dir", editor.BundlePath)
	}
	if _, ok := got["com.apple.TextEdit"]; ok {
		t.Error("Apple bundles must not be listed as uninstallable apps")
	}
	if _, ok := got["com.acme.editor.helper"]; ok {
		t.Error("nested helper bundles must not be listed separately")
	}
}

func TestDiscoverInstalledAppsMissingRoot(t *testing.T) {
	apps, err := DiscoverInstalledApps(t.Context(), []string{filepath.Join(t.TempDir(), "nope")}, readAppBundleID)
	if err != nil {
		t.Fatalf("DiscoverInstalledApps() error: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("expected no apps, got %v", apps)
	}
}

func TestBundleVendor(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"com.vendor.foo.helper", "com.vendor"},
		{"com.vendor.foo", "com.vendor"},
		{"com.vendor", "com.vendor"},
		{"com", ""},
		{"", ""},
		{"com..foo", ""},
	}

	for _, tt := range tests {
		if got := bundleVendor(tt.in); got != tt.want {
			t.Errorf("bundleVendor(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeAppName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Acme Editor", "acmeeditor"},
		{"acme-editor", "acmeeditor"},
		{"Acme  Editor!", "acmeeditor"},
		{"Slack", "slack"},
		{"SlackBot", "slackbot"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := normalizeAppName(tt.in); got != tt.want {
			t.Errorf("normalizeAppName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLeftoverBaseName(t *testing.T) {
	tests := []struct {
		rel  string
		name string
		want string
	}{
		{"Caches", "com.acme.editor", "com.acme.editor"},
		{"Preferences", "com.acme.editor.plist", "com.acme.editor"},
		{"Preferences", "com.acme.editor.lockfile", ""},
		{"LaunchAgents", "com.acme.editor.plist", "com.acme.editor"},
		{"Saved Application State", "com.acme.editor.savedState", "com.acme.editor"},
		{"Saved Application State", "com.acme.editor", ""},
		{"Group Containers", "group.com.acme.suite", "com.acme.suite"},
	}

	for _, tt := range tests {
		if got := leftoverBaseName(tt.rel, tt.name); got != tt.want {
			t.Errorf("leftoverBaseName(%q, %q) = %q, want %q", tt.rel, tt.name, got, tt.want)
		}
	}
}

// The .app bundle is a removal candidate in its own right: without it an
// "uninstall" would only sweep up the traces and leave the application itself
// installed. It is the exact resolved target, so it scores at the top of the
// evidence table and lands in Safe.
func TestScanIncludesAppBundleAsCandidate(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")
	leftover := createDir(t, library, "Application Support", "com.acme.editor")
	bundle := createTestApp(t, filepath.Join(home, "Applications"), "Acme Editor.app", "com.acme.editor")

	c := newTestUninstaller(home, AppTarget{
		BundlePath: bundle,
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	var bundleEntry *FileEntry
	for i, entry := range result.Entries {
		if entry.Path == bundle {
			bundleEntry = &result.Entries[i]
		}
	}
	if bundleEntry == nil {
		t.Fatalf("Scan entries %v do not include the bundle %q", entryPaths(result.Entries), bundle)
	}
	if !bundleEntry.IsDir {
		t.Error("bundle entry IsDir = false, want true: a .app is a directory")
	}
	if bundleEntry.Size != 4096 {
		t.Errorf("bundle entry Size = %d, want the size fetcher's 4096", bundleEntry.Size)
	}
	if bundleEntry.Category != CategoryAppUninstall {
		t.Errorf("bundle entry Category = %q, want %q", bundleEntry.Category, CategoryAppUninstall)
	}

	got, ok := c.ExplainCandidate(*bundleEntry)
	if !ok {
		t.Fatal("the bundle entry was not explained: it must go through the same scoring pipeline")
	}
	if got.Band != ConfidenceSafe {
		t.Errorf("bundle Band = %q, want %q", got.Band, ConfidenceSafe)
	}
	if got.Score != weightAppBundleItself || len(got.Reasons) != 1 || got.Reasons[0].Source != matchSourceAppBundleItself {
		t.Errorf("bundle evidence = %+v, want the single source %q at weight %d",
			got, matchSourceAppBundleItself, weightAppBundleItself)
	}
	if got.Reasons[0].Detail != bundle {
		t.Errorf("bundle reason detail = %q, want the path", got.Reasons[0].Detail)
	}
	if got.Shared || got.ContainerData {
		t.Errorf("bundle flags = %+v, want neither shared nor container data", got)
	}

	// The leftovers are still there: the bundle is an addition, not a swap.
	if _, ok := c.ExplainCandidate(FileEntry{Path: leftover}); !ok {
		t.Errorf("leftover %q disappeared from the scan", leftover)
	}
	// And it is listed exactly once.
	var count int
	for _, entry := range result.Entries {
		if entry.Path == bundle {
			count++
		}
	}
	if count != 1 {
		t.Errorf("bundle listed %d times, want exactly 1", count)
	}
}

func TestAppBundleCandidate(t *testing.T) {
	home := t.TempDir()
	bundle := createTestApp(t, filepath.Join(home, "Applications"), "Acme Editor.app", "com.acme.editor")
	sizer := func(context.Context, string) (int64, error) { return 8192, nil }

	t.Run("no bundle path", func(t *testing.T) {
		if _, ok := appBundleCandidate(t.Context(), AppTarget{BundleID: "com.acme.editor"}, sizer); ok {
			t.Error("ok = true for a target with no bundle path, want false")
		}
	})

	t.Run("bundle not on disk", func(t *testing.T) {
		target := AppTarget{BundlePath: filepath.Join(home, "Applications", "Gone.app")}
		if _, ok := appBundleCandidate(t.Context(), target, sizer); ok {
			t.Error("ok = true for a bundle that is not on disk, want false")
		}
	})

	t.Run("size fetcher failure still yields a candidate", func(t *testing.T) {
		failing := func(context.Context, string) (int64, error) { return 0, errors.New("du exploded") }
		got, ok := appBundleCandidate(t.Context(), AppTarget{BundlePath: bundle}, failing)
		if !ok {
			t.Fatal("ok = false; an unmeasurable bundle must still be offered for removal")
		}
		if got.entry.Path != bundle {
			t.Errorf("entry path = %q, want %q", got.entry.Path, bundle)
		}
	})

	got, ok := appBundleCandidate(t.Context(), AppTarget{BundlePath: bundle}, sizer)
	if !ok {
		t.Fatal("ok = false for a real bundle")
	}
	if got.entry.Size != 8192 {
		t.Errorf("entry size = %d, want the fetcher's 8192", got.entry.Size)
	}
	if got.reason.Source != matchSourceAppBundleItself || got.reason.Weight != weightAppBundleItself {
		t.Errorf("reason = %+v, want %q at %d", got.reason, matchSourceAppBundleItself, weightAppBundleItself)
	}
	if got.shared || got.isContainerData {
		t.Errorf("candidate flags = %+v, want neither shared nor container data", got)
	}
}

func TestCleanRemovesAppBundleWhenWritable(t *testing.T) {
	home := t.TempDir()
	bundle := createTestApp(t, filepath.Join(home, "Applications"), "Acme Editor.app", "com.acme.editor")
	leftover := createDir(t, filepath.Join(home, "Library"), "Application Support", "com.acme.editor")

	c := newTestUninstaller(home, AppTarget{
		BundlePath: bundle,
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})
	c.SetProcessChecker(&fakeProcessChecker{running: false})

	scanned, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	result, err := c.Clean(t.Context(), scanned.Entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Errors = %v, want none", result.Errors)
	}
	if result.Skipped {
		t.Errorf("Skipped = true (%q), want false", result.SkipReason)
	}
	if result.FilesDeleted != len(scanned.Entries) {
		t.Errorf("FilesDeleted = %d, want %d", result.FilesDeleted, len(scanned.Entries))
	}
	for _, path := range []string{bundle, leftover} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%q should have been deleted (stat err = %v)", path, err)
		}
	}
}

// A bundle the current user cannot remove (the /Applications-owned-by-root
// case, simulated with a read-only parent directory) is reported as a per-entry
// error with an actionable message. It must not abort the batch and must not
// set Skipped: the leftovers in ~/Library are still cleaned, and "skipped"
// means "nothing was touched at all", which is reserved for the running-app
// refusal.
func TestCleanReportsPermissionErrorForAppBundleWithoutBlockingLeftovers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: no path is unwritable")
	}

	home := t.TempDir()
	appRoot := filepath.Join(home, "Applications")
	bundle := createTestApp(t, appRoot, "Acme Editor.app", "com.acme.editor")
	leftover := createDir(t, filepath.Join(home, "Library"), "Application Support", "com.acme.editor")

	c := newTestUninstaller(home, AppTarget{
		BundlePath: bundle,
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})
	c.SetProcessChecker(&fakeProcessChecker{running: false})

	scanned, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	// Removing a directory entry needs write permission on its *parent*, so
	// locking appRoot is what makes the bundle unremovable. Restored on cleanup
	// so t.TempDir() can clean itself up.
	if err := os.Chmod(appRoot, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(appRoot, 0o755) })

	result, err := c.Clean(t.Context(), scanned.Entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if result.Skipped {
		t.Errorf("Skipped = true (%q); a permission failure is not a skip", result.SkipReason)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %v, want exactly one (the bundle)", result.Errors)
	}
	message := result.Errors[0].Error()
	for _, want := range []string{"elevated permissions", bundle} {
		if !strings.Contains(message, want) {
			t.Errorf("error = %q, want it to mention %q", message, want)
		}
	}

	if _, err := os.Stat(bundle); err != nil {
		t.Errorf("the bundle should still be on disk: %v", err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("the leftover should have been removed despite the bundle failure (stat err = %v)", err)
	}
	if result.FilesDeleted != len(scanned.Entries)-1 {
		t.Errorf("FilesDeleted = %d, want %d: one failure never aborts the batch",
			result.FilesDeleted, len(scanned.Entries)-1)
	}
}

func entryPaths(entries []FileEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

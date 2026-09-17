package cleaner

import (
	"context"
	"os"
	"path/filepath"
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

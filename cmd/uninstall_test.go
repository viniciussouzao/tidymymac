package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
)

// ---------------------------------------------------------------------------
// passesMinConfidence / filterEntriesByConfidence -- the pure band-filtering
// logic, independent of any real cleaner.
// ---------------------------------------------------------------------------

func TestPassesMinConfidence(t *testing.T) {
	tests := []struct {
		name          string
		minConfidence string
		conf          cleaner.Confidence
		want          bool
	}{
		{"safe/safe", "safe", cleaner.Confidence{Band: cleaner.ConfidenceSafe}, true},
		{"safe/review", "safe", cleaner.Confidence{Band: cleaner.ConfidenceReview}, false},
		{"safe/caution", "safe", cleaner.Confidence{Band: cleaner.ConfidenceCaution}, false},
		{"safe/zero-value", "safe", cleaner.Confidence{}, false},

		{"review/safe", "review", cleaner.Confidence{Band: cleaner.ConfidenceSafe}, true},
		{"review/review", "review", cleaner.Confidence{Band: cleaner.ConfidenceReview}, true},
		{"review/caution", "review", cleaner.Confidence{Band: cleaner.ConfidenceCaution}, false},
		{"review/zero-value", "review", cleaner.Confidence{}, false},

		{"caution/safe", "caution", cleaner.Confidence{Band: cleaner.ConfidenceSafe}, true},
		{"caution/review", "caution", cleaner.Confidence{Band: cleaner.ConfidenceReview}, true},
		{"caution/caution", "caution", cleaner.Confidence{Band: cleaner.ConfidenceCaution}, true},
		// The zero value must never pass even at the widest tier: it is not a
		// real Caution verdict, it is "no evidence" (see
		// docs/ARCHITECTURE.md's Confidence section and passesMinConfidence's
		// own doc comment). filterEntriesByConfidence already keeps this from
		// happening in practice by checking ExplainCandidate's ok first, but
		// this pins the fail-closed behaviour of the function itself too.
		{"caution/zero-value", "caution", cleaner.Confidence{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := passesMinConfidence(tt.conf, tt.minConfidence); got != tt.want {
				t.Errorf("passesMinConfidence(%+v, %q) = %v, want %v", tt.conf, tt.minConfidence, got, tt.want)
			}
		})
	}
}

// fakeExplainer is a minimal cleaner.CandidateExplainer double, keyed by
// path, for testing filterEntriesByConfidence without a real AppUninstaller.
type fakeExplainer map[string]cleaner.Confidence

func (f fakeExplainer) ExplainCandidate(entry cleaner.FileEntry) (cleaner.Confidence, bool) {
	conf, ok := f[entry.Path]
	return conf, ok
}

func TestFilterEntriesByConfidence(t *testing.T) {
	entries := []cleaner.FileEntry{
		{Path: "/a/safe"},
		{Path: "/a/review"},
		{Path: "/a/caution"},
		{Path: "/a/unexplained"}, // deliberately absent from the explainer
	}
	explainer := fakeExplainer{
		"/a/safe":    {Band: cleaner.ConfidenceSafe},
		"/a/review":  {Band: cleaner.ConfidenceReview},
		"/a/caution": {Band: cleaner.ConfidenceCaution},
	}

	safe := filterEntriesByConfidence(explainer, entries, "safe")
	assertPaths(t, safe, "/a/safe")

	review := filterEntriesByConfidence(explainer, entries, "review")
	assertPaths(t, review, "/a/safe", "/a/review")

	caution := filterEntriesByConfidence(explainer, entries, "caution")
	assertPaths(t, caution, "/a/safe", "/a/review", "/a/caution")
}

func assertPaths(t *testing.T, entries []cleaner.FileEntry, want ...string) {
	t.Helper()
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Path)
	}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	for _, g := range got {
		if !wantSet[g] {
			t.Errorf("unexpected path %q in %v, want %v", g, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// resolveUninstallTarget
// ---------------------------------------------------------------------------

func TestResolveUninstallTarget(t *testing.T) {
	apps := []cleaner.AppTarget{
		{Name: "Foo", BundleID: "com.acme.foo", BundlePath: "/Applications/Foo.app"},
		{Name: "Bar", BundleID: "com.other.bar", BundlePath: "/Applications/Bar.app"},
		{Name: "Foo", BundleID: "com.rival.foo", BundlePath: "/Applications/Foo (rival).app"},
	}

	t.Run("matches by bundle id case-insensitively", func(t *testing.T) {
		got, err := resolveUninstallTarget(apps, "COM.ACME.FOO")
		if err != nil {
			t.Fatalf("resolveUninstallTarget() error: %v", err)
		}
		if got.BundleID != "com.acme.foo" {
			t.Errorf("got %+v, want com.acme.foo", got)
		}
	})

	t.Run("matches by unique name case-insensitively", func(t *testing.T) {
		got, err := resolveUninstallTarget(apps, "bar")
		if err != nil {
			t.Fatalf("resolveUninstallTarget() error: %v", err)
		}
		if got.BundleID != "com.other.bar" {
			t.Errorf("got %+v, want com.other.bar", got)
		}
	})

	t.Run("ambiguous name is rejected rather than guessed at", func(t *testing.T) {
		_, err := resolveUninstallTarget(apps, "Foo")
		if err == nil {
			t.Fatal("expected an ambiguous-match error, got nil")
		}
		if !strings.Contains(err.Error(), "more than one") {
			t.Errorf("error = %v, want it to mention the ambiguity", err)
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, err := resolveUninstallTarget(apps, "does-not-exist")
		if err == nil {
			t.Fatal("expected a not-found error, got nil")
		}
		if !strings.Contains(err.Error(), "--list") {
			t.Errorf("error = %v, want it to point at --list", err)
		}
	})

	t.Run("empty query", func(t *testing.T) {
		if _, err := resolveUninstallTarget(apps, "   "); err == nil {
			t.Fatal("expected an error for an empty query")
		}
	})
}

// ---------------------------------------------------------------------------
// Regression: uninstalling "Foo" at the default --min-confidence=safe must
// never remove "Foo Helper"'s leftovers, even though they share a vendor
// prefix (com.acme) that a name/vendor heuristic alone would otherwise treat
// as related. That relationship is real evidence (matchSourceVendorIdentifier,
// weight 80) but it only clears ConfidenceReview, not ConfidenceSafe -- see
// internal/cleaner/app_uninstaller_confidence.go.
// ---------------------------------------------------------------------------

// fooFixture is everything one subtest needs to exercise a real
// cleaner.AppUninstaller against an isolated fake home directory.
type fooFixture struct {
	registry   *cleaner.Registry
	explainer  cleaner.CandidateExplainer
	bundlePath string // Foo.app itself -- an exact-identity Safe candidate
	prefsPath  string // Foo's own Preferences plist -- exact_bundle_id, Safe
	helperPath string // "Foo Helper"'s Application Support dir -- vendor_identifier, Review only
}

// setupFooFixture builds Foo.app, one of its own leftovers, and one leftover
// that actually belongs to a *different*, similarly-named/vendored app ("Foo
// Helper", com.acme.foo.helper) underneath a fresh, isolated $HOME. Nothing
// outside t.TempDir() is ever touched.
func setupFooFixture(t *testing.T) fooFixture {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	withLoadedConfig(t)

	library := filepath.Join(home, "Library")

	bundlePath := filepath.Join(home, "Applications", "Foo.app")
	mustMkdirAll(t, filepath.Join(bundlePath, "Contents", "MacOS"))
	mustWriteFile(t, filepath.Join(bundlePath, "Contents", "MacOS", "Foo"), "binary")

	prefsPath := filepath.Join(library, "Preferences", "com.acme.foo.plist")
	mustMkdirAll(t, filepath.Dir(prefsPath))
	mustWriteFile(t, prefsPath, "prefs")

	// "Foo Helper" (com.acme.foo.helper) is a distinct application that is
	// NOT being uninstalled here. Its own Application Support directory only
	// shares a vendor prefix with the target ("com.acme"), which is exactly
	// the vendor_identifier heuristic -- Review, never Safe.
	helperPath := filepath.Join(library, "Application Support", "com.acme.foo.helper")
	mustMkdirAll(t, helperPath)
	mustWriteFile(t, filepath.Join(helperPath, "state.db"), "helper state")

	target := cleaner.AppTarget{BundlePath: bundlePath, BundleID: "com.acme.foo", Name: "Foo"}
	au := cleaner.NewAppUninstaller(target)

	registry := cleaner.NewRegistry()
	registry.Register(au)

	return fooFixture{
		registry:   registry,
		explainer:  au,
		bundlePath: bundlePath,
		prefsPath:  prefsPath,
		helperPath: helperPath,
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func pathExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("Lstat(%q): %v", path, err)
	return false
}

// decodeCleanOutput parses what runUninstallNonInteractive wrote to stdout.
func decodeCleanOutput(t *testing.T, raw string) commands.CleanOutput {
	t.Helper()
	var out commands.CleanOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode CleanOutput: %v\nraw: %s", err, raw)
	}
	return out
}

func filePaths(cat commands.CleanCategoryResult) []string {
	paths := make([]string, 0, len(cat.Files))
	for _, f := range cat.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func TestUninstall_DefaultSafeConfidence_ExcludesRelatedAppLeftovers(t *testing.T) {
	fx := setupFooFixture(t)
	ctx := context.Background()

	stop := captureStdout(t)
	err := runUninstallNonInteractive(ctx, fx.registry, fx.explainer, "safe", true, "json")
	raw := stop()
	if err != nil {
		t.Fatalf("runUninstallNonInteractive() (dry-run) error: %v\noutput: %s", err, raw)
	}

	out := decodeCleanOutput(t, raw)
	if len(out.Result.Categories) != 1 {
		t.Fatalf("categories = %d, want 1", len(out.Result.Categories))
	}
	cat := out.Result.Categories[0]

	paths := filePaths(cat)
	for _, want := range []string{fx.bundlePath, fx.prefsPath} {
		if !containsString(paths, want) {
			t.Errorf("expected safe-band path %q in preview, got %v", want, paths)
		}
	}
	if containsString(paths, fx.helperPath) {
		t.Fatalf("REGRESSION: 'Foo Helper' leftover %q was included in a --min-confidence safe preview of Foo; it only clears Review", fx.helperPath)
	}

	// Nothing was touched -- this call never set executeFlag, and it must
	// still be a dry run by default regardless.
	for _, p := range []string{fx.bundlePath, fx.prefsPath, fx.helperPath} {
		if !pathExists(t, p) {
			t.Fatalf("dry-run preview deleted %q from disk", p)
		}
	}
}

func TestUninstall_Execute_DefaultSafeConfidence_NeverDeletesHelperLeftovers(t *testing.T) {
	fx := setupFooFixture(t)
	ctx := context.Background()
	withExecuteFlag(t, true)

	stop := captureStdout(t)
	err := runUninstallNonInteractive(ctx, fx.registry, fx.explainer, "safe", true, "json")
	raw := stop()
	if err != nil {
		t.Fatalf("runUninstallNonInteractive() (execute) error: %v\noutput: %s", err, raw)
	}

	out := decodeCleanOutput(t, raw)
	if out.Result.HasErrors {
		t.Fatalf("unexpected errors in result: %+v", out.Result)
	}

	// Foo's own bundle and preferences file -- both Safe -- are actually gone.
	if pathExists(t, fx.bundlePath) {
		t.Errorf("Foo.app bundle %q still exists after --execute", fx.bundlePath)
	}
	if pathExists(t, fx.prefsPath) {
		t.Errorf("Foo's own preferences %q still exists after --execute", fx.prefsPath)
	}

	// The regression: "Foo Helper"'s leftover must survive a real deletion
	// run, not just a dry-run preview.
	if !pathExists(t, fx.helperPath) {
		t.Fatalf("REGRESSION: 'Foo Helper' leftover %q was deleted by a --min-confidence safe --execute run of Foo", fx.helperPath)
	}

	cat := out.Result.Categories[0]
	if cat.DeletedFiles != 2 {
		t.Errorf("DeletedFiles = %d, want 2 (bundle + prefs only)", cat.DeletedFiles)
	}
}

func TestUninstall_MinConfidenceReview_IncludesRelatedAppLeftovers(t *testing.T) {
	fx := setupFooFixture(t)
	ctx := context.Background()

	stop := captureStdout(t)
	err := runUninstallNonInteractive(ctx, fx.registry, fx.explainer, "review", true, "json")
	raw := stop()
	if err != nil {
		t.Fatalf("runUninstallNonInteractive() error: %v\noutput: %s", err, raw)
	}

	out := decodeCleanOutput(t, raw)
	paths := filePaths(out.Result.Categories[0])
	if !containsString(paths, fx.helperPath) {
		t.Fatalf("--min-confidence review should widen the preview to include the Review-band helper leftover %q, got %v", fx.helperPath, paths)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// writeUninstallAppListJSON / writeUninstallAppListHuman
// ---------------------------------------------------------------------------

func TestWriteUninstallAppListJSON(t *testing.T) {
	apps := []cleaner.AppTarget{
		{Name: "Foo", BundleID: "com.acme.foo", BundlePath: "/Applications/Foo.app"},
	}
	var buf strings.Builder
	if err := writeUninstallAppListJSON(&buf, apps); err != nil {
		t.Fatalf("writeUninstallAppListJSON() error: %v", err)
	}

	var decoded []uninstallAppListEntry
	if err := json.Unmarshal([]byte(buf.String()), &decoded); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, buf.String())
	}
	if len(decoded) != 1 || decoded[0].BundleID != "com.acme.foo" {
		t.Errorf("decoded = %+v, want one entry for com.acme.foo", decoded)
	}
}

func TestWriteUninstallAppListHuman_Empty(t *testing.T) {
	var buf strings.Builder
	if err := writeUninstallAppListHuman(&buf, nil); err != nil {
		t.Fatalf("writeUninstallAppListHuman() error: %v", err)
	}
	if !strings.Contains(buf.String(), "no third-party applications found") {
		t.Errorf("output = %q, want the empty-state message", buf.String())
	}
}

func TestWriteUninstallAppListHuman_ListsSortedByName(t *testing.T) {
	apps := []cleaner.AppTarget{
		{Name: "Zeta", BundleID: "com.acme.zeta", BundlePath: "/Applications/Zeta.app"},
		{Name: "Alpha", BundleID: "com.acme.alpha", BundlePath: "/Applications/Alpha.app"},
	}
	var buf strings.Builder
	if err := writeUninstallAppListHuman(&buf, apps); err != nil {
		t.Fatalf("writeUninstallAppListHuman() error: %v", err)
	}
	out := buf.String()
	if strings.Index(out, "Alpha") > strings.Index(out, "Zeta") {
		t.Errorf("expected Alpha before Zeta, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Command wiring (flag validation, end-to-end RunE) -- mirrors the style of
// TestCommandsReturnOutputErrors in output_errors_test.go.
// ---------------------------------------------------------------------------

func TestUninstallCmd_FlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"invalid output", []string{"--output", "csv", "Foo"}, "invalid --output value"},
		{"invalid min-confidence", []string{"--min-confidence", "yolo", "Foo"}, "invalid --min-confidence value"},
		{"list with positional arg", []string{"--list", "Foo"}, "does not take an application argument"},
		{"missing argument", []string{}, "requires exactly one application"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uninstallCmd.SetArgs(nil) // flags are read via cmd.Flags().Get*, not cobra parsing, below
			resetUninstallFlags(t)
			if err := applyUninstallFlags(t, tt.args); err != nil {
				t.Fatalf("applyUninstallFlags: %v", err)
			}

			var positional []string
			for _, a := range tt.args {
				if !strings.HasPrefix(a, "-") {
					positional = append(positional, a)
				}
			}

			err := uninstallCmd.RunE(uninstallCmd, positional)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// resetUninstallFlags restores every uninstallCmd flag to its registered
// default before a subtest sets its own, so subtests never leak flag state
// into one another.
func resetUninstallFlags(t *testing.T) {
	t.Helper()
	uninstallCmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

// applyUninstallFlags sets uninstallCmd's own flags (not positional args)
// from a cobra-style argument slice, without going through
// uninstallCmd.Execute() -- this package's tests call RunE directly instead
// of Execute() throughout (see TestExecuteEntryPointsRefuseRoot), since
// Execute() would also run PersistentPreRunE against the real config file.
func applyUninstallFlags(t *testing.T, args []string) error {
	t.Helper()
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		name := strings.TrimPrefix(a, "--")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			if err := uninstallCmd.Flags().Set(name[:eq], name[eq+1:]); err != nil {
				return err
			}
			continue
		}
		f := uninstallCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("unknown flag --%s", name)
		}
		if f.Value.Type() == "bool" {
			if err := uninstallCmd.Flags().Set(name, "true"); err != nil {
				return err
			}
			continue
		}
		if i+1 >= len(args) {
			t.Fatalf("flag --%s expects a value", name)
		}
		i++
		if err := uninstallCmd.Flags().Set(name, args[i]); err != nil {
			return err
		}
	}
	return nil
}

// TestUninstallCmd_List_EndToEnd is a smoke test of the full wiring for
// 'tidymymac uninstall --list --output json': flag parsing, discovery, and
// JSON encoding. It intentionally makes no assertion about *which*
// applications are found (that depends on the machine it runs on) -- only
// that the command runs to completion and produces a well-formed JSON array,
// exactly like 'tidymymac scan --output json' running against whatever the
// host machine happens to have.
func TestUninstallCmd_List_EndToEnd(t *testing.T) {
	resetUninstallFlags(t)
	if err := applyUninstallFlags(t, []string{"--list", "--output", "json"}); err != nil {
		t.Fatalf("applyUninstallFlags: %v", err)
	}
	uninstallCmd.SetContext(context.Background())

	stop := captureStdout(t)
	err := uninstallCmd.RunE(uninstallCmd, nil)
	raw := stop()
	if err != nil {
		t.Fatalf("uninstall --list --output json: %v\noutput: %s", err, raw)
	}

	var decoded []uninstallAppListEntry
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, raw)
	}
}

// TestUninstallCmd_UnknownApp_EndToEnd exercises the full non-list RunE path
// (flag validation, discovery, resolveUninstallTarget) deterministically,
// without depending on any specific application being installed on the host:
// an application name this unlikely to exist must always fail to resolve.
func TestUninstallCmd_UnknownApp_EndToEnd(t *testing.T) {
	resetUninstallFlags(t)
	if err := applyUninstallFlags(t, []string{"--output", "json"}); err != nil {
		t.Fatalf("applyUninstallFlags: %v", err)
	}
	uninstallCmd.SetContext(context.Background())

	err := uninstallCmd.RunE(uninstallCmd, []string{"tidymymac-uninstall-test-does-not-exist-app"})
	if err == nil {
		t.Fatal("expected a not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "--list") {
		t.Errorf("error = %v, want it to point at --list", err)
	}
}

// TestUninstallCmd_NoOutput_NotYetImplemented pins the deliberate scope
// boundary: without --output, this command is the interactive/TUI surface,
// which is Phase 5/7 work and out of scope here. This is checked before any
// discovery/resolution work, so the test needs no fixture and touches no
// filesystem beyond flag parsing.
func TestUninstallCmd_NoOutput_NotYetImplemented(t *testing.T) {
	resetUninstallFlags(t)
	uninstallCmd.SetContext(context.Background())

	err := uninstallCmd.RunE(uninstallCmd, []string{"Foo"})
	if err == nil {
		t.Fatal("expected a not-implemented error, got nil")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error = %v, want it to say interactive mode is not implemented", err)
	}
}

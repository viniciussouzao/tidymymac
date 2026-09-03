package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/elevate"
	"github.com/viniciussouzao/tidymymac/internal/history"
)

// ---------------------------------------------------------------------------
// Fixtures and helpers for the non-interactive (--output json) elevation path.
// ---------------------------------------------------------------------------

// isolateCleanRun gives a test its own HOME -- so history.Append and
// config.New's "~" expansion never touch the real user's files -- and installs
// the package-level loadedConfig that resolveSudoElevation/elevateForClean
// read. HOME must be set before withLoadedConfig, since config.New resolves the
// builtin protected paths against it.
func isolateCleanRun(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SUDO_USER", "")
	withLoadedConfig(t)
}

// captureStdout swaps os.Stdout for a pipe and returns a function that
// restores it and yields everything written in between. runCleanNonInteractive
// writes to os.Stdout directly (not through cobra's out writer), and the
// safety contract under test is "a refused run writes NOTHING to stdout", so
// the real fd has to be intercepted.
//
// The reader is drained on a goroutine so a payload larger than the pipe
// buffer can never deadlock the test.
func captureStdout(t *testing.T) func() string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	previous := os.Stdout
	os.Stdout = w

	drained := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		drained <- b.String()
	}()

	var (
		once sync.Once
		got  string
	)
	stop := func() string {
		once.Do(func() {
			os.Stdout = previous
			_ = w.Close()
			got = <-drained
			_ = r.Close()
		})
		return got
	}
	t.Cleanup(func() { stop() })
	return stop
}

// ttyStub counts how many times the terminal-detection seams were consulted,
// so a test can assert not just the answer they gave but that they were never
// asked at all (the dry-run contract).
type ttyStub struct {
	controllingCalls int
	stderrCalls      int
}

func (s *ttyStub) calls() int { return s.controllingCalls + s.stderrCalls }

func stubTerminals(t *testing.T, controllingTTY, stderrTTY bool) *ttyStub {
	t.Helper()
	s := &ttyStub{}
	prevTTY, prevErr := controllingTerminalAvailable, stderrIsTerminal
	controllingTerminalAvailable = func() bool { s.controllingCalls++; return controllingTTY }
	stderrIsTerminal = func() bool { s.stderrCalls++; return stderrTTY }
	t.Cleanup(func() {
		controllingTerminalAvailable = prevTTY
		stderrIsTerminal = prevErr
	})
	return s
}

// forbidElevation fails the test if anything reaches elevate.Invoke's seam.
// The recorded flag is checked as well as t.Error'd, because stubInvokeElevated
// runs on runClean's worker goroutines where t.Fatal would be unsafe.
func forbidElevation(t *testing.T, reason string) *bool {
	t.Helper()
	called := false
	stubInvokeElevated(t, func(context.Context, elevate.Plan) (elevate.Result, error) {
		called = true
		t.Errorf("invokeElevated must never be called: %s", reason)
		return elevate.Result{}, nil
	})
	return &called
}

// deletingSpyCleaner is a non-sudo cleaner that reports real (simulated)
// deletion counts. spyCleaner and wholeDomainSpyCleaner both report zero, which
// would leave the live leg invisible in totals and absent from history --
// buildRunRecord skips rows that reclaimed nothing.
type deletingSpyCleaner struct {
	category cleaner.Category
	entries  []cleaner.FileEntry

	scanned     bool
	cleanCalls  int
	cleanedWith []cleaner.FileEntry
	cleanDryRun bool
}

func (c *deletingSpyCleaner) Category() cleaner.Category { return c.category }
func (c *deletingSpyCleaner) Name() string               { return string(c.category) }
func (c *deletingSpyCleaner) Description() string        { return "deleting spy" }
func (c *deletingSpyCleaner) RequiresSudo() bool         { return false }
func (c *deletingSpyCleaner) DeletesWholeDomain() bool   { return false }

func (c *deletingSpyCleaner) Scan(context.Context, func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	c.scanned = true
	return &cleaner.ScanResult{Category: c.category, Entries: c.entries, TotalFiles: len(c.entries)}, nil
}

func (c *deletingSpyCleaner) Clean(_ context.Context, entries []cleaner.FileEntry, dryRun bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	c.cleanCalls++
	c.cleanDryRun = dryRun
	c.cleanedWith = append(c.cleanedWith, entries...)
	var freed int64
	for _, e := range entries {
		freed += e.Size
	}
	return &cleaner.CleanResult{Category: c.category, FilesDeleted: len(entries), BytesFreed: freed}, nil
}

func (c *deletingSpyCleaner) touched() bool { return c.scanned || c.cleanCalls > 0 }

// nameSplitCleaner is splitPrivilegeCleaner's --from-file counterpart: entries
// loaded from a scan file must be real on-disk paths (PrepareScanResultForClean
// revalidates them with os.Stat and drops anything missing), so the "needs
// root" marker moves from a "/sudo/" path prefix to a "sudo-" file-name prefix.
type nameSplitCleaner struct {
	category cleaner.Category

	cleanCalls  int
	cleanedWith []cleaner.FileEntry
}

func (c *nameSplitCleaner) Category() cleaner.Category { return c.category }
func (c *nameSplitCleaner) Name() string               { return string(c.category) }
func (c *nameSplitCleaner) Description() string        { return "name split spy" }
func (c *nameSplitCleaner) RequiresSudo() bool         { return true }
func (c *nameSplitCleaner) DeletesWholeDomain() bool   { return false }

func (c *nameSplitCleaner) NeedsSudo(entry cleaner.FileEntry) bool {
	return strings.HasPrefix(filepath.Base(entry.Path), "sudo-")
}

func (c *nameSplitCleaner) Scan(context.Context, func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	// Never reached in these tests: every use goes through --from-file. A
	// scan happening anyway is a bug the caller wants to hear about.
	return &cleaner.ScanResult{Category: c.category}, nil
}

func (c *nameSplitCleaner) Clean(_ context.Context, entries []cleaner.FileEntry, _ bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	c.cleanCalls++
	c.cleanedWith = append(c.cleanedWith, entries...)
	var freed int64
	for _, e := range entries {
		freed += e.Size
	}
	return &cleaner.CleanResult{Category: c.category, FilesDeleted: len(entries), BytesFreed: freed}, nil
}

// cleanJSONOutput mirrors commands.CleanOutput's wire shape. Decoding into a
// local struct rather than the production type keeps these tests asserting on
// the JSON contract scripts actually consume.
type cleanJSONOutput struct {
	Result struct {
		TotalFiles int   `json:"total_files"`
		TotalSize  int64 `json:"total_size_bytes"`
		HasErrors  bool  `json:"has_errors"`
		Categories []struct {
			Category     string `json:"category"`
			DeletedFiles int    `json:"deleted_files"`
			DeletedSize  int64  `json:"deleted_size_bytes"`
			Error        string `json:"error"`
		} `json:"categories"`
	} `json:"result"`
	Revalidation *struct {
		RevalidatedFiles int `json:"revalidated_files"`
		MissingFiles     int `json:"missing_files"`
	} `json:"revalidation"`
}

func decodeCleanJSON(t *testing.T, raw string) cleanJSONOutput {
	t.Helper()
	var out cleanJSONOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("stdout is not valid JSON (%v):\n%s", err, raw)
	}
	return out
}

func (o cleanJSONOutput) category(t *testing.T, name string) (deletedFiles int, deletedSize int64, errMsg string) {
	t.Helper()
	for _, c := range o.Result.Categories {
		if c.Category == name {
			return c.DeletedFiles, c.DeletedSize, c.Error
		}
	}
	t.Fatalf("category %q missing from JSON output: %+v", name, o.Result.Categories)
	return 0, 0, ""
}

// withStdin points os.Stdin at a temp file holding contents, so "--from-file -"
// can be exercised without a real pipe. A second read of the same fd returns
// EOF, which is exactly what makes "read exactly once" observable. The sudo
// gate deliberately checks /dev/tty rather than this fd, so consuming the scan
// from stdin remains compatible with an interactive password prompt.
func withStdin(t *testing.T, contents string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "scan.json")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatalf("write scan file: %v", err)
	}
	f, err := os.Open(p) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatalf("open scan file: %v", err)
	}
	previous := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = previous
		_ = f.Close()
	})
}

// errCleanRefused stands in for whatever policy error a caller's allowSudo
// hook returns; resolveSudoElevation must propagate it untouched.
var errCleanRefused = errors.New("refused by policy")

func historyRuns(t *testing.T) []history.RunRecord {
	t.Helper()
	record, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	return record.Runs
}

// ---------------------------------------------------------------------------
// 1. --prompt-sudo absent: refused before anything runs, stdout untouched.
// ---------------------------------------------------------------------------

func TestCleanJSON_SudoCategoryWithoutPromptSudoRefusesBeforeAnythingRuns(t *testing.T) {
	isolateCleanRun(t)
	withExecuteFlag(t, true)

	const sudoCat cleaner.Category = "auto_sudo_cat"
	sudo := &splitPrivilegeCleaner{
		category: sudoCat,
		entries:  []cleaner.FileEntry{{Path: "/sudo/a", Size: 10, Category: sudoCat}},
	}
	live := &deletingSpyCleaner{
		category: "auto_live_cat",
		entries:  []cleaner.FileEntry{{Path: "/live/a", Size: 25, Category: "auto_live_cat"}},
	}
	registry := cleaner.NewRegistry()
	registry.Register(sudo)
	registry.Register(live)

	forbidElevation(t, "the run must be refused before elevateForClean")
	stubTerminals(t, true, true)

	stdout := captureStdout(t)
	err := runCleanNonInteractive(
		context.Background(), registry,
		[]string{string(sudoCat), string(live.category)},
		false, "", false, "json", true, /*quiet*/
		false, /*promptSudo*/
	)
	out := stdout()

	if err == nil {
		t.Fatal("expected an error: a sudo category was selected without --prompt-sudo")
	}
	if !strings.Contains(err.Error(), "--prompt-sudo") {
		t.Errorf("error %q does not name the flag that would have allowed the run", err)
	}
	if !strings.Contains(err.Error(), "nothing was cleaned") {
		t.Errorf("error %q does not state that nothing was cleaned", err)
	}
	if !strings.Contains(err.Error(), sudoCat.DisplayName()) {
		t.Errorf("error %q does not name the category that required sudo", err)
	}

	// The sudo category may be scanned read-only so preparation can determine
	// whether any entry genuinely needs root. No cleaner may be asked to delete,
	// and the ordinary category must remain entirely untouched, so a script can
	// retry without wondering what already happened.
	if sudo.cleanCalls != 0 {
		t.Errorf("the sudo category's direct leg ran %d time(s), want 0", sudo.cleanCalls)
	}
	if live.touched() {
		t.Errorf("the non-sudo category was touched (scanned=%v, cleans=%d); the whole run must be refused, not partially executed", live.scanned, live.cleanCalls)
	}
	if out != "" {
		t.Errorf("stdout must stay empty on a refused run, got:\n%s", out)
	}
	if runs := historyRuns(t); len(runs) != 0 {
		t.Errorf("history has %d run(s), want 0: nothing was deleted", len(runs))
	}
}

func TestCleanJSON_DirectOnlySudoCategoryNeedsNoPromptOrTerminal(t *testing.T) {
	isolateCleanRun(t)
	withExecuteFlag(t, true)

	const cat cleaner.Category = "auto_direct_only_sudo_cat"
	c := &splitPrivilegeCleaner{
		category: cat,
		entries:  []cleaner.FileEntry{{Path: "/direct/a", Size: 7, Category: cat}},
	}
	registry := cleaner.NewRegistry()
	registry.Register(c)

	elevated := forbidElevation(t, "the prepared plan contains no entry that genuinely needs sudo")
	tty := stubTerminals(t, false, false)
	stdout := captureStdout(t)
	err := runCleanNonInteractive(context.Background(), registry, []string{string(cat)}, false, "", false, "json", true, false)
	out := stdout()

	if err != nil {
		t.Fatalf("runCleanNonInteractive: %v", err)
	}
	if *elevated {
		t.Fatal("direct-only work reached elevation")
	}
	if tty.calls() != 0 {
		t.Fatalf("terminal gate was consulted %d time(s), want 0 for an empty elevated plan", tty.calls())
	}
	if c.cleanCalls != 1 || len(c.cleanedWith) != 1 {
		t.Fatalf("direct clean = %d calls / %d entries, want 1/1", c.cleanCalls, len(c.cleanedWith))
	}
	decoded := decodeCleanJSON(t, out)
	files, size, errMsg := decoded.category(t, string(cat))
	if files != 1 || size != 7 || errMsg != "" {
		t.Fatalf("category = %d files / %d bytes / error %q, want 1/7/no error", files, size, errMsg)
	}
	if runs := historyRuns(t); len(runs) != 1 || runs[0].TotalFiles != 1 {
		t.Fatalf("history = %+v, want the direct deletion recorded once", runs)
	}
}

func TestCleanJSON_EmptySudoCategoryCreatesNeitherPromptNorHistory(t *testing.T) {
	isolateCleanRun(t)
	withExecuteFlag(t, true)

	const cat cleaner.Category = "auto_empty_sudo_cat"
	c := &splitPrivilegeCleaner{category: cat}
	registry := cleaner.NewRegistry()
	registry.Register(c)

	elevated := forbidElevation(t, "an empty category has no elevated plan")
	tty := stubTerminals(t, false, false)
	stdout := captureStdout(t)
	err := runCleanNonInteractive(context.Background(), registry, []string{string(cat)}, false, "", false, "json", true, false)
	out := stdout()

	if err != nil {
		t.Fatalf("runCleanNonInteractive: %v", err)
	}
	if *elevated || c.cleanCalls != 0 {
		t.Fatalf("empty work executed: elevated=%v cleanCalls=%d", *elevated, c.cleanCalls)
	}
	if tty.calls() != 0 {
		t.Fatalf("terminal gate was consulted %d time(s), want 0", tty.calls())
	}
	decoded := decodeCleanJSON(t, out)
	files, size, errMsg := decoded.category(t, string(cat))
	if files != 0 || size != 0 || errMsg != "" {
		t.Fatalf("empty category = %d/%d/%q", files, size, errMsg)
	}
	if runs := historyRuns(t); len(runs) != 0 {
		t.Fatalf("history = %+v, want no empty run", runs)
	}
}

// ---------------------------------------------------------------------------
// 2. --prompt-sudo present but no usable prompt terminal is available.
// ---------------------------------------------------------------------------

func TestCleanJSON_PromptSudoWithoutTerminalRefusesBeforeAnythingRuns(t *testing.T) {
	cases := []struct {
		name           string
		controllingTTY bool
		stderrTTY      bool
	}{
		{name: "no controlling terminal", controllingTTY: false, stderrTTY: true},
		{name: "stderr is redirected", controllingTTY: true, stderrTTY: false},
		{name: "neither is a terminal", controllingTTY: false, stderrTTY: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCleanRun(t)
			withExecuteFlag(t, true)

			const sudoCat cleaner.Category = "auto_sudo_cat"
			sudo := &splitPrivilegeCleaner{
				category: sudoCat,
				entries:  []cleaner.FileEntry{{Path: "/sudo/a", Size: 10, Category: sudoCat}},
			}
			live := &deletingSpyCleaner{
				category: "auto_live_cat",
				entries:  []cleaner.FileEntry{{Path: "/live/a", Size: 25, Category: "auto_live_cat"}},
			}
			registry := cleaner.NewRegistry()
			registry.Register(sudo)
			registry.Register(live)

			forbidElevation(t, "a missing controlling terminal or redirected stderr must never reach a password prompt")
			stubTerminals(t, tc.controllingTTY, tc.stderrTTY)

			stdout := captureStdout(t)
			err := runCleanNonInteractive(
				context.Background(), registry,
				[]string{string(sudoCat), string(live.category)},
				false, "", false, "json", true, /*quiet*/
				true, /*promptSudo*/
			)
			out := stdout()

			if err == nil {
				t.Fatal("expected an error: --prompt-sudo cannot prompt without a terminal")
			}
			if !strings.Contains(err.Error(), "controlling terminal") {
				t.Errorf("error %q does not explain that a usable prompt terminal is unavailable", err)
			}
			if sudo.cleanCalls != 0 || live.touched() {
				t.Errorf("nothing may run: sudo direct cleans=%d, live touched=%v", sudo.cleanCalls, live.touched())
			}
			if out != "" {
				t.Errorf("stdout must stay empty on a refused run, got:\n%s", out)
			}
			if runs := historyRuns(t); len(runs) != 0 {
				t.Errorf("history has %d run(s), want 0", len(runs))
			}
		})
	}
}

// TestCleanJSON_RefusalMessagesDistinguishMissingFlagFromMissingTerminal pins
// that the two refusals are actionable in different ways: one is fixed by
// adding --prompt-sudo, the other by not fixing it that way at all. A single
// shared message would send a script author down the wrong path.
func TestCleanJSON_RefusalMessagesDistinguishMissingFlagFromMissingTerminal(t *testing.T) {
	const sudoCat cleaner.Category = "auto_sudo_cat"

	run := func(t *testing.T, promptSudo, tty bool) error {
		t.Helper()
		isolateCleanRun(t)
		withExecuteFlag(t, true)

		registry := cleaner.NewRegistry()
		registry.Register(&splitPrivilegeCleaner{
			category: sudoCat,
			entries:  []cleaner.FileEntry{{Path: "/sudo/a", Size: 10, Category: sudoCat}},
		})
		forbidElevation(t, "both variants must be refused before elevation")
		stubTerminals(t, tty, tty)

		stdout := captureStdout(t)
		err := runCleanNonInteractive(context.Background(), registry, []string{string(sudoCat)}, false, "", false, "json", true, promptSudo)
		if out := stdout(); out != "" {
			t.Errorf("stdout must stay empty, got:\n%s", out)
		}
		return err
	}

	var missingFlag, missingTTY error
	t.Run("no flag", func(t *testing.T) { missingFlag = run(t, false, true) })
	t.Run("no terminal", func(t *testing.T) { missingTTY = run(t, true, false) })

	if missingFlag == nil || missingTTY == nil {
		t.Fatalf("both variants must fail; got %v and %v", missingFlag, missingTTY)
	}
	if missingFlag.Error() == missingTTY.Error() {
		t.Fatalf("both refusals produced the same message %q; they need different remedies", missingFlag)
	}
	if strings.Contains(missingTTY.Error(), "--prompt-sudo was not given") {
		t.Errorf("the TTY refusal %q wrongly blames the missing flag", missingTTY)
	}
	if strings.Contains(missingFlag.Error(), "not a terminal") {
		t.Errorf("the missing-flag refusal %q wrongly blames the terminal", missingFlag)
	}
}

// ---------------------------------------------------------------------------
// 3. Happy path: elevated leg and live remainder both land in output+history.
// ---------------------------------------------------------------------------

func TestCleanJSON_PromptSudoOnTerminalMergesElevatedAndLiveLegs(t *testing.T) {
	isolateCleanRun(t)
	withExecuteFlag(t, true)

	const sudoCat cleaner.Category = "auto_sudo_cat"
	sudo := &splitPrivilegeCleaner{
		category: sudoCat,
		entries: []cleaner.FileEntry{
			{Path: "/sudo/a", Size: 10, Category: sudoCat},
			{Path: "/direct/b", Size: 5, Category: sudoCat},
		},
	}
	live := &deletingSpyCleaner{
		category: "auto_live_cat",
		entries: []cleaner.FileEntry{
			{Path: "/live/a", Size: 15, Category: "auto_live_cat"},
			{Path: "/live/b", Size: 10, Category: "auto_live_cat"},
		},
	}
	registry := cleaner.NewRegistry()
	registry.Register(sudo)
	registry.Register(live)

	var gotPlan elevate.Plan
	invoked := 0
	stubInvokeElevated(t, func(_ context.Context, plan elevate.Plan) (elevate.Result, error) {
		invoked++
		gotPlan = plan
		if sudo.cleanCalls != 0 || live.touched() {
			t.Errorf("direct/live work started before elevation completed: direct=%d live=%v", sudo.cleanCalls, live.touched())
		}
		return elevate.Result{
			Clean: commands.CleanResult{
				Categories: []commands.CleanCategoryResult{
					{Category: sudoCat, Name: sudoCat.DisplayName(), DeletedFiles: 1, DeletedSize: 10},
				},
			},
		}, nil
	})
	tty := stubTerminals(t, true, true)

	stdout := captureStdout(t)
	err := runCleanNonInteractive(
		context.Background(), registry,
		[]string{string(sudoCat), string(live.category)},
		false, "", false, "json", true, /*quiet*/
		true, /*promptSudo*/
	)
	out := stdout()
	if err != nil {
		t.Fatalf("runCleanNonInteractive: %v", err)
	}

	if invoked != 1 {
		t.Fatalf("invokeElevated called %d time(s), want exactly 1", invoked)
	}
	if tty.controllingCalls == 0 || tty.stderrCalls == 0 {
		t.Errorf("both terminal seams must be consulted before prompting, got controlling=%d stderr=%d", tty.controllingCalls, tty.stderrCalls)
	}
	if len(gotPlan.Categories) != 1 || len(gotPlan.Categories[0].Entries) != 1 {
		t.Fatalf("plan = %+v, want only the single /sudo/ entry", gotPlan.Categories)
	}
	if !live.scanned || live.cleanCalls != 1 {
		t.Errorf("the non-sudo remainder must still run live: scanned=%v cleans=%d", live.scanned, live.cleanCalls)
	}

	decoded := decodeCleanJSON(t, out)
	sudoFiles, sudoSize, sudoErr := decoded.category(t, string(sudoCat))
	if sudoFiles != 2 || sudoSize != 15 {
		t.Errorf("sudo category = %d files / %d bytes, want 2 / 15 (1+10 elevated, 1+5 direct)", sudoFiles, sudoSize)
	}
	if sudoErr != "" {
		t.Errorf("sudo category carries error %q, want none", sudoErr)
	}
	liveFiles, liveSize, _ := decoded.category(t, string(live.category))
	if liveFiles != 2 || liveSize != 25 {
		t.Errorf("live category = %d files / %d bytes, want 2 / 25", liveFiles, liveSize)
	}
	if decoded.Result.TotalFiles != 4 || decoded.Result.TotalSize != 40 {
		t.Errorf("totals = %d files / %d bytes, want 4 / 40 (both legs merged)", decoded.Result.TotalFiles, decoded.Result.TotalSize)
	}
	if decoded.Result.HasErrors {
		t.Error("has_errors = true, want false")
	}

	// Two separate records, exactly as the interactive path produces: the
	// elevated leg is recorded synchronously inside resolveSudoElevation so an
	// interrupted or failed remainder can never cost it its audit trail, and
	// the live remainder records its own afterwards.
	runs := historyRuns(t)
	if len(runs) != 2 {
		t.Fatalf("history has %d run(s), want 2 (elevated leg + live remainder): %+v", len(runs), runs)
	}
	recorded := map[string]history.CategoryRecord{}
	for _, run := range runs {
		if len(run.Categories) != 1 {
			t.Fatalf("run %+v should record exactly one category", run)
		}
		recorded[run.Categories[0].Name] = run.Categories[0]
	}
	if got := recorded[string(sudoCat)]; got.Files != 2 || got.Bytes != 15 {
		t.Errorf("elevated record = %+v, want 2 files / 15 bytes", got)
	}
	if got := recorded[string(live.category)]; got.Files != 2 || got.Bytes != 25 {
		t.Errorf("live record = %+v, want 2 files / 25 bytes", got)
	}
}

// ---------------------------------------------------------------------------
// 4. Elevated failure aborts before any direct/live deletion.
// ---------------------------------------------------------------------------

func TestCleanJSON_ElevatedFailurePreservesAllOrNothing(t *testing.T) {
	cases := []struct {
		name      string
		invokeErr error
	}{
		{name: "elevation failed", invokeErr: elevate.ErrElevationFailed},
		{name: "outcome unknown", invokeErr: elevate.ErrElevationOutcomeUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCleanRun(t)
			withExecuteFlag(t, true)

			const sudoCat cleaner.Category = "auto_sudo_cat"
			sudo := &splitPrivilegeCleaner{
				category: sudoCat,
				entries: []cleaner.FileEntry{
					{Path: "/sudo/a", Size: 10, Category: sudoCat},
					{Path: "/direct/b", Size: 5, Category: sudoCat},
				},
			}
			live := &deletingSpyCleaner{
				category: "auto_live_cat",
				entries: []cleaner.FileEntry{
					{Path: "/live/a", Size: 15, Category: "auto_live_cat"},
					{Path: "/live/b", Size: 10, Category: "auto_live_cat"},
				},
			}
			registry := cleaner.NewRegistry()
			registry.Register(sudo)
			registry.Register(live)

			stubInvokeElevated(t, func(context.Context, elevate.Plan) (elevate.Result, error) {
				return elevate.Result{}, tc.invokeErr
			})
			stubTerminals(t, true, true)

			stdout := captureStdout(t)
			err := runCleanNonInteractive(
				context.Background(), registry,
				[]string{string(sudoCat), string(live.category)},
				false, "", false, "json", true, true,
			)
			out := stdout()

			// Failure to establish a completed privileged leg aborts before
			// any direct or ordinary cleaner starts. Outcome-unknown may mean
			// the helper itself partially ran, but this process must not widen
			// that uncertainty by starting more deletions afterwards.
			if err == nil {
				t.Fatal("expected a non-nil error: the elevated leg failed")
			}
			if !errors.Is(err, tc.invokeErr) {
				t.Errorf("error = %v, want it to preserve %v", err, tc.invokeErr)
			}
			if out != "" {
				t.Errorf("stdout must stay empty when orchestration aborts before the ordinary run, got:\n%s", out)
			}
			if sudo.cleanCalls != 0 {
				t.Errorf("the sudo category's direct leg ran %d time(s), want 0", sudo.cleanCalls)
			}
			if live.touched() {
				t.Errorf("the ordinary category was touched after elevation failure: scanned=%v cleanCalls=%d", live.scanned, live.cleanCalls)
			}
			if runs := historyRuns(t); len(runs) != 0 {
				t.Errorf("history has %d run(s), want 0 because this process observed no completed deletion: %+v", len(runs), runs)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5. Dry run is completely unaffected by the elevation contract.
// ---------------------------------------------------------------------------

func TestCleanJSON_DryRunNeverElevatesOrChecksTheTerminal(t *testing.T) {
	cases := []struct {
		name       string
		promptSudo bool
	}{
		{name: "without --prompt-sudo", promptSudo: false},
		{name: "with --prompt-sudo", promptSudo: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCleanRun(t)
			withExecuteFlag(t, false) // dry run

			const sudoCat cleaner.Category = "auto_sudo_cat"
			sudo := &splitPrivilegeCleaner{
				category: sudoCat,
				entries:  []cleaner.FileEntry{{Path: "/sudo/a", Size: 10, Category: sudoCat}},
			}
			live := &deletingSpyCleaner{
				category: "auto_live_cat",
				entries:  []cleaner.FileEntry{{Path: "/live/a", Size: 25, Category: "auto_live_cat"}},
			}
			registry := cleaner.NewRegistry()
			registry.Register(sudo)
			registry.Register(live)

			elevated := forbidElevation(t, "a dry run deletes nothing, so it must never elevate")
			tty := stubTerminals(t, true, true)

			stdout := captureStdout(t)
			err := runCleanNonInteractive(
				context.Background(), registry,
				[]string{string(sudoCat), string(live.category)},
				false, "", false, "json", true, tc.promptSudo,
			)
			out := stdout()
			if err != nil {
				t.Fatalf("runCleanNonInteractive: %v", err)
			}

			if *elevated {
				t.Error("a dry run must never reach elevate.Invoke")
			}
			// The terminal seams are the observable proxy for "allowSudo was
			// consulted". A dry run must not even ask the question: there is
			// nothing to authorize.
			if tty.calls() != 0 {
				t.Errorf("terminal detection was consulted %d time(s) during a dry run, want 0", tty.calls())
			}

			decoded := decodeCleanJSON(t, out)
			sudoFiles, sudoSize, _ := decoded.category(t, string(sudoCat))
			if sudoFiles != 1 || sudoSize != 10 {
				t.Errorf("sudo category = %d files / %d bytes, want 1 / 10 previewed through the ordinary live path", sudoFiles, sudoSize)
			}
			liveFiles, liveSize, _ := decoded.category(t, string(live.category))
			if liveFiles != 1 || liveSize != 25 {
				t.Errorf("live category = %d files / %d bytes, want 1 / 25", liveFiles, liveSize)
			}
			if decoded.Result.TotalFiles != 2 || decoded.Result.TotalSize != 35 {
				t.Errorf("totals = %d files / %d bytes, want 2 / 35", decoded.Result.TotalFiles, decoded.Result.TotalSize)
			}
			if !live.cleanDryRun {
				t.Error("the live cleaner was called with dryRun=false during a dry run")
			}
			if runs := historyRuns(t); len(runs) != 0 {
				t.Errorf("history has %d run(s), want 0: a dry run deletes nothing", len(runs))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 6. --from-file is read exactly once across the refactor.
// ---------------------------------------------------------------------------

// writeScanFileEntry creates a real file (revalidation drops anything os.Stat
// cannot find) and returns the matching scan-file entry.
func writeScanFileEntry(t *testing.T, dir, name string, size int, category cleaner.Category) cleaner.FileEntry {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return cleaner.FileEntry{Path: p, Size: int64(size), Category: category}
}

func TestCleanJSON_FromFileStdinIsReadExactlyOnce(t *testing.T) {
	const sudoCat cleaner.Category = "auto_file_sudo_cat"
	const liveCat cleaner.Category = "auto_file_live_cat"

	cases := []struct {
		name             string
		includeLive      bool
		wantTotalFiles   int
		wantTotalSize    int64
		wantRevalidated  int
		wantLiveCleaned  bool
		wantInvokedTimes int
	}{
		{
			// Only sudo categories: skipLiveRun is taken, and the prepared
			// scan must still be reported (it was loaded, just not re-run).
			name:             "scan contains only sudo categories",
			includeLive:      false,
			wantTotalFiles:   2,
			wantTotalSize:    15,
			wantRevalidated:  2,
			wantLiveCleaned:  false,
			wantInvokedTimes: 1,
		},
		{
			// The interesting regression shape: the live remainder runs after
			// the sudo leg. If it re-read "--from-file -" it would hit EOF on
			// the already-consumed stdin and fail the whole run.
			name:             "scan contains sudo and non-sudo categories",
			includeLive:      true,
			wantTotalFiles:   3,
			wantTotalSize:    22,
			wantRevalidated:  3,
			wantLiveCleaned:  true,
			wantInvokedTimes: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCleanRun(t)
			withExecuteFlag(t, true)

			dir := t.TempDir()
			sudoEntry := writeScanFileEntry(t, dir, "sudo-a", 10, sudoCat)
			directEntry := writeScanFileEntry(t, dir, "direct-b", 5, sudoCat)

			sudo := &nameSplitCleaner{category: sudoCat}
			live := &deletingSpyCleaner{category: liveCat}
			// A whole-domain cleaner the scan file never mentions must never
			// run at all: an empty entry list reads as "clear everything" to
			// it, and the empty selection below expands only to the scan
			// file's own categories.
			brew := &wholeDomainSpyCleaner{category: "auto_file_brew"}
			registry := cleaner.NewRegistry()
			registry.Register(sudo)
			registry.Register(live)
			registry.Register(brew)

			scan := commands.ScanResult{
				Categories: []commands.ScanCategoryResult{{
					Category:   sudoCat,
					Name:       sudoCat.DisplayName(),
					TotalFiles: 2,
					Files:      []cleaner.FileEntry{sudoEntry, directEntry},
				}},
			}
			if tc.includeLive {
				liveEntry := writeScanFileEntry(t, dir, "live-c", 7, liveCat)
				scan.Categories = append(scan.Categories, commands.ScanCategoryResult{
					Category:   liveCat,
					Name:       liveCat.DisplayName(),
					TotalFiles: 1,
					Files:      []cleaner.FileEntry{liveEntry},
				})
			}
			raw, err := json.Marshal(scan)
			if err != nil {
				t.Fatalf("marshal scan: %v", err)
			}
			withStdin(t, string(raw))

			invoked := 0
			stubInvokeElevated(t, func(_ context.Context, plan elevate.Plan) (elevate.Result, error) {
				invoked++
				if len(plan.Categories) != 1 || len(plan.Categories[0].Entries) != 1 {
					t.Errorf("plan = %+v, want only the single sudo- entry", plan.Categories)
				}
				return elevate.Result{
					Clean: commands.CleanResult{
						Categories: []commands.CleanCategoryResult{
							{Category: sudoCat, Name: sudoCat.DisplayName(), DeletedFiles: 1, DeletedSize: 10},
						},
					},
				}, nil
			})
			stubTerminals(t, true, true)

			stdout := captureStdout(t)
			// Empty selection on purpose: it must expand from the scan file,
			// which is only possible if the file was loaded exactly once and
			// reused for both legs.
			runErr := runCleanNonInteractive(
				context.Background(), registry, nil,
				false, "-", false, "json", true, true,
			)
			out := stdout()
			if runErr != nil {
				t.Fatalf("runCleanNonInteractive: %v (a second read of --from-file - would surface here as an EOF/decode error)", runErr)
			}

			if invoked != tc.wantInvokedTimes {
				t.Errorf("invokeElevated called %d time(s), want %d", invoked, tc.wantInvokedTimes)
			}
			if brew.cleaned {
				t.Errorf("the whole-domain cleaner ran with entries=%v; it is absent from the scan file and must never run", brew.cleanedWith)
			}
			if got := live.cleanCalls > 0; got != tc.wantLiveCleaned {
				t.Errorf("live cleaner cleaned = %v, want %v", got, tc.wantLiveCleaned)
			}
			if live.scanned {
				t.Error("the live leg must reuse the prepared scan, never re-Scan the cleaner")
			}

			decoded := decodeCleanJSON(t, out)
			if decoded.Result.TotalFiles != tc.wantTotalFiles || decoded.Result.TotalSize != tc.wantTotalSize {
				t.Errorf("totals = %d files / %d bytes, want %d / %d", decoded.Result.TotalFiles, decoded.Result.TotalSize, tc.wantTotalFiles, tc.wantTotalSize)
			}
			if decoded.Revalidation == nil {
				t.Fatal("revalidation summary missing: the prepared scan must be reported even when every category was elevated")
			}
			if decoded.Revalidation.RevalidatedFiles != tc.wantRevalidated {
				t.Errorf("revalidated_files = %d, want %d", decoded.Revalidation.RevalidatedFiles, tc.wantRevalidated)
			}
			if decoded.Revalidation.MissingFiles != 0 {
				t.Errorf("missing_files = %d, want 0", decoded.Revalidation.MissingFiles)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 7. resolveSudoElevation's allowSudo gate, in isolation.
// ---------------------------------------------------------------------------

func TestResolveSudoElevation_AllowSudoGate(t *testing.T) {
	const sudoCat cleaner.Category = "gate_sudo_cat"
	const liveCat cleaner.Category = "gate_live_cat"

	newRegistry := func() (*cleaner.Registry, *splitPrivilegeCleaner, *deletingSpyCleaner) {
		sudo := &splitPrivilegeCleaner{
			category: sudoCat,
			entries:  []cleaner.FileEntry{{Path: "/sudo/a", Size: 10, Category: sudoCat}},
		}
		live := &deletingSpyCleaner{
			category: liveCat,
			entries:  []cleaner.FileEntry{{Path: "/live/a", Size: 25, Category: liveCat}},
		}
		registry := cleaner.NewRegistry()
		registry.Register(sudo)
		registry.Register(live)
		return registry, sudo, live
	}

	t.Run("dry run never asks, even with a sudo category selected", func(t *testing.T) {
		isolateCleanRun(t)
		registry, sudo, _ := newRegistry()
		forbidElevation(t, "dry run")

		var asked [][]string
		outcome, err := resolveSudoElevation(context.Background(), registry, []string{string(sudoCat), string(liveCat)}, "", false, true, func(names []string) error {
			asked = append(asked, names)
			return nil
		})
		if err != nil {
			t.Fatalf("resolveSudoElevation: %v", err)
		}
		if len(asked) != 0 {
			t.Errorf("allowSudo called %d time(s) during a dry run, want 0 (asked with %v)", len(asked), asked)
		}
		if sudo.cleanCalls != 0 {
			t.Errorf("the sudo cleaner's direct leg ran %d time(s) during a dry run, want 0", sudo.cleanCalls)
		}
		if outcome.skipLiveRun {
			t.Error("skipLiveRun = true, want false: a dry run leaves every category to the live run")
		}
		if len(outcome.nonSudoCategories) != 2 {
			t.Errorf("nonSudoCategories = %v, want both categories untouched by the split", outcome.nonSudoCategories)
		}
		if len(outcome.preResolved) != 0 {
			t.Errorf("preResolved = %+v, want empty", outcome.preResolved)
		}
	})

	t.Run("an all-non-sudo selection never asks", func(t *testing.T) {
		isolateCleanRun(t)
		registry, _, _ := newRegistry()
		forbidElevation(t, "no sudo category was selected")

		calls := 0
		outcome, err := resolveSudoElevation(context.Background(), registry, []string{string(liveCat)}, "", false, false, func([]string) error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("resolveSudoElevation: %v", err)
		}
		if calls != 0 {
			t.Errorf("allowSudo called %d time(s) with an empty sudo selection, want 0", calls)
		}
		if outcome.skipLiveRun {
			t.Error("skipLiveRun = true, want false")
		}
	})

	t.Run("execute with a sudo category asks exactly once with the sudo names", func(t *testing.T) {
		isolateCleanRun(t)
		registry, _, _ := newRegistry()
		stubInvokeElevated(t, func(context.Context, elevate.Plan) (elevate.Result, error) {
			return elevate.Result{Clean: commands.CleanResult{
				Categories: []commands.CleanCategoryResult{{Category: sudoCat, Name: sudoCat.DisplayName(), DeletedFiles: 1, DeletedSize: 10}},
			}}, nil
		})

		var asked [][]string
		outcome, err := resolveSudoElevation(context.Background(), registry, []string{string(sudoCat), string(liveCat)}, "", false, false, func(names []string) error {
			asked = append(asked, append([]string(nil), names...))
			return nil
		})
		if err != nil {
			t.Fatalf("resolveSudoElevation: %v", err)
		}
		if len(asked) != 1 {
			t.Fatalf("allowSudo called %d time(s), want exactly 1: %v", len(asked), asked)
		}
		if len(asked[0]) != 1 || asked[0][0] != string(sudoCat) {
			t.Errorf("allowSudo received %v, want only [%s]: a non-sudo category must never be part of the consent request", asked[0], sudoCat)
		}
		if len(outcome.nonSudoCategories) != 1 || outcome.nonSudoCategories[0] != string(liveCat) {
			t.Errorf("nonSudoCategories = %v, want [%s]", outcome.nonSudoCategories, liveCat)
		}
		if len(outcome.preResolved) != 1 {
			t.Fatalf("preResolved = %+v, want the elevated category's result", outcome.preResolved)
		}
	})

	t.Run("a refusing allowSudo aborts before anything is deleted", func(t *testing.T) {
		isolateCleanRun(t)
		registry, sudo, live := newRegistry()
		elevated := forbidElevation(t, "allowSudo refused")

		refusal := errCleanRefused
		_, err := resolveSudoElevation(context.Background(), registry, []string{string(sudoCat), string(liveCat)}, "", false, false, func([]string) error {
			return refusal
		})
		if err == nil {
			t.Fatal("expected allowSudo's error to abort resolveSudoElevation")
		}
		if err != refusal { //nolint:errorlint // the hook's error must reach the caller unwrapped
			t.Errorf("error = %v, want allowSudo's own error propagated unchanged", err)
		}
		if *elevated {
			t.Error("elevate.Invoke ran despite allowSudo refusing")
		}
		if sudo.cleanCalls != 0 {
			t.Errorf("the sudo category's direct leg ran %d time(s) despite the refusal, want 0", sudo.cleanCalls)
		}
		if live.touched() {
			t.Error("a non-sudo category was touched despite the refusal")
		}
		if runs := historyRuns(t); len(runs) != 0 {
			t.Errorf("history has %d run(s), want 0", len(runs))
		}
	})
}

package elevate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
)

// TestElevateHelperProcess is not a real test: it is the stub process
// Invoke's tests exec instead of sudo (the os/exec helper-process pattern).
// Running actual sudo here would block forever on a password prompt.
//
// Behavior is driven by the environment the stub is launched with:
//
//	TIDYMYMAC_TEST_HELPER=1        marks the process as the stub
//	TIDYMYMAC_TEST_HELPER_MODE     result | invalid | silent | fail
//	TIDYMYMAC_TEST_HELPER_ARGV     file to record os.Args into
//	TIDYMYMAC_TEST_HELPER_PLAN     file to record the plan file's mode/dir mode
//
// The same stub also stands in for the "sudo -v" authentication step, which
// Invoke runs before the helper:
//
//	TIDYMYMAC_TEST_AUTH=1          marks the process as the auth stub
//	TIDYMYMAC_TEST_AUTH_MODE       ok (default) | fail
//	TIDYMYMAC_TEST_AUTH_MARK       file to create so tests can prove auth ran
func TestElevateHelperProcess(t *testing.T) {
	if os.Getenv("TIDYMYMAC_TEST_AUTH") == "1" {
		if mark := os.Getenv("TIDYMYMAC_TEST_AUTH_MARK"); mark != "" {
			_ = os.WriteFile(mark, []byte("authenticated\n"), 0o600)
		}
		if os.Getenv("TIDYMYMAC_TEST_AUTH_MODE") == "fail" {
			// Wrong password / cancelled prompt: sudo's own exit code.
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("TIDYMYMAC_TEST_HELPER") != "1" {
		t.Skip("not the helper process")
	}

	args := os.Args
	if argvFile := os.Getenv("TIDYMYMAC_TEST_HELPER_ARGV"); argvFile != "" {
		// Record only what came after the "--" separator the stub is invoked
		// with, i.e. the argv Invoke actually built.
		for i, a := range args {
			if a == "--" {
				args = args[i+1:]
				break
			}
		}
		_ = os.WriteFile(argvFile, []byte(strings.Join(args, "\n")), 0o600)
	}

	// Observe the plan file exactly as the real helper would see it: while the
	// child is running, before the parent's deferred cleanup.
	if planReport := os.Getenv("TIDYMYMAC_TEST_HELPER_PLAN"); planReport != "" {
		planPath := args[len(args)-1]
		report := map[string]string{"path": planPath}
		if info, err := os.Stat(planPath); err == nil {
			report["file_mode"] = info.Mode().Perm().String()
		} else {
			report["file_error"] = err.Error()
		}
		if info, err := os.Stat(filepath.Dir(planPath)); err == nil {
			report["dir_mode"] = info.Mode().Perm().String()
		}
		data, _ := json.Marshal(report)
		_ = os.WriteFile(planReport, data, 0o600)
	}

	switch os.Getenv("TIDYMYMAC_TEST_HELPER_MODE") {
	case "invalid":
		os.Stdout.WriteString("not json at all\n")
	case "silent":
		// Exits 0 with no output: violates the contract, must be reported as
		// "helper never ran".
	case "partial":
		// Got past the guards, deleted things, then failed to write the whole
		// Result: exit 0 with truncated JSON. Must NOT be reported as "nothing
		// was deleted".
		os.Stdout.WriteString(`{"version":1,"clean":{"total_files":2,`)
	case "fail":
		// Exit 1 with nothing on stdout. Before authentication was split out
		// this was read as "sudo rejected the password"; now that the helper
		// only runs after a proven auth, it is what cobra or the Go runtime
		// exit with on an error AFTER the clean, and must be unknown.
		os.Exit(1)
	case "guard-rejected":
		// The helper's dedicated pre-deletion guard channel.
		os.Stderr.WriteString("guard rejected the plan\n")
		os.Exit(HelperGuardRejectedExitCode)
	case "crash":
		// Any other abnormal exit: could have died mid-clean.
		os.Exit(9)
	case "sleep":
		// Outlives the parent's context so cancellation can be exercised.
		time.Sleep(30 * time.Second)
	case "wrong-version":
		_ = json.NewEncoder(os.Stdout).Encode(Result{Version: ResultSchemaVersion + 1})
	default:
		_ = json.NewEncoder(os.Stdout).Encode(Result{
			Version: ResultSchemaVersion,
			Clean: commands.CleanResult{
				TotalFiles: 2,
				TotalSize:  1024,
			},
			Intersections: []CategoryIntersection{{
				Category: cleaner.CategoryTemp,
				Approved: 3,
				Matched:  2,
				Missing:  1,
			}},
		})
	}
	os.Exit(0)
}

// stubSudo swaps the sudoCommand and sudoAuthCommand seams for the stub
// process above, restoring them afterwards. Tests using it must not run in
// parallel: the seams are package level precisely so they stay a single,
// obvious, minimal hook. Auth-stub behavior is driven by the same env map
// (TIDYMYMAC_TEST_AUTH_* keys); by default it succeeds.
func stubSudo(t *testing.T, env map[string]string) {
	t.Helper()

	originalSudo, originalAuth := sudoCommand, sudoAuthCommand
	t.Cleanup(func() { sudoCommand, sudoAuthCommand = originalSudo, originalAuth })

	sudoAuthCommand = func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestElevateHelperProcess", "--", "-v")
		cmd.Env = append(os.Environ(), "TIDYMYMAC_TEST_AUTH=1")
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		return cmd
	}

	sudoCommand = func(ctx context.Context, exePath, planPath string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0],
			"-test.run=TestElevateHelperProcess", "--",
			exePath, HelperCommandName, planFileFlag, planPath)
		cmd.Env = append(os.Environ(), "TIDYMYMAC_TEST_HELPER=1")
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		return cmd
	}
}

func invokablePlan() Plan {
	return Plan{
		Categories: []PlanCategory{{
			Category: cleaner.CategoryTemp,
			Entries:  []cleaner.FileEntry{{Path: "/tmp/example", Size: 4}},
		}},
	}
}

func TestInvokeParsesHelperResult(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	planFile := filepath.Join(dir, "plan-report")

	stubSudo(t, map[string]string{
		"TIDYMYMAC_TEST_HELPER_ARGV": argvFile,
		"TIDYMYMAC_TEST_HELPER_PLAN": planFile,
	})

	result, err := Invoke(context.Background(), invokablePlan())
	if err != nil {
		t.Fatalf("Invoke() error: %v", err)
	}

	if result.Version != ResultSchemaVersion {
		t.Fatalf("result version = %d, want %d", result.Version, ResultSchemaVersion)
	}
	if result.Clean.TotalFiles != 2 || result.Clean.TotalSize != 1024 {
		t.Fatalf("result clean = %+v, want the helper's payload", result.Clean)
	}
	if len(result.Intersections) != 1 || result.Intersections[0].Missing != 1 {
		t.Fatalf("intersections = %+v, want the helper's payload", result.Intersections)
	}

	// --- argv shape ---
	argv := strings.Split(strings.TrimSpace(readFile(t, argvFile)), "\n")
	if len(argv) != 4 {
		t.Fatalf("argv = %v, want 4 elements", argv)
	}
	if !filepath.IsAbs(argv[0]) {
		t.Fatalf("argv[0] = %q, want an absolute path to the running binary", argv[0])
	}
	if argv[1] != HelperCommandName {
		t.Fatalf("argv[1] = %q, want %q", argv[1], HelperCommandName)
	}
	if argv[2] != planFileFlag {
		t.Fatalf("argv[2] = %q, want %q", argv[2], planFileFlag)
	}

	// --- plan file as the child saw it ---
	var report map[string]string
	if err := json.Unmarshal([]byte(readFile(t, planFile)), &report); err != nil {
		t.Fatalf("decode plan report: %v", err)
	}
	if report["file_mode"] != planFilePerm.String() {
		t.Fatalf("plan file mode = %q, want %q", report["file_mode"], planFilePerm.String())
	}
	if report["dir_mode"] != planDirPerm.String() {
		t.Fatalf("plan dir mode = %q, want %q", report["dir_mode"], planDirPerm.String())
	}

	// The temp directory must be gone once Invoke returns; a plan naming every
	// path the user is about to delete has no business outliving the call.
	if _, err := os.Stat(filepath.Dir(report["path"])); !os.IsNotExist(err) {
		t.Fatalf("plan directory should have been removed, stat err = %v", err)
	}
}

// TestInvokeErrorMapping pins the table documented on Invoke. The distinction
// it locks in is the honest one: only outcomes the parent can PROVE were
// pre-deletion are allowed to claim nothing was deleted.
func TestInvokeErrorMapping(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		wantFailed  bool
		wantUnknown bool
		wantErr     string
	}{
		{
			name:        "exit 1 with empty stdout from the helper is an unknown outcome, not an auth failure",
			mode:        "fail",
			wantUnknown: true,
		},
		{
			name:       "the guard exit code means a plan was rejected before any deletion",
			mode:       "guard-rejected",
			wantFailed: true,
		},
		{
			name:        "exit 0 with no output is an unknown outcome, not a no-op",
			mode:        "silent",
			wantUnknown: true,
			wantErr:     "no result",
		},
		{
			name:        "truncated stdout after exit 0 is an unknown outcome",
			mode:        "partial",
			wantUnknown: true,
			wantErr:     "unreadable result",
		},
		{
			name:        "unparseable stdout is an unknown outcome",
			mode:        "invalid",
			wantUnknown: true,
			wantErr:     "unreadable result",
		},
		{
			name:        "an unexpected exit code could be a crash mid-clean",
			mode:        "crash",
			wantUnknown: true,
		},
		{
			name:    "a result from a different schema version is refused",
			mode:    "wrong-version",
			wantErr: "schema version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubSudo(t, map[string]string{"TIDYMYMAC_TEST_HELPER_MODE": tt.mode})

			_, err := Invoke(context.Background(), invokablePlan())
			if err == nil {
				t.Fatalf("Invoke() expected an error, got none")
			}
			if got := errors.Is(err, ErrElevationFailed); got != tt.wantFailed {
				t.Fatalf("errors.Is(err, ErrElevationFailed) = %t, want %t (err: %v)", got, tt.wantFailed, err)
			}
			if got := errors.Is(err, ErrElevationOutcomeUnknown); got != tt.wantUnknown {
				t.Fatalf("errors.Is(err, ErrElevationOutcomeUnknown) = %t, want %t (err: %v)", got, tt.wantUnknown, err)
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
			// The whole point of the split: an unknown outcome must never be
			// worded as a reassurance.
			if tt.wantUnknown && strings.Contains(err.Error(), "nothing was deleted") {
				t.Fatalf("error = %q, must not claim nothing was deleted when the helper was past its guards", err.Error())
			}
		})
	}
}

// TestInvokeAuthFailureMeansNothingRan pins the reason authentication is a
// separate sudo invocation: a failure there is provably pre-deletion, so it is
// the one sudo-side failure that may be reported as "nothing was deleted" --
// and the helper must not have been spawned at all.
func TestInvokeAuthFailureMeansNothingRan(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "auth-ran")
	argvFile := filepath.Join(dir, "argv")

	stubSudo(t, map[string]string{
		"TIDYMYMAC_TEST_AUTH_MODE":   "fail",
		"TIDYMYMAC_TEST_AUTH_MARK":   mark,
		"TIDYMYMAC_TEST_HELPER_ARGV": argvFile,
	})

	_, err := Invoke(context.Background(), invokablePlan())
	if !errors.Is(err, ErrElevationFailed) {
		t.Fatalf("Invoke() error = %v, want ErrElevationFailed", err)
	}
	if errors.Is(err, ErrElevationOutcomeUnknown) {
		t.Fatalf("an authentication failure must not be reported as an unknown outcome: %v", err)
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("error = %q, want it to name authentication as the cause", err.Error())
	}
	if _, statErr := os.Stat(mark); statErr != nil {
		t.Fatalf("auth step should have run (mark file missing): %v", statErr)
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Fatalf("the helper must never be spawned after a failed authentication (argv file stat err = %v)", statErr)
	}
}

// TestInvokeAuthenticatesBeforeHelper proves the ordering: the helper only
// runs once the auth step has succeeded.
func TestInvokeAuthenticatesBeforeHelper(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "auth-ran")

	stubSudo(t, map[string]string{"TIDYMYMAC_TEST_AUTH_MARK": mark})

	if _, err := Invoke(context.Background(), invokablePlan()); err != nil {
		t.Fatalf("Invoke() error: %v", err)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Fatalf("auth step should have run before the helper: %v", err)
	}
}

// TestInvokeCancellationReturnsUnknownOutcome covers the case the old code got
// wrong twice: cancelling SIGKILLed sudo while the root helper kept deleting,
// and Wait then blocked on the inherited stdout pipe anyway.
func TestInvokeCancellationReturnsUnknownOutcome(t *testing.T) {
	stubSudo(t, map[string]string{"TIDYMYMAC_TEST_HELPER_MODE": "sleep"})
	// The auth step precedes the helper and shares the same context. Start
	// the clock only once the helper is actually running, so a slow test
	// binary start-up (e.g. under -race) cannot expire the context during
	// authentication and turn this into an ErrElevationFailed instead.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	helperStub := sudoCommand
	sudoCommand = func(ctx context.Context, exePath, planPath string) *exec.Cmd {
		time.AfterFunc(200*time.Millisecond, cancel)
		return helperStub(ctx, exePath, planPath)
	}

	start := time.Now()
	_, err := Invoke(ctx, invokablePlan())
	elapsed := time.Since(start)

	if !errors.Is(err, ErrElevationOutcomeUnknown) {
		t.Fatalf("Invoke() error = %v, want ErrElevationOutcomeUnknown", err)
	}
	if errors.Is(err, ErrElevationFailed) {
		t.Fatalf("a cancelled elevation must never be reported as ErrElevationFailed: %v", err)
	}
	if strings.Contains(err.Error(), "nothing was deleted") {
		t.Fatalf("error = %q, must not claim nothing was deleted after a cancellation", err.Error())
	}
	// Must return by the signal, or at worst WaitDelay -- never hang for the
	// stub's full sleep.
	if limit := 200*time.Millisecond + helperKillDelay + 5*time.Second; elapsed > limit {
		t.Fatalf("Invoke() took %v, want it to unblock within %v", elapsed, limit)
	}
}

func TestInvokeRejectsEmptyOrMisversionedPlansBeforePrompting(t *testing.T) {
	// If the seam is ever reached, sudoCommand would run the stub; failing
	// here instead proves Invoke bailed out before spawning anything.
	original := sudoCommand
	t.Cleanup(func() { sudoCommand = original })
	sudoCommand = func(ctx context.Context, exePath, planPath string) *exec.Cmd {
		t.Fatalf("Invoke must not spawn a privileged child for a plan it can reject locally")
		return nil
	}

	tests := []struct {
		name    string
		plan    Plan
		wantErr string
	}{
		{name: "no categories", plan: Plan{}, wantErr: "no entries"},
		{
			name:    "categories with no entries",
			plan:    Plan{Categories: []PlanCategory{{Category: cleaner.CategoryTemp}}},
			wantErr: "no entries",
		},
		{
			name:    "unsupported schema version",
			plan:    Plan{Version: PlanSchemaVersion + 1, Categories: invokablePlan().Categories},
			wantErr: "schema version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Invoke(context.Background(), tt.plan)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Invoke() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestSudoCommandArgv pins the real (unsubstituted) argv, since the stub in
// the tests above necessarily replaces it.
func TestSudoCommandArgv(t *testing.T) {
	cmd := sudoCommand(context.Background(), "/usr/local/bin/tidymymac", "/tmp/x/plan.json")

	want := []string{sudoPath, "-p", sudoPrompt, "/usr/local/bin/tidymymac", HelperCommandName, planFileFlag, "/tmp/x/plan.json"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %v, want %v", cmd.Args, want)
	}
	// An absolute sudo, never a $PATH lookup: a fake sudo earlier on PATH can
	// print the very same anti-phishing prompt and harvest the password.
	if !filepath.IsAbs(cmd.Args[0]) {
		t.Fatalf("sudo must be invoked by absolute path, got %q", cmd.Args[0])
	}
	// No "-S": the password must never flow through this process's stdin.
	for _, a := range cmd.Args {
		if a == "-S" || a == "--stdin" {
			t.Fatalf("argv must never ask sudo to read the password from stdin: %v", cmd.Args)
		}
	}
}

// TestSudoAuthCommandArgv pins the real authentication argv: "-v" validates
// the credential without running anything, with the same branded prompt.
func TestSudoAuthCommandArgv(t *testing.T) {
	cmd := sudoAuthCommand(context.Background())

	want := []string{sudoPath, "-p", sudoPrompt, "-v"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %v, want %v", cmd.Args, want)
	}
	for _, a := range cmd.Args {
		if a == "-S" || a == "--stdin" || a == "-n" {
			// -n would make an expired credential cache a hard failure the
			// parent could not tell from a crash; -S must never be used.
			t.Fatalf("auth argv must not contain %q: %v", a, cmd.Args)
		}
	}
}

func TestPlanAndResultJSONRoundTrip(t *testing.T) {
	plan := Plan{
		Version: PlanSchemaVersion,
		DryRun:  true,
		Categories: []PlanCategory{{
			Category: cleaner.CategoryLogs,
			Entries:  []cleaner.FileEntry{{Path: "/var/log/x", Size: 12, IsDir: false}},
		}},
	}

	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if !strings.Contains(string(data), `"version":1`) || !strings.Contains(string(data), `"dry_run":true`) {
		t.Fatalf("plan JSON = %s, want snake_case version/dry_run fields", data)
	}

	var decodedPlan Plan
	if err := json.Unmarshal(data, &decodedPlan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if decodedPlan.Version != plan.Version || !decodedPlan.DryRun ||
		len(decodedPlan.Categories) != 1 ||
		decodedPlan.Categories[0].Category != cleaner.CategoryLogs ||
		decodedPlan.Categories[0].Entries[0].Path != "/var/log/x" {
		t.Fatalf("round-tripped plan = %+v, want %+v", decodedPlan, plan)
	}

	result := Result{
		Version: ResultSchemaVersion,
		Clean: commands.CleanResult{TotalFiles: 1, TotalSize: 12, HasErrors: true, Categories: []commands.CleanCategoryResult{{
			Category:      cleaner.CategoryLogs,
			DeletedFiles:  1,
			DeletedSize:   12,
			PartialErrors: 2,
			PartialErrorDetails: []commands.ItemError{
				{Path: "/var/log/locked", Reason: "operation not permitted"},
			},
			PartialErrorsTruncated: true,
		}}},
		Intersections: []CategoryIntersection{{
			Category: cleaner.CategoryLogs,
			Name:     "System Logs",
			Approved: 2,
			Matched:  1,
			Missing:  1,
			ErrMsg:   "boom",
		}},
	}

	data, err = json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var decodedResult Result
	if err := json.Unmarshal(data, &decodedResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if decodedResult.Version != ResultSchemaVersion || !decodedResult.HasErrors() ||
		len(decodedResult.Intersections) != 1 || decodedResult.Intersections[0].Missing != 1 ||
		decodedResult.Intersections[0].ErrMsg != "boom" {
		t.Fatalf("round-tripped result = %+v, want %+v", decodedResult, result)
	}
	// Per-item failures are the reason the schema is at version 2: they must
	// cross the helper boundary intact, not collapse into a bare success.
	cat := decodedResult.Clean.Categories[0]
	if cat.PartialErrors != 2 || !cat.PartialErrorsTruncated ||
		len(cat.PartialErrorDetails) != 1 || cat.PartialErrorDetails[0].Path != "/var/log/locked" {
		t.Fatalf("round-tripped partial errors = %+v, want count 2, truncated, one detail", cat)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

package elevate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// samplePlan is a minimal valid plan payload used by the file-level tests,
// which care about the file's metadata rather than its contents.
func samplePlan() Plan {
	return Plan{
		Version: PlanSchemaVersion,
		Categories: []PlanCategory{{
			Category: cleaner.CategoryTemp,
			Entries:  []cleaner.FileEntry{{Path: "/tmp/example", Size: 1}},
		}},
	}
}

// writePlanFileAt writes plan to dir/plan.json with the given permissions,
// bypassing writePlanFile so tests can produce the *invalid* shapes too.
func writePlanFileAt(t *testing.T, dir string, perm os.FileMode, plan Plan) string {
	t.Helper()

	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	path := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	// WriteFile's mode is masked by umask; set it explicitly so the test
	// asserts what it means to assert.
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("chmod plan: %v", err)
	}
	return path
}

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, planDirPerm); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	return dir
}

func TestReadPlanFile(t *testing.T) {
	uid := os.Getuid()

	tests := []struct {
		name string
		// setup returns the path handed to readPlanFile.
		setup       func(t *testing.T) string
		expectedUID int
		wantErr     string
	}{
		{
			name: "accepts a 0600 file in a 0700 directory owned by the expected uid",
			setup: func(t *testing.T) string {
				return writePlanFileAt(t, privateDir(t), planFilePerm, samplePlan())
			},
			expectedUID: uid,
		},
		{
			name: "rejects a symlink in place of the plan file",
			setup: func(t *testing.T) string {
				dir := privateDir(t)
				target := writePlanFileAt(t, dir, planFilePerm, samplePlan())
				link := filepath.Join(dir, "link.json")
				if err := os.Symlink(target, link); err != nil {
					t.Fatalf("symlink: %v", err)
				}
				return link
			},
			expectedUID: uid,
			// O_NOFOLLOW makes the open itself fail rather than resolving it.
			wantErr: "opening plan file",
		},
		{
			name: "rejects a group-readable plan file",
			setup: func(t *testing.T) string {
				return writePlanFileAt(t, privateDir(t), 0o640, samplePlan())
			},
			expectedUID: uid,
			wantErr:     "want exactly 0600",
		},
		{
			name: "rejects a world-readable plan file",
			setup: func(t *testing.T) string {
				return writePlanFileAt(t, privateDir(t), 0o644, samplePlan())
			},
			expectedUID: uid,
			wantErr:     "want exactly 0600",
		},
		{
			name: "rejects a plan file over the size cap",
			setup: func(t *testing.T) string {
				dir := privateDir(t)
				path := filepath.Join(dir, "plan.json")
				if err := os.WriteFile(path, make([]byte, maxPlanFileBytes+1), planFilePerm); err != nil {
					t.Fatalf("write oversized plan: %v", err)
				}
				if err := os.Chmod(path, planFilePerm); err != nil {
					t.Fatalf("chmod: %v", err)
				}
				return path
			},
			expectedUID: uid,
			wantErr:     "over the",
		},
		{
			name: "rejects a plan file owned by another uid",
			setup: func(t *testing.T) string {
				return writePlanFileAt(t, privateDir(t), planFilePerm, samplePlan())
			},
			// We cannot chown as an unprivileged user, so instead we claim to
			// expect a different uid -- the check under test is the comparison.
			expectedUID: uid + 1,
			wantErr:     "owned by uid",
		},
		{
			name: "rejects a world-readable parent directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				path := writePlanFileAt(t, dir, planFilePerm, samplePlan())
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatalf("chmod dir: %v", err)
				}
				return path
			},
			expectedUID: uid,
			wantErr:     "plan directory",
		},
		{
			name: "rejects a directory in place of the plan file",
			setup: func(t *testing.T) string {
				dir := privateDir(t)
				sub := filepath.Join(dir, "plan.json")
				if err := os.Mkdir(sub, planDirPerm); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return sub
			},
			expectedUID: uid,
			wantErr:     "not a regular file",
		},
		{
			name: "rejects a plan with unknown fields",
			setup: func(t *testing.T) string {
				dir := privateDir(t)
				path := filepath.Join(dir, "plan.json")
				if err := os.WriteFile(path, []byte(`{"version":1,"surprise":true}`), planFilePerm); err != nil {
					t.Fatalf("write plan: %v", err)
				}
				if err := os.Chmod(path, planFilePerm); err != nil {
					t.Fatalf("chmod: %v", err)
				}
				return path
			},
			expectedUID: uid,
			wantErr:     "decoding plan file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup(t)

			plan, err := readPlanFile(path, tt.expectedUID)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("readPlanFile() unexpected error: %v", err)
				}
				if plan.Version != PlanSchemaVersion || len(plan.Categories) != 1 {
					t.Fatalf("readPlanFile() decoded unexpected plan: %+v", plan)
				}
				return
			}
			if err == nil {
				t.Fatalf("readPlanFile() expected error containing %q, got none", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("readPlanFile() error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestWritePlanFileProducesWhatReadPlanFileRequires locks the two sides of the
// IPC together: whatever Invoke writes must satisfy the root helper's checks.
func TestWritePlanFileProducesWhatReadPlanFileRequires(t *testing.T) {
	dir, path, err := writePlanFile(samplePlan())
	if err != nil {
		t.Fatalf("writePlanFile() error: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	plan, err := readPlanFile(path, os.Getuid())
	if err != nil {
		t.Fatalf("readPlanFile() rejected a file written by writePlanFile: %v", err)
	}
	if plan.Version != PlanSchemaVersion {
		t.Fatalf("round-tripped plan version = %d, want %d", plan.Version, PlanSchemaVersion)
	}
}

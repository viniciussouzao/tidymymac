package cleaner

import "testing"

func TestCategoryDisplayName(t *testing.T) {
	tests := []struct {
		category Category
		want     string
	}{
		{CategoryTemp, "Temporary Files"},
		{CategoryHomebrew, "Homebrew Cache"},
		{CategoryApplicationCaches, "Application Caches"},
		{CategoryDevelopmentArtifacts, "Development Artifacts"},
		{CategoryLogs, "System Logs"},
		{CategoryDocker, "Docker"},
		{CategoryIOSBackups, "iOS Backups"},
		{CategoryUpdates, "macOS Updates"},
		{CategoryDownloads, "Downloads"},
		{CategoryTrashBin, "Trash Files"},
		{CategoryXcode, "Xcode"},
	}

	for _, tt := range tests {
		t.Run(string(tt.category), func(t *testing.T) {
			if got := tt.category.DisplayName(); got != tt.want {
				t.Errorf("Category(%q).DisplayName() = %q, want %q", tt.category, got, tt.want)
			}
		})
	}
}

func TestCategoryDisplayNameUnknown(t *testing.T) {
	c := Category("unknown")
	if got := c.DisplayName(); got != "unknown" {
		t.Errorf("Category(%q).DisplayName() = %q, want %q", c, got, "unknown")
	}
}

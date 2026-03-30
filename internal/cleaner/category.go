package cleaner

type Category string

const (
	CategoryTemp                 Category = "temp"
	CategoryHomebrew             Category = "homebrew"
	CategoryCaches               Category = "caches"
	CategoryLogs                 Category = "logs"
	CategoryDocker               Category = "docker"
	CategoryIOSBackups           Category = "ios_backups"
	CategoryUpdates              Category = "macos_updates"
	CategoryTrashBin             Category = "trash_files"
	CategoryXcode                Category = "xcode"
	CategoryDevelopmentArtifacts Category = "development_artifacts"
	CategoryTimeMachineSnapshots Category = "time_machine"
)

func (c Category) DisplayName() string {
	switch c {
	case CategoryTemp:
		return "Temporary Files"
	case CategoryHomebrew:
		return "Homebrew Cache"
	case CategoryCaches:
		return "Application Caches"
	case CategoryLogs:
		return "System Logs"
	case CategoryDocker:
		return "Docker"
	case CategoryIOSBackups:
		return "iOS Backups"
	case CategoryUpdates:
		return "macOS Updates"
	case CategoryTrashBin:
		return "Trash Files"
	case CategoryXcode:
		return "Xcode"
	case CategoryDevelopmentArtifacts:
		return "Development Artifacts"
	case CategoryTimeMachineSnapshots:
		return "Time Machine Snapshots"
	default:
		return string(c)
	}
}

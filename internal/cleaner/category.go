package cleaner

type Category string

const (
	CategoryTemp                 Category = "temp"
	CategoryHomebrew             Category = "homebrew"
	CategoryApplicationCaches    Category = "app-caches"
	CategoryLogs                 Category = "logs"
	CategoryDocker               Category = "docker"
	CategoryIOSBackups           Category = "ios-backups"
	CategoryUpdates              Category = "macos-updates"
	CategoryTrashBin             Category = "trash"
	CategoryXcode                Category = "xcode"
	CategoryDevelopmentArtifacts Category = "development-artifacts"
	CategoryTimeMachineSnapshots Category = "time-machine"
)

func (c Category) DisplayName() string {
	switch c {
	case CategoryTemp:
		return "Temporary Files"
	case CategoryHomebrew:
		return "Homebrew Cache"
	case CategoryApplicationCaches:
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

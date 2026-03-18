package cleaner

type Category string

const (
	CategoryTemp     Category = "temp"
	CategoryHomebrew Category = "homebrew"
	CategoryCaches   Category = "caches"
	CategoryLogs     Category = "logs"
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
	default:
		return string(c)
	}
}

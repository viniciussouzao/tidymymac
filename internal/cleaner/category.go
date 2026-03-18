package cleaner

type Category string

const (
	CategoryTemp     Category = "temp"
	CategoryHomebrew Category = "homebrew"
	CategoryCaches   Category = "caches"
)

func (c Category) DisplayName() string {
	switch c {
	case CategoryTemp:
		return "Temporary Files"
	case CategoryHomebrew:
		return "Homebrew Cache"
	case CategoryCaches:
		return "Application Caches"
	default:
		return string(c)
	}
}

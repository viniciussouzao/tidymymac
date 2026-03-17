package cleaner

type Category string

const (
	CategoryTemp     Category = "temp"
	CategoryHomebrew Category = "homebrew"
)

func (c Category) DisplayName() string {
	switch c {
	case CategoryTemp:
		return "Temporary Files"
	case CategoryHomebrew:
		return "Homebrew Cache"
	default:
		return string(c)
	}
}

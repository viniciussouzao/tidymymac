package cleaner

type HomebrewCleaner struct{}

func NewHomebrewCleaner() *HomebrewCleaner {
	return &HomebrewCleaner{}
}

func (c *HomebrewCleaner) Category() Category { return CategoryHomebrew }

func (c *HomebrewCleaner) Name() string { return "Homebrew Cache" }

func (c *HomebrewCleaner) Description() string { return "Cached files from Homebrew package manager" }

func (c *HomebrewCleaner) RequiresSudo() bool { return false }

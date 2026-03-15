package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up             key.Binding
	Down           key.Binding
	Select         key.Binding
	Confirm        key.Binding
	Back           key.Binding
	Quit           key.Binding
	SelectAll      key.Binding
	ReRun          key.Binding
	GenerateScript key.Binding
	FullPath       key.Binding
	NextList       key.Binding
	ToggleShowAll  key.Binding
}

var keys = keyMap{
	Up:             key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("up/k", "up")),
	Down:           key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("down/j", "'down")),
	Select:         key.NewBinding(key.WithKeys(" ", "x"), key.WithHelp("space", "select/deselect")),
	Confirm:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
	Back:           key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Quit:           key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q/ctrl+c", "quit")),
	SelectAll:      key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "select all")),
	ReRun:          key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "re-run")),
	GenerateScript: key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "generate script")),
	FullPath:       key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "full path")),
	NextList:       key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next list")),
	ToggleShowAll:  key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "show all")),
}

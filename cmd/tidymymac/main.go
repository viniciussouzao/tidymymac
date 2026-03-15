package main

import (
	"github.com/viniciussouzao/tidymymac/internal/tui/screens"
)

func main() {
	//cmd.Execute()

	dashboard := screens.NewDashboard()
	dashboard.View()
}

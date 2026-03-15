package main

import (
	"context"
	"fmt"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

func main() {
	//cmd.Execute()

	cleaner := cleaner.NewTempCleaner()
	scanResult, err := cleaner.Scan(context.Background(), nil)
	if err != nil {
		fmt.Println("failed to test:", err)
	}

	for _, entry := range scanResult.Entries {
		fmt.Printf("%s - %s\n", entry.Path, utils.FormatBytes(entry.Size))
	}
}

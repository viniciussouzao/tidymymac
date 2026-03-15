package cleaner

func totalSize(entries []FileEntry) int64 {
	var total int64
	for _, entry := range entries {
		total += entry.Size
	}

	return total
}

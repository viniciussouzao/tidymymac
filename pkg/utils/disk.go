package utils

import "syscall"

// DiskUsage returns the total, used and free disk space in bytes for the filesystem at the given path.
func DiskUsage(path string) (total, used, free int64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return
	}

	total = int64(stat.Blocks) * int64(stat.Bsize)
	free = int64(stat.Bfree) * int64(stat.Bsize)
	used = total - free
	return
}

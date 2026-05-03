//go:build linux

package audit

import "syscall"

// availableDiskMB returns the bytes-available count at dir, converted
// to MiB. Used by Validate to flag a cap larger than the underlying
// volume can hold. Returns an error if the path doesn't exist yet —
// Validate handles that as "skip the disk check."
func availableDiskMB(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	// Bavail is blocks available to non-root processes; Bsize is
	// the block size. Multiply, divide by MiB.
	return int64(st.Bavail) * int64(st.Bsize) / (1024 * 1024), nil
}

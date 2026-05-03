//go:build !linux

package audit

// availableDiskMB on non-Linux returns an error so Validate skips the
// disk-availability warning. Periscope only ships on Linux; this shim
// exists so the package builds on macOS / Windows dev machines.
func availableDiskMB(_ string) (int64, error) {
	return 0, errSkipDiskCheck
}

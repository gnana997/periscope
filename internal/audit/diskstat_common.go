package audit

import "errors"

// errSkipDiskCheck is returned by availableDiskMB on platforms that
// don't implement disk-stat probing. Validate treats this as a
// normal "skip the check" signal, not a real error.
var errSkipDiskCheck = errors.New("disk-stat unavailable on this platform")

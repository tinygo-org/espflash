//go:build !darwin && !linux

package espflasher

import "errors"

var errNoHandle = errors.New("atomic TIOCMSET not available on this platform")

// setDTRandRTSAtomic is not available on non-Unix platforms.
// Non-Unix systems will always fall back to separate SetDTR/SetRTS calls.
func setDTRandRTSAtomic(port interface{}, dtr, rts bool) error {
	return errNoHandle
}

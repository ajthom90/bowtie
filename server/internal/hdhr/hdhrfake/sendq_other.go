//go:build !darwin && !linux

package hdhrfake

// sendQueue is unknown on this platform; abort falls back to a fixed pause.
func sendQueue(uintptr) (int, bool) { return 0, false }

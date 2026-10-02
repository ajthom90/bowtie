package hdhrfake

import "golang.org/x/sys/unix"

// sendQueue returns the bytes still in fd's send buffer (unsent + unacked).
func sendQueue(fd uintptr) (int, bool) {
	n, err := unix.IoctlGetInt(int(fd), unix.SIOCOUTQ)
	return n, err == nil
}

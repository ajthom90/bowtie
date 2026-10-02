package hdhrfake

import "golang.org/x/sys/unix"

// sendQueue returns the bytes still in fd's send buffer (unsent + unacked).
func sendQueue(fd uintptr) (int, bool) {
	n, err := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_NWRITE)
	return n, err == nil
}

package dvr

import "syscall"

// freeBytes reports the space available to unprivileged users in dir.
func freeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil //nolint:unconvert // Bsize is uint32 on darwin
}

// totalBytes reports the size of the filesystem holding dir as seen by
// unprivileged users (root-reserved blocks left out, matching freeBytes).
func totalBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Blocks-st.Bfree+st.Bavail) * int64(st.Bsize), nil //nolint:unconvert // Bsize is uint32 on darwin
}

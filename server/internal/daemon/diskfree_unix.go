//go:build !windows

package daemon

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// diskFreeStats reports the bytes available to an unprivileged writer and the
// total capacity of the filesystem that holds path. It powers the GC's
// disk-pressure escalation, so "available" deliberately means Bavail (space a
// non-root process may actually use), not Bfree (which includes the
// root-reserved slack a normal daemon can never touch).
func diskFreeStats(path string) (availBytes, totalBytes uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %q: %w", path, err)
	}
	// Bsize is the fundamental block size the block counts are expressed in.
	// Field widths differ across unixes (uint32 on darwin, int64 on linux), so
	// widen everything to uint64 before multiplying.
	bsize := uint64(st.Bsize)
	return uint64(st.Bavail) * bsize, uint64(st.Blocks) * bsize, nil
}

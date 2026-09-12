//go:build windows

package daemon

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// diskFreeStats reports the bytes available to the calling user and the total
// capacity of the volume that holds path. GetDiskFreeSpaceExW already accounts
// for per-user quotas in the "available" figure, which is the Windows analogue
// of Bavail on unix.
func diskFreeStats(path string) (availBytes, totalBytes uint64, err error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, fmt.Errorf("path %q: %w", path, err)
	}
	var freeAvailable, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeAvailable, &total, &totalFree); err != nil {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx %q: %w", path, err)
	}
	return freeAvailable, total, nil
}

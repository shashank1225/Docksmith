//go:build linux

package runtime

import "syscall"

func chrootRootFS(rootFS string) error {
	return syscall.Chroot(rootFS)
}

//go:build !linux

package runtime

import "fmt"

func chrootRootFS(rootFS string) error {
	return fmt.Errorf("chroot is only supported on Linux")
}

package runtime

import (
	"fmt"
	"strings"
)

func ApplyIsolation(rootFS string) error {
	if strings.TrimSpace(rootFS) == "" {
		return fmt.Errorf("root filesystem path is empty")
	}

	if err := chrootRootFS(rootFS); err != nil {
		return fmt.Errorf("apply chroot isolation %q: %w", rootFS, err)
	}

	return nil
}

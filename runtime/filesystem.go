package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"docksmith/layers"
	"docksmith/store"
)

func PrepareContainerFilesystem(imageRef string) (string, string, *store.ImageManifest, error) {
	bundleDir, err := os.MkdirTemp("", "docksmith-container-")
	if err != nil {
		return "", "", nil, fmt.Errorf("create container bundle directory: %w", err)
	}

	rootFS := filepath.Join(bundleDir, "rootfs")
	if err := os.MkdirAll(rootFS, 0o755); err != nil {
		_ = os.RemoveAll(bundleDir)
		return "", "", nil, fmt.Errorf("create root filesystem directory: %w", err)
	}

	manifest, err := loadImageManifest(imageRef)
	if err != nil {
		_ = os.RemoveAll(bundleDir)
		return "", "", nil, err
	}

	for _, layer := range manifest.Layers {
		if err := layers.ExtractLayer(layer.Digest, rootFS); err != nil {
			_ = os.RemoveAll(bundleDir)
			return "", "", nil, fmt.Errorf("extract layer %q: %w", layer.Digest, err)
		}
	}

	manifest.Config.Env = store.NormalizeEnvList(manifest.Config.Env)

	return bundleDir, rootFS, manifest, nil
}

func CleanupContainerFilesystem(bundleDir string) error {
	if strings.TrimSpace(bundleDir) == "" {
		return nil
	}

	if err := os.RemoveAll(bundleDir); err != nil {
		return fmt.Errorf("cleanup container filesystem %q: %w", bundleDir, err)
	}

	return nil
}

func loadImageManifest(imageRef string) (*store.ImageManifest, error) {
	manifest, err := store.LoadImage(imageRef)
	if err != nil {
		return nil, err
	}

	return manifest, nil
}

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"docksmith/layers"
)

type ImageManifest struct {
	Name   string      `json:"name"`
	Tag    string      `json:"tag"`
	Layers []string    `json:"layers"`
	Config ImageConfig `json:"config"`
}

type ImageConfig struct {
	Env        map[string]string `json:"Env"`
	Cmd        []string          `json:"Cmd"`
	WorkingDir string            `json:"WorkingDir"`
	BaseImage  string            `json:"BaseImage"`
}

func PrepareContainerFilesystem(imageRef string) (string, string, *ImageManifest, error) {
	bundleDir, err := os.MkdirTemp("/tmp", "docksmith-container-")
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

	for _, digest := range manifest.Layers {
		if err := layers.ExtractLayer(digest, rootFS); err != nil {
			_ = os.RemoveAll(bundleDir)
			return "", "", nil, fmt.Errorf("extract layer %q: %w", digest, err)
		}
	}

	if manifest.Config.Env == nil {
		manifest.Config.Env = map[string]string{}
	}

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

func loadImageManifest(imageRef string) (*ImageManifest, error) {
	name, tag, err := splitImageReference(imageRef)
	if err != nil {
		return nil, err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	manifestPath := filepath.Join(homeDir, ".docksmith", "images", name+"_"+tag+".json")
	file, err := os.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open image manifest %q: %w", manifestPath, err)
	}
	defer file.Close()

	var manifest ImageManifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode image manifest %q: %w", manifestPath, err)
	}

	return &manifest, nil
}

func splitImageReference(ref string) (string, string, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid image reference %q, expected <name:tag>", ref)
	}

	name := strings.TrimSpace(parts[0])
	tag := strings.TrimSpace(parts[1])
	if name == "" || tag == "" {
		return "", "", fmt.Errorf("invalid image reference %q, expected <name:tag>", ref)
	}

	return name, tag, nil
}

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func EnsureLayout() error {
	root, err := RootDir()
	if err != nil {
		return err
	}

	dirs := []string{
		filepath.Join(root, "layers"),
		filepath.Join(root, "images"),
		filepath.Join(root, "cache"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}

	return nil
}

func RootDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(homeDir, ".docksmith"), nil
}

func LayerPath(digest string) (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(root, "layers", digest+".tar"), nil
}

func ImagePath(name string, tag string) (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}

	fileName := strings.ReplaceAll(name, "/", "_") + "_" + tag + ".json"
	return filepath.Join(root, "images", fileName), nil
}

func HashParts(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}

	return "sha256_" + hex.EncodeToString(h.Sum(nil))
}

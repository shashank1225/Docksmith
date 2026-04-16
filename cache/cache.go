package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	fileDigest := strings.ReplaceAll(digest, ":", "_")
	return filepath.Join(root, "layers", fileDigest+".tar"), nil
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

	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func CacheIndexPath() (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(root, "cache", "index.json"), nil
}

func LoadCacheIndex() (map[string]string, error) {
	if err := EnsureLayout(); err != nil {
		return nil, err
	}

	path, err := CacheIndexPath()
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open cache index %q: %w", path, err)
	}
	defer file.Close()

	index := map[string]string{}
	if err := json.NewDecoder(file).Decode(&index); err != nil {
		return nil, fmt.Errorf("decode cache index %q: %w", path, err)
	}

	return index, nil
}

func SaveCacheIndex(index map[string]string) error {
	if err := EnsureLayout(); err != nil {
		return err
	}

	path, err := CacheIndexPath()
	if err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create cache index %q: %w", path, err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(index); err != nil {
		return fmt.Errorf("write cache index %q: %w", path, err)
	}

	return nil
}

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"docksmith/layers"
)

const cacheIndexFileName = "index.json"

// ComputeCacheKey derives a deterministic SHA256 key from build step inputs.
func ComputeCacheKey(prevDigest string, instruction string, workdir string, env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(prevDigest)
	b.WriteString("\n")
	b.WriteString(instruction)
	b.WriteString("\n")
	b.WriteString(workdir)
	b.WriteString("\n")

	for _, key := range keys {
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(env[key])
		b.WriteString("\n")
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// LoadCache loads ~/.docksmith/cache/index.json as cache_key -> layer_digest.
func LoadCache() (map[string]string, error) {
	path, err := cacheIndexPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read cache index: %w", err)
	}

	if len(data) == 0 {
		return map[string]string{}, nil
	}

	cache := map[string]string{}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("parse cache index: %w", err)
	}

	return cache, nil
}

// SaveCache writes the cache map to ~/.docksmith/cache/index.json.
func SaveCache(cache map[string]string) error {
	path, err := cacheIndexPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache index: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp cache index: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace cache index: %w", err)
	}

	return nil
}

// LookupCache resolves a cache key to a layer digest.
func LookupCache(key string) (digest string, found bool) {
	cache, err := LoadCache()
	if err != nil {
		return "", false
	}

	digest, found = cache[key]
	return digest, found
}

// StoreCache stores or updates cache_key -> layer_digest.
func StoreCache(key string, digest string) error {
	cache, err := LoadCache()
	if err != nil {
		return err
	}

	cache[key] = digest
	return SaveCache(cache)
}

func cacheIndexPath() (string, error) {
	if err := layers.EnsureDocksmithDirs(); err != nil {
		return "", err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	cacheDir := filepath.Join(home, ".docksmith", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create cache directory: %w", err)
	}

	return filepath.Join(cacheDir, cacheIndexFileName), nil
}

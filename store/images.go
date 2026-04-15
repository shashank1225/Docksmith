package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"docksmith/cache"
)

type ImageConfig struct {
	Env        map[string]string `json:"Env"`
	Cmd        []string          `json:"Cmd"`
	WorkingDir string            `json:"WorkingDir"`
	BaseImage  string            `json:"BaseImage"`
}

type ImageManifest struct {
	Name      string      `json:"name"`
	Tag       string      `json:"tag"`
	Layers    []string    `json:"layers"`
	Config    ImageConfig `json:"config"`
	CreatedAt string      `json:"createdAt"`
}

func SaveImage(manifest ImageManifest) error {
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Tag) == "" {
		return fmt.Errorf("image manifest must include name and tag")
	}
	if manifest.CreatedAt == "" {
		manifest.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	if err := cache.EnsureLayout(); err != nil {
		return err
	}

	path, err := cache.ImagePath(manifest.Name, manifest.Tag)
	if err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create image manifest %q: %w", path, err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("write image manifest %q: %w", path, err)
	}

	return nil
}

func LoadImage(imageRef string) (*ImageManifest, error) {
	name, tag, err := ParseImageReference(imageRef)
	if err != nil {
		return nil, err
	}

	path, err := cache.ImagePath(name, tag)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open image manifest %q: %w", path, err)
	}
	defer file.Close()

	var manifest ImageManifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode image manifest %q: %w", path, err)
	}

	if manifest.Config.Env == nil {
		manifest.Config.Env = map[string]string{}
	}

	return &manifest, nil
}

func ListImages() ([]ImageManifest, error) {
	root, err := cache.RootDir()
	if err != nil {
		return nil, err
	}

	imagesDir := filepath.Join(root, "images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return nil, fmt.Errorf("ensure images directory %q: %w", imagesDir, err)
	}

	entries, err := os.ReadDir(imagesDir)
	if err != nil {
		return nil, fmt.Errorf("read images directory %q: %w", imagesDir, err)
	}

	images := make([]ImageManifest, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(imagesDir, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open image manifest %q: %w", path, err)
		}

		var manifest ImageManifest
		decodeErr := json.NewDecoder(file).Decode(&manifest)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode image manifest %q: %w", path, decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close image manifest %q: %w", path, closeErr)
		}

		images = append(images, manifest)
	}

	sort.Slice(images, func(i, j int) bool {
		left := images[i].Name + ":" + images[i].Tag
		right := images[j].Name + ":" + images[j].Tag
		return left < right
	})

	return images, nil
}

func DeleteImage(imageRef string) ([]string, error) {
	name, tag, err := ParseImageReference(imageRef)
	if err != nil {
		return nil, err
	}

	target, err := LoadImage(imageRef)
	if err != nil {
		return nil, err
	}

	path, err := cache.ImagePath(name, tag)
	if err != nil {
		return nil, err
	}

	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("remove image manifest %q: %w", path, err)
	}

	all, err := ListImages()
	if err != nil {
		return nil, err
	}

	inUse := make(map[string]bool)
	for _, img := range all {
		for _, digest := range img.Layers {
			inUse[digest] = true
		}
	}

	removed := make([]string, 0)
	for _, digest := range target.Layers {
		if inUse[digest] {
			continue
		}

		layerPath, err := cache.LayerPath(digest)
		if err != nil {
			return nil, err
		}

		err = os.Remove(layerPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove layer archive %q: %w", layerPath, err)
		}

		removed = append(removed, digest)
	}

	return removed, nil
}

func ParseImageReference(imageRef string) (string, string, error) {
	parts := strings.Split(imageRef, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid image reference %q, expected <name:tag>", imageRef)
	}

	name := strings.TrimSpace(parts[0])
	tag := strings.TrimSpace(parts[1])
	if name == "" || tag == "" {
		return "", "", fmt.Errorf("invalid image reference %q, expected <name:tag>", imageRef)
	}

	return name, tag, nil
}

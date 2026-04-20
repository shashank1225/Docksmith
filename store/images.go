package store

import (
	"crypto/sha256"
	"encoding/hex"
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
	Env        []string `json:"Env"`
	Cmd        []string `json:"Cmd"`
	WorkingDir string   `json:"WorkingDir"`
}

type LayerDescriptor struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	CreatedBy string `json:"createdBy"`
}

type ImageManifest struct {
	Name    string            `json:"name"`
	Tag     string            `json:"tag"`
	Digest  string            `json:"digest"`
	Created string            `json:"created"`
	Config  ImageConfig       `json:"config"`
	Layers  []LayerDescriptor `json:"layers"`
}

func SaveImage(manifest ImageManifest) error {
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Tag) == "" {
		return fmt.Errorf("image manifest must include name and tag")
	}

	manifest.Config.Env = NormalizeEnvList(manifest.Config.Env)
	if manifest.Config.WorkingDir == "" {
		manifest.Config.WorkingDir = "/"
	}

	if err := cache.EnsureLayout(); err != nil {
		return err
	}

	path, err := cache.ImagePath(manifest.Name, manifest.Tag)
	if err != nil {
		return err
	}

	if existing, err := LoadImage(manifest.Name + ":" + manifest.Tag); err == nil {
		manifest.Created = existing.Created
	}
	if manifest.Created == "" {
		manifest.Created = time.Now().UTC().Format(time.RFC3339)
	}

	digest, err := ComputeManifestDigest(manifest)
	if err != nil {
		return err
	}
	manifest.Digest = digest

	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode image manifest %q: %w", path, err)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create image manifest %q: %w", path, err)
	}
	defer file.Close()

	if _, err := file.Write(payload); err != nil {
		return fmt.Errorf("write image manifest %q: %w", path, err)
	}
	if _, err := file.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write image manifest %q: %w", path, err)
	}

	return nil
}

func ComputeManifestDigest(manifest ImageManifest) (string, error) {
	canonical := manifest
	canonical.Digest = ""

	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode canonical manifest: %w", err)
	}

	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
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

	manifest.Config.Env = NormalizeEnvList(manifest.Config.Env)
	if manifest.Config.WorkingDir == "" {
		manifest.Config.WorkingDir = "/"
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

	referenced, err := referencedLayerDigests()
	if err != nil {
		return nil, err
	}

	removed := make([]string, 0)
	for _, layer := range target.Layers {
		if _, inUse := referenced[layer.Digest]; inUse {
			continue
		}

		layerPath, err := cache.LayerPath(layer.Digest)
		if err != nil {
			return nil, err
		}

		err = os.Remove(layerPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove layer archive %q: %w", layerPath, err)
		}

		removed = append(removed, layer.Digest)
	}

	return removed, nil
}

func referencedLayerDigests() (map[string]struct{}, error) {
	images, err := ListImages()
	if err != nil {
		return nil, err
	}

	referenced := make(map[string]struct{})
	for _, image := range images {
		for _, layer := range image.Layers {
			referenced[layer.Digest] = struct{}{}
		}
	}

	return referenced, nil
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

func NormalizeEnvList(env []string) []string {
	if len(env) == 0 {
		return []string{}
	}

	values := make(map[string]string, len(env))
	for _, item := range env {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		values[key] = parts[1]
	}

	return EnvMapToList(values)
}

func EnvMapToList(env map[string]string) []string {
	if len(env) == 0 {
		return []string{}
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}

	return out
}

func EnvListToMap(env []string) map[string]string {
	if len(env) == 0 {
		return map[string]string{}
	}

	out := make(map[string]string, len(env))
	for _, item := range env {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		out[key] = parts[1]
	}

	return out
}

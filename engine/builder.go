package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"docksmith/cache"
	"docksmith/layers"
	"docksmith/store"
)

// Build builds a container image from a Dockerfile in the context directory
func Build(tag string, context string) error {
	name, imageTag, err := store.ParseImageReference(tag)
	if err != nil {
		return err
	}

	absContext, err := filepath.Abs(context)
	if err != nil {
		return fmt.Errorf("resolve context path %q: %w", context, err)
	}

	if err := cache.EnsureLayout(); err != nil {
		return err
	}

	spec, err := ParseBuildFile(absContext)
	if err != nil {
		return err
	}

	rootFS, err := os.MkdirTemp("", "docksmith-build-rootfs-")
	if err != nil {
		return fmt.Errorf("create build root filesystem: %w", err)
	}
	defer os.RemoveAll(rootFS)

	imageConfig := store.ImageConfig{
		Env:        map[string]string{},
		WorkingDir: "/",
	}

	layerDigests := make([]string, 0)
	prevDigest := "base:empty"

	for idx, inst := range spec.Instructions {
		fmt.Printf("Step %d/%d : %s\n", idx+1, len(spec.Instructions), inst.Raw)

		switch inst.Op {
		case "FROM":
			imageConfig.BaseImage = strings.TrimSpace(inst.Args[0])
			if err := resetDir(rootFS); err != nil {
				return err
			}
			prevDigest = cache.HashParts("FROM", imageConfig.BaseImage)
		case "ENV":
			key, value, err := parseEnvPair(inst.Args[0])
			if err != nil {
				return fmt.Errorf("line %d: %w", inst.Line, err)
			}
			imageConfig.Env[key] = value
		case "WORKDIR":
			imageConfig.WorkingDir = normalizeContainerPath(imageConfig.WorkingDir, inst.Args[0])
			if err := os.MkdirAll(filepath.Join(rootFS, trimLeadingSlash(imageConfig.WorkingDir)), 0o755); err != nil {
				return fmt.Errorf("create workdir %q: %w", imageConfig.WorkingDir, err)
			}
		case "CMD":
			imageConfig.Cmd = []string{"/bin/sh", "-c", inst.Args[0]}
		case "COPY":
			src := inst.Args[0]
			dst := inst.Args[1]

			sourceHash, err := hashContextPath(absContext, src)
			if err != nil {
				return err
			}

			digest := cache.HashParts(prevDigest, "COPY", src, dst, sourceHash)
			hit, err := layers.LayerExists(digest)
			if err != nil {
				return err
			}

			if hit {
				fmt.Printf("[CACHE HIT] COPY %s %s\n", src, dst)
				if err := resetDir(rootFS); err != nil {
					return err
				}
				if err := layers.ExtractLayer(digest, rootFS); err != nil {
					return err
				}
			} else {
				fmt.Printf("[CACHE MISS] COPY %s %s\n", src, dst)
				if err := copyFromContext(absContext, src, rootFS, dst); err != nil {
					return err
				}
				if err := layers.WriteSnapshotLayer(digest, rootFS); err != nil {
					return err
				}
			}

			layerDigests = append(layerDigests, digest)
			prevDigest = digest
		case "RUN":
			envHash := hashEnv(imageConfig.Env)
			digest := cache.HashParts(prevDigest, "RUN", inst.Args[0], imageConfig.WorkingDir, envHash)
			hit, err := layers.LayerExists(digest)
			if err != nil {
				return err
			}

			if hit {
				fmt.Printf("[CACHE HIT] RUN %s\n", inst.Args[0])
				if err := resetDir(rootFS); err != nil {
					return err
				}
				if err := layers.ExtractLayer(digest, rootFS); err != nil {
					return err
				}
			} else {
				fmt.Printf("[CACHE MISS] RUN %s\n", inst.Args[0])
				if err := runInRootFS(rootFS, imageConfig.WorkingDir, imageConfig.Env, inst.Args[0]); err != nil {
					return err
				}
				if err := layers.WriteSnapshotLayer(digest, rootFS); err != nil {
					return err
				}
			}

			layerDigests = append(layerDigests, digest)
			prevDigest = digest
		default:
			return fmt.Errorf("unsupported instruction %q", inst.Op)
		}
	}

	if len(imageConfig.Cmd) == 0 {
		imageConfig.Cmd = []string{"/bin/sh"}
	}

	manifest := store.ImageManifest{
		Name:   name,
		Tag:    imageTag,
		Layers: layerDigests,
		Config: imageConfig,
	}

	if err := store.SaveImage(manifest); err != nil {
		return err
	}

	fmt.Printf("Successfully built %s\n", tag)
	return nil
}

func parseEnvPair(raw string) (string, string, error) {
	parts := strings.SplitN(raw, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("ENV requires KEY=value, got %q", raw)
	}

	key := strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", fmt.Errorf("ENV key cannot be empty")
	}

	return key, parts[1], nil
}

func normalizeContainerPath(current string, next string) string {
	next = strings.TrimSpace(next)
	if strings.HasPrefix(next, "/") {
		return filepath.ToSlash(filepath.Clean(next))
	}

	return filepath.ToSlash(filepath.Clean(filepath.Join(current, next)))
}

func trimLeadingSlash(path string) string {
	return strings.TrimPrefix(path, "/")
}

func resetDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("reset directory %q: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("recreate directory %q: %w", path, err)
	}
	return nil
}

func copyFromContext(contextDir string, src string, rootFS string, dst string) error {
	sourcePath := filepath.Join(contextDir, filepath.FromSlash(src))
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("stat COPY source %q: %w", sourcePath, err)
	}

	destPath := filepath.Join(rootFS, filepath.FromSlash(trimLeadingSlash(dst)))
	if info.IsDir() {
		return copyDirectory(sourcePath, destPath)
	}

	if strings.HasSuffix(dst, "/") {
		destPath = filepath.Join(destPath, filepath.Base(sourcePath))
	}

	return copyFile(sourcePath, destPath)
}

func copyDirectory(src string, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create directory %q: %w", dst, err)
	}

	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == src {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return copyFile(path, target)
	})
}

func copyFile(src string, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", dst, err)
	}

	r, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", src, err)
	}
	defer r.Close()

	info, err := r.Stat()
	if err != nil {
		return fmt.Errorf("stat source file %q: %w", src, err)
	}

	w, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("open destination file %q: %w", dst, err)
	}

	_, copyErr := io.Copy(w, r)
	closeErr := w.Close()
	if copyErr != nil {
		return fmt.Errorf("copy %q to %q: %w", src, dst, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close destination file %q: %w", dst, closeErr)
	}

	return nil
}

func runInRootFS(rootFS string, workDir string, env map[string]string, command string) error {
	hostWorkDir := filepath.Join(rootFS, trimLeadingSlash(workDir))
	if err := os.MkdirAll(hostWorkDir, 0o755); err != nil {
		return fmt.Errorf("create RUN working directory %q: %w", hostWorkDir, err)
	}

	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = hostWorkDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	mergedEnv := []string{"PATH=" + os.Getenv("PATH")}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		mergedEnv = append(mergedEnv, key+"="+env[key])
	}
	cmd.Env = mergedEnv

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("RUN command %q failed: %w", command, err)
	}

	return nil
}

func hashContextPath(contextDir string, relPath string) (string, error) {
	path := filepath.Join(contextDir, filepath.FromSlash(relPath))
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat path %q for hashing: %w", path, err)
	}

	h := sha256.New()
	if info.IsDir() {
		paths := make([]string, 0)
		err := filepath.WalkDir(path, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, relErr := filepath.Rel(path, p)
			if relErr != nil {
				return relErr
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("walk directory %q: %w", path, err)
		}

		sort.Strings(paths)
		for _, rel := range paths {
			full := filepath.Join(path, filepath.FromSlash(rel))
			entryInfo, statErr := os.Lstat(full)
			if statErr != nil {
				return "", fmt.Errorf("stat %q: %w", full, statErr)
			}

			_, _ = h.Write([]byte(rel))
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(entryInfo.Mode().String()))
			_, _ = h.Write([]byte{0})

			if entryInfo.Mode().IsRegular() {
				file, openErr := os.Open(full)
				if openErr != nil {
					return "", fmt.Errorf("open %q: %w", full, openErr)
				}
				_, copyErr := io.Copy(h, file)
				closeErr := file.Close()
				if copyErr != nil {
					return "", fmt.Errorf("hash %q: %w", full, copyErr)
				}
				if closeErr != nil {
					return "", fmt.Errorf("close %q: %w", full, closeErr)
				}
			}
		}
	} else {
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open file %q: %w", path, err)
		}
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash file %q: %w", path, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close file %q: %w", path, closeErr)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}

	return strings.Join(parts, "\n")
}

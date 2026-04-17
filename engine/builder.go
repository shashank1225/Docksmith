package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"docksmith/cache"
	"docksmith/layers"
	dockruntime "docksmith/runtime"
	"docksmith/store"
)

type BuildOptions struct {
	NoCache bool
}

func Build(tag string, context string, opts BuildOptions) error {
	buildStart := time.Now()

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
	if len(spec.Instructions) == 0 || spec.Instructions[0].Op != "FROM" {
		return fmt.Errorf("line %d: first instruction must be FROM", spec.Instructions[0].Line)
	}

	cacheIndex := map[string]string{}
	if !opts.NoCache {
		cacheIndex, err = cache.LoadCacheIndex()
		if err != nil {
			return err
		}
	}
	cacheDirty := false

	rootFS, err := os.MkdirTemp("", "docksmith-build-rootfs-")
	if err != nil {
		return fmt.Errorf("create build root filesystem: %w", err)
	}
	defer os.RemoveAll(rootFS)

	imageConfig := store.ImageConfig{
		Env:        []string{},
		WorkingDir: "/",
	}
	envMap := map[string]string{}

	manifestLayers := make([]store.LayerDescriptor, 0)
	prevDigest := ""
	pendingWorkdirCreate := false
	cascadeMiss := false

	for idx, inst := range spec.Instructions {
		fmt.Printf("Step %d/%d : %s\n", idx+1, len(spec.Instructions), inst.Raw)
		stepStart := time.Now()

		switch inst.Op {
		case "FROM":
			baseRef := normalizeImageRef(inst.Args[0])
			baseManifest, err := store.LoadImage(baseRef)
			if err != nil {
				if baseRef != "alpine:3.18" {
					return fmt.Errorf("line %d: FROM %q failed: %w", inst.Line, baseRef, err)
				}
				baseManifest = &store.ImageManifest{
					Name:   "alpine",
					Tag:    "3.18",
					Config: store.ImageConfig{Env: []string{}, WorkingDir: "/", Cmd: []string{}},
					Layers: []store.LayerDescriptor{},
				}
			}

			baseDigest := baseManifest.Digest
			if baseDigest == "" {
				baseDigest = cache.HashParts(baseRef, baseManifest.Config.WorkingDir, serializeEnv(store.EnvListToMap(baseManifest.Config.Env)), serializeCmd(baseManifest.Config.Cmd))
			}
			cacheKey := cache.HashParts("FROM", baseRef, baseDigest)
			hit := false
			if !opts.NoCache {
				if cachedDigest, ok := cacheIndex[cacheKey]; ok && cachedDigest == cacheKey {
					hit = true
				}
			}

			if err := resetDir(rootFS); err != nil {
				return err
			}
			for _, layer := range baseManifest.Layers {
				if err := layers.ExtractLayer(layer.Digest, rootFS); err != nil {
					return fmt.Errorf("extract base layer %q: %w", layer.Digest, err)
				}
			}

			manifestLayers = append([]store.LayerDescriptor{}, baseManifest.Layers...)
			prevDigest = baseManifest.Digest
			if prevDigest == "" {
				prevDigest = "base:empty"
			}
			imageConfig.WorkingDir = baseManifest.Config.WorkingDir
			if imageConfig.WorkingDir == "" {
				imageConfig.WorkingDir = "/"
			}
			envMap = store.EnvListToMap(baseManifest.Config.Env)
			imageConfig.Env = store.EnvMapToList(envMap)
			if len(baseManifest.Config.Cmd) > 0 {
				imageConfig.Cmd = append([]string{}, baseManifest.Config.Cmd...)
			}
			cascadeMiss = false
			prevDigest = cacheKey
			if hit {
				fmt.Printf("[CACHE HIT] %.2fs\n", time.Since(stepStart).Seconds())
			} else {
				fmt.Printf("[CACHE MISS] %.2fs\n", time.Since(stepStart).Seconds())
				if !opts.NoCache {
					cacheIndex[cacheKey] = cacheKey
					cacheDirty = true
				}
			}
		case "ENV":
			key, value, err := parseEnvPair(inst.Args[0])
			if err != nil {
				return fmt.Errorf("line %d: %w", inst.Line, err)
			}
			envMap[key] = value
			imageConfig.Env = store.EnvMapToList(envMap)
			cacheKey := cache.HashParts(prevDigest, inst.Raw, serializeEnv(envMap), imageConfig.WorkingDir, serializeCmd(imageConfig.Cmd))
			hit := false
			if !opts.NoCache {
				if cachedDigest, ok := cacheIndex[cacheKey]; ok && cachedDigest == cacheKey {
					hit = true
				}
			}
			prevDigest = cacheKey
			if hit {
				fmt.Printf("[CACHE HIT] %.2fs\n", time.Since(stepStart).Seconds())
			} else {
				fmt.Printf("[CACHE MISS] %.2fs\n", time.Since(stepStart).Seconds())
				if !opts.NoCache {
					cacheIndex[cacheKey] = cacheKey
					cacheDirty = true
				}
			}
		case "WORKDIR":
			imageConfig.WorkingDir = normalizeContainerPath(imageConfig.WorkingDir, inst.Args[0])
			pendingWorkdirCreate = true
			cacheKey := cache.HashParts(prevDigest, inst.Raw, serializeEnv(envMap), imageConfig.WorkingDir, serializeCmd(imageConfig.Cmd))
			hit := false
			if !opts.NoCache {
				if cachedDigest, ok := cacheIndex[cacheKey]; ok && cachedDigest == cacheKey {
					hit = true
				}
			}
			prevDigest = cacheKey
			if hit {
				fmt.Printf("[CACHE HIT] %.2fs\n", time.Since(stepStart).Seconds())
			} else {
				fmt.Printf("[CACHE MISS] %.2fs\n", time.Since(stepStart).Seconds())
				if !opts.NoCache {
					cacheIndex[cacheKey] = cacheKey
					cacheDirty = true
				}
			}
		case "CMD":
			cmd, err := parseCmdJSON(inst.Args[0])
			if err != nil {
				return fmt.Errorf("line %d: %w", inst.Line, err)
			}
			imageConfig.Cmd = cmd
			cacheKey := cache.HashParts(prevDigest, inst.Raw, serializeEnv(envMap), imageConfig.WorkingDir, serializeCmd(imageConfig.Cmd))
			hit := false
			if !opts.NoCache {
				if cachedDigest, ok := cacheIndex[cacheKey]; ok && cachedDigest == cacheKey {
					hit = true
				}
			}
			prevDigest = cacheKey
			if hit {
				fmt.Printf("[CACHE HIT] %.2fs\n", time.Since(stepStart).Seconds())
			} else {
				fmt.Printf("[CACHE MISS] %.2fs\n", time.Since(stepStart).Seconds())
				if !opts.NoCache {
					cacheIndex[cacheKey] = cacheKey
					cacheDirty = true
				}
			}
		case "COPY":
			if prevDigest == "" {
				return fmt.Errorf("line %d: FROM must appear before COPY", inst.Line)
			}
			if pendingWorkdirCreate {
				if err := ensureWorkDirExists(rootFS, imageConfig.WorkingDir); err != nil {
					return err
				}
				pendingWorkdirCreate = false
			}

			sources, err := expandCopySources(absContext, inst.Args[0])
			if err != nil {
				return fmt.Errorf("line %d: %w", inst.Line, err)
			}

			sourceHash, err := hashCopySources(absContext, sources)
			if err != nil {
				return err
			}

			cacheKey := cache.HashParts(
				prevDigest,
				inst.Raw,
				imageConfig.WorkingDir,
				serializeEnv(envMap),
				sourceHash,
			)

			resolvedDst := normalizeContainerPath(imageConfig.WorkingDir, inst.Args[1])
			if strings.HasSuffix(inst.Args[1], "/") || inst.Args[1] == "." || inst.Args[1] == "./" {
				if !strings.HasSuffix(resolvedDst, "/") {
					resolvedDst += "/"
				}
			}

			stepStart := time.Now()
			digest, size, hit, err := runCopyStep(absContext, rootFS, sources, resolvedDst, inst.Raw, cacheKey, cacheIndex, opts.NoCache, cascadeMiss)
			if err != nil {
				return err
			}

			if hit {
				fmt.Printf("[CACHE HIT] %.2fs\n", time.Since(stepStart).Seconds())
			} else {
				fmt.Printf("[CACHE MISS] %.2fs\n", time.Since(stepStart).Seconds())
				cascadeMiss = true
				if !opts.NoCache {
					cacheIndex[cacheKey] = digest
					cacheDirty = true
				}
			}

			manifestLayers = append(manifestLayers, store.LayerDescriptor{Digest: digest, Size: size, CreatedBy: inst.Raw})
			prevDigest = digest
		case "RUN":
			if prevDigest == "" {
				return fmt.Errorf("line %d: FROM must appear before RUN", inst.Line)
			}
			if err := validateLocalCommand(inst.Args[0]); err != nil {
				return fmt.Errorf("line %d: %w", inst.Line, err)
			}
			if pendingWorkdirCreate {
				if err := ensureWorkDirExists(rootFS, imageConfig.WorkingDir); err != nil {
					return err
				}
				pendingWorkdirCreate = false
			}

			cacheKey := cache.HashParts(
				prevDigest,
				inst.Raw,
				imageConfig.WorkingDir,
				serializeEnv(envMap),
			)

			stepStart := time.Now()
			digest, size, hit, err := runCommandStep(rootFS, imageConfig.WorkingDir, envMap, inst.Args[0], inst.Raw, cacheKey, cacheIndex, opts.NoCache, cascadeMiss)
			if err != nil {
				return err
			}

			if hit {
				fmt.Printf("[CACHE HIT] %.2fs\n", time.Since(stepStart).Seconds())
			} else {
				fmt.Printf("[CACHE MISS] %.2fs\n", time.Since(stepStart).Seconds())
				cascadeMiss = true
				if !opts.NoCache {
					cacheIndex[cacheKey] = digest
					cacheDirty = true
				}
			}

			manifestLayers = append(manifestLayers, store.LayerDescriptor{Digest: digest, Size: size, CreatedBy: inst.Raw})
			prevDigest = digest
		default:
			return fmt.Errorf("line %d: unsupported instruction %q", inst.Line, inst.Op)
		}
	}

	manifest := store.ImageManifest{
		Name:   name,
		Tag:    imageTag,
		Config: imageConfig,
		Layers: manifestLayers,
	}

	if err := store.SaveImage(manifest); err != nil {
		return err
	}

	if cacheDirty && !opts.NoCache {
		if err := cache.SaveCacheIndex(cacheIndex); err != nil {
			return err
		}
	}

	fmt.Printf("Build completed in %.2fs\n", time.Since(buildStart).Seconds())

	saved, err := store.LoadImage(name + ":" + imageTag)
	if err != nil {
		return err
	}

	fmt.Printf("Successfully built %s %s\n", saved.Digest, name+":"+imageTag)
	return nil
}

func validateLocalCommand(command string) error {
	lowered := strings.ToLower(command)
	blocked := []string{"curl", "wget", "apt-get", "apk add", "pip install", "npm install", "go get", "git clone"}
	for _, item := range blocked {
		if strings.Contains(lowered, item) {
			return fmt.Errorf("network or package-install command %q is not allowed", item)
		}
	}

	return nil
}

func serializeCmd(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}

	return strings.Join(cmd, "\x00")
}

func runCopyStep(contextDir string, rootFS string, sources []string, dst string, createdBy string, cacheKey string, cacheIndex map[string]string, noCache bool, cascadeMiss bool) (string, int64, bool, error) {
	if !noCache && !cascadeMiss {
		if digest, ok := cacheIndex[cacheKey]; ok {
			exists, err := layers.LayerExists(digest)
			if err != nil {
				return "", 0, false, err
			}
			if exists {
				if err := layers.ExtractLayer(digest, rootFS); err != nil {
					return "", 0, false, err
				}
				size, err := layers.GetLayerSize(digest)
				if err != nil {
					return "", 0, false, err
				}
				return digest, size, true, nil
			}
		}
	}

	preStateDir, err := snapshotDirectory(rootFS)
	if err != nil {
		return "", 0, false, err
	}
	defer os.RemoveAll(preStateDir)

	if err := copyFromContext(contextDir, sources, rootFS, dst); err != nil {
		return "", 0, false, err
	}

	digest, size, err := layers.WriteDeltaLayer(preStateDir, rootFS)
	if err != nil {
		return "", 0, false, fmt.Errorf("write layer for %q: %w", createdBy, err)
	}

	return digest, size, false, nil
}

func runCommandStep(rootFS string, workDir string, env map[string]string, command string, createdBy string, cacheKey string, cacheIndex map[string]string, noCache bool, cascadeMiss bool) (string, int64, bool, error) {
	if !noCache && !cascadeMiss {
		if digest, ok := cacheIndex[cacheKey]; ok {
			exists, err := layers.LayerExists(digest)
			if err != nil {
				return "", 0, false, err
			}
			if exists {
				if err := layers.ExtractLayer(digest, rootFS); err != nil {
					return "", 0, false, err
				}
				size, err := layers.GetLayerSize(digest)
				if err != nil {
					return "", 0, false, err
				}
				return digest, size, true, nil
			}
		}
	}

	preStateDir, err := snapshotDirectory(rootFS)
	if err != nil {
		return "", 0, false, err
	}
	defer os.RemoveAll(preStateDir)

	if err := dockruntime.ExecuteShellInRootFS(rootFS, workDir, env, command); err != nil {
		return "", 0, false, fmt.Errorf("RUN command %q failed: %w", command, err)
	}

	digest, size, err := layers.WriteDeltaLayer(preStateDir, rootFS)
	if err != nil {
		return "", 0, false, fmt.Errorf("write layer for %q: %w", createdBy, err)
	}

	return digest, size, false, nil
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

func parseCmdJSON(raw string) ([]string, error) {
	var cmd []string
	if err := json.Unmarshal([]byte(raw), &cmd); err != nil {
		return nil, fmt.Errorf("CMD must be JSON array form, got %q", raw)
	}
	if len(cmd) == 0 {
		return nil, fmt.Errorf("CMD cannot be empty")
	}
	for _, part := range cmd {
		if strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("CMD arguments must not be empty strings")
		}
	}
	return cmd, nil
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

func ensureWorkDirExists(rootFS string, workDir string) error {
	hostWorkDir := filepath.Join(rootFS, trimLeadingSlash(workDir))
	if err := os.MkdirAll(hostWorkDir, 0o755); err != nil {
		return fmt.Errorf("create WORKDIR %q: %w", workDir, err)
	}
	return nil
}

func copyFromContext(contextDir string, sources []string, rootFS string, dst string) error {
	destPath := filepath.Join(rootFS, filepath.FromSlash(trimLeadingSlash(dst)))
	destIsDir := strings.HasSuffix(dst, "/")

	if len(sources) > 1 && !destIsDir {
		return fmt.Errorf("COPY with multiple sources requires destination directory")
	}

	for _, src := range sources {
		sourcePath := filepath.Join(contextDir, filepath.FromSlash(src))
		info, err := os.Stat(sourcePath)
		if err != nil {
			return fmt.Errorf("stat COPY source %q: %w", sourcePath, err)
		}

		targetPath := destPath
		if len(sources) > 1 || destIsDir {
			targetPath = filepath.Join(destPath, filepath.Base(sourcePath))
		}

		if info.IsDir() {
			if err := copyDirectory(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}

		if err := copyFile(sourcePath, targetPath); err != nil {
			return err
		}
	}

	return nil
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

func snapshotDirectory(source string) (string, error) {
	snapshot, err := os.MkdirTemp("", "docksmith-pre-step-")
	if err != nil {
		return "", fmt.Errorf("create pre-step snapshot dir: %w", err)
	}

	if err := filepath.WalkDir(source, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		target := filepath.Join(snapshot, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}

		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}

		return copyFile(path, target)
	}); err != nil {
		_ = os.RemoveAll(snapshot)
		return "", fmt.Errorf("create pre-step snapshot: %w", err)
	}

	return snapshot, nil
}

func serializeEnv(env map[string]string) string {
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

func normalizeImageRef(ref string) string {
	if strings.Contains(ref, ":") {
		return ref
	}
	return ref + ":latest"
}

func expandCopySources(contextDir string, pattern string) ([]string, error) {
	cleanPattern := filepath.ToSlash(strings.TrimSpace(pattern))
	if cleanPattern == "" {
		return nil, fmt.Errorf("COPY source cannot be empty")
	}
	if filepath.IsAbs(filepath.FromSlash(cleanPattern)) || strings.HasPrefix(cleanPattern, "/") {
		return nil, fmt.Errorf("COPY source %q must be relative to the build context", pattern)
	}

	matches := make([]string, 0)
	if strings.Contains(cleanPattern, "**") {
		re, err := globToRegex(cleanPattern)
		if err != nil {
			return nil, err
		}

		err = filepath.WalkDir(contextDir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(contextDir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == "." {
				return nil
			}
			if re.MatchString(rel) {
				matches = append(matches, rel)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("expand COPY pattern %q: %w", pattern, err)
		}
	} else {
		globMatches, err := filepath.Glob(filepath.Join(contextDir, filepath.FromSlash(cleanPattern)))
		if err != nil {
			return nil, fmt.Errorf("expand COPY pattern %q: %w", pattern, err)
		}
		for _, path := range globMatches {
			rel, err := filepath.Rel(contextDir, path)
			if err != nil {
				return nil, err
			}
			rel = filepath.ToSlash(rel)
			if !isPathWithinContext(rel) {
				return nil, fmt.Errorf("COPY source %q resolves outside build context", pattern)
			}
			matches = append(matches, rel)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("COPY source %q matched no files", pattern)
	}

	sort.Strings(matches)
	uniq := matches[:0]
	for i, m := range matches {
		if i == 0 || matches[i-1] != m {
			if !isPathWithinContext(m) {
				return nil, fmt.Errorf("COPY source %q resolves outside build context", m)
			}
			uniq = append(uniq, m)
		}
	}

	return uniq, nil
}

func isPathWithinContext(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || clean == "" {
		return false
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	if filepath.IsAbs(filepath.FromSlash(clean)) || strings.HasPrefix(clean, "/") {
		return false
	}
	return true
}
func globToRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.':
			b.WriteString("\\.")
		case '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")

	return regexp.Compile(b.String())
}

func hashCopySources(contextDir string, sources []string) (string, error) {
	entries := make([]string, 0)

	for _, rel := range sources {
		if !isPathWithinContext(rel) {
			return "", fmt.Errorf("COPY source %q resolves outside build context", rel)
		}

		abs := filepath.Join(contextDir, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("stat COPY source %q: %w", abs, err)
		}

		if info.IsDir() {
			err := filepath.WalkDir(abs, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					return nil
				}
				innerRel, err := filepath.Rel(contextDir, path)
				if err != nil {
					return err
				}
				innerRel = filepath.ToSlash(innerRel)
				if !isPathWithinContext(innerRel) {
					return fmt.Errorf("COPY source %q resolves outside build context", innerRel)
				}
				entries = append(entries, innerRel)
				return nil
			})
			if err != nil {
				return "", fmt.Errorf("walk COPY directory %q: %w", rel, err)
			}
			continue
		}

		entries = append(entries, rel)
	}

	sort.Strings(entries)
	h := sha256.New()
	for _, rel := range entries {
		abs := filepath.Join(contextDir, filepath.FromSlash(rel))
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("read COPY source %q: %w", rel, err)
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

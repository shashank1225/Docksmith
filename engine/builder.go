package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"docksmith/cache"
	"docksmith/layers"
)

// Build runs a minimal build pipeline with cache reuse for COPY and RUN steps.
func Build(tag string, contextDir string) error {
	if err := layers.EnsureDocksmithDirs(); err != nil {
		return err
	}

	contextAbs, err := filepath.Abs(contextDir)
	if err != nil {
		return fmt.Errorf("resolve context path %q: %w", contextDir, err)
	}

	ctxInfo, err := os.Stat(contextAbs)
	if err != nil {
		return fmt.Errorf("stat context path %q: %w", contextAbs, err)
	}
	if !ctxInfo.IsDir() {
		return fmt.Errorf("build context %q is not a directory", contextAbs)
	}

	steps := []string{"COPY . /app", "RUN build"}
	prevDigest := "base"
	cacheChainValid := true
	workdir := "/app"
	env := map[string]string{}

	fmt.Println("Step 1/3 : FROM base")

	for i, instruction := range steps {
		fmt.Printf("Step %d/3 : %s\n", i+2, instruction)

		key := cache.ComputeCacheKey(prevDigest, instruction, workdir, env)

		if cacheChainValid {
			if digest, found := cache.LookupCache(key); found && layers.LayerExists(digest) {
				fmt.Println("[CACHE HIT]")
				prevDigest = digest
				continue
			}
		}

		fmt.Println("[CACHE MISS]")
		cacheChainValid = false

		digest, err := layers.CreateLayerFromDir(contextAbs)
		if err != nil {
			return fmt.Errorf("create layer for instruction %q: %w", instruction, err)
		}

		fmt.Printf("-> creates layer %s.tar\n", digest)

		if err := cache.StoreCache(key, digest); err != nil {
			return fmt.Errorf("store cache for instruction %q: %w", instruction, err)
		}

		prevDigest = digest
	}

	fmt.Printf("BUILD completed for tag=%s, context=%s\n", tag, contextAbs)
	return nil
}

package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"docksmith/cache"
	"docksmith/store"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <alpine-rootfs.tar|alpine-rootfs.tar.gz>\n", filepath.Base(os.Args[0]))
		os.Exit(1)
	}

	sourcePath := os.Args[1]
	fmt.Printf("Importing Alpine base image from %s...\n", sourcePath)

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening source file: %v\n", err)
		os.Exit(1)
	}
	defer sourceFile.Close()

	reader := io.Reader(sourceFile)
	if strings.HasSuffix(strings.ToLower(sourcePath), ".gz") {
		gz, err := gzip.NewReader(sourceFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating gzip reader: %v\n", err)
			os.Exit(1)
		}
		defer gz.Close()
		reader = gz
	}

	tmpFile, err := os.CreateTemp("", "alpine-*.tar")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating temp file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmpFile.Name())

	hash := sha256.New()
	multiWriter := io.MultiWriter(tmpFile, hash)

	size, err := io.Copy(multiWriter, reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting tar: %v\n", err)
		os.Exit(1)
	}
	tmpFile.Close()

	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	fmt.Printf("Extracted alpine rootfs: digest=%s, size=%d bytes\n", digest, size)

	if err := cache.EnsureLayout(); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating docksmith layout: %v\n", err)
		os.Exit(1)
	}

	layerPath, err := cache.LayerPath(digest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving layer path: %v\n", err)
		os.Exit(1)
	}

	// 3. Move the uncompressed tar to the layers directory
	if err := copyFile(tmpFile.Name(), layerPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing layer to storage: %v\n", err)
		os.Exit(1)
	}

	// 4. Create the manifest and save it
	manifest := store.ImageManifest{
		Name:    "alpine",
		Tag:     "3.18",
		Created: time.Now().UTC().Format(time.RFC3339),
		Config: store.ImageConfig{
			Env:        []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			WorkingDir: "/",
			Cmd:        []string{"/bin/sh"},
		},
		Layers: []store.LayerDescriptor{
			{
				Digest:    digest,
				Size:      size,
				CreatedBy: "alpine 3.18 base import",
			},
		},
	}

	if err := store.SaveImage(manifest); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving base image manifest: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Successfully imported alpine:3.18 into Docksmith!")
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

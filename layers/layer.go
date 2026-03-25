package layers

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	docksmithDirName = ".docksmith"
	layersDirName    = "layers"
	cacheDirName     = "cache"
	imagesDirName    = "images"
)

// EnsureDocksmithDirs makes sure all runtime storage directories exist.
func EnsureDocksmithDirs() error {
	root, err := docksmithRoot()
	if err != nil {
		return err
	}

	dirs := []string{
		filepath.Join(root, layersDirName),
		filepath.Join(root, cacheDirName),
		filepath.Join(root, imagesDirName),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create docksmith directory %q: %w", dir, err)
		}
	}

	return nil
}

// CreateLayerFromDir snapshots dir into a deterministic tar, stores it, and returns sha256_<hash>.
func CreateLayerFromDir(dir string) (string, error) {
	if err := EnsureDocksmithDirs(); err != nil {
		return "", err
	}

	root, err := docksmithRoot()
	if err != nil {
		return "", err
	}

	layersDir := filepath.Join(root, layersDirName)
	if err := os.MkdirAll(layersDir, 0o755); err != nil {
		return "", fmt.Errorf("create layers directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(layersDir, "layer-*.tar")
	if err != nil {
		return "", fmt.Errorf("create temp layer file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	multiWriter := io.MultiWriter(tmpFile, hasher)
	tarWriter := tar.NewWriter(multiWriter)

	if err := writeDeterministicTar(tarWriter, dir); err != nil {
		_ = tarWriter.Close()
		_ = tmpFile.Close()
		return "", err
	}

	if err := tarWriter.Close(); err != nil {
		_ = tmpFile.Close()
		return "", fmt.Errorf("close tar writer: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("close temp layer file: %w", err)
	}

	hexHash := hex.EncodeToString(hasher.Sum(nil))
	digest := "sha256_" + hexHash
	finalPath := filepath.Join(layersDir, digest+".tar")

	if _, err := os.Stat(finalPath); err == nil {
		return digest, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("check existing layer file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("move layer file into place: %w", err)
	}

	return digest, nil
}

// ExtractLayer unpacks a previously stored layer tar into targetDir.
func ExtractLayer(digest string, targetDir string) error {
	layerPath, err := layerPathForDigest(digest)
	if err != nil {
		return err
	}

	file, err := os.Open(layerPath)
	if err != nil {
		return fmt.Errorf("open layer %q: %w", digest, err)
	}
	defer file.Close()

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create target directory %q: %w", targetDir, err)
	}

	targetAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolve target directory %q: %w", targetDir, err)
	}

	tarReader := tar.NewReader(file)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read layer archive entry: %w", err)
		}

		rel := filepath.Clean(header.Name)
		if rel == "." {
			continue
		}

		destPath := filepath.Join(targetAbs, rel)
		destAbs, err := filepath.Abs(destPath)
		if err != nil {
			return fmt.Errorf("resolve destination path: %w", err)
		}

		if destAbs != targetAbs && !strings.HasPrefix(destAbs, targetAbs+string(os.PathSeparator)) {
			return fmt.Errorf("layer entry escapes target directory: %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			mode := fs.FileMode(header.Mode)
			if mode == 0 {
				mode = 0o755
			}
			if err := os.MkdirAll(destAbs, mode); err != nil {
				return fmt.Errorf("create directory %q: %w", destAbs, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
				return fmt.Errorf("create parent directory for %q: %w", destAbs, err)
			}

			mode := fs.FileMode(header.Mode)
			if mode == 0 {
				mode = 0o644
			}

			outFile, err := os.OpenFile(destAbs, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return fmt.Errorf("create file %q: %w", destAbs, err)
			}

			if _, err := io.Copy(outFile, tarReader); err != nil {
				_ = outFile.Close()
				return fmt.Errorf("write file %q: %w", destAbs, err)
			}

			if err := outFile.Close(); err != nil {
				return fmt.Errorf("close file %q: %w", destAbs, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
				return fmt.Errorf("create parent directory for symlink %q: %w", destAbs, err)
			}
			_ = os.Remove(destAbs)
			if err := os.Symlink(header.Linkname, destAbs); err != nil {
				return fmt.Errorf("create symlink %q: %w", destAbs, err)
			}
		default:
			// Unsupported entry types are skipped in this simplified implementation.
		}
	}

	return nil
}

// LayerExists returns true when a layer tar already exists in local storage.
func LayerExists(digest string) bool {
	layerPath, err := layerPathForDigest(digest)
	if err != nil {
		return false
	}

	_, err = os.Stat(layerPath)
	return err == nil
}

func docksmithRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, docksmithDirName), nil
}

func layerPathForDigest(digest string) (string, error) {
	root, err := docksmithRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, layersDirName, digest+".tar"), nil
}

func writeDeterministicTar(tw *tar.Writer, dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve input directory %q: %w", dir, err)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("stat input directory %q: %w", absDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("input path %q is not a directory", absDir)
	}

	entries := make([]string, 0)
	err = filepath.WalkDir(absDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if path == absDir {
			return nil
		}

		rel, err := filepath.Rel(absDir, path)
		if err != nil {
			return err
		}

		entries = append(entries, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk input directory %q: %w", absDir, err)
	}

	sort.Strings(entries)
	zeroTime := time.Unix(0, 0)

	for _, rel := range entries {
		fullPath := filepath.Join(absDir, filepath.FromSlash(rel))
		fileInfo, err := os.Lstat(fullPath)
		if err != nil {
			return fmt.Errorf("stat entry %q: %w", fullPath, err)
		}

		linkName := ""
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			linkName, err = os.Readlink(fullPath)
			if err != nil {
				return fmt.Errorf("read symlink %q: %w", fullPath, err)
			}
		}

		header, err := tar.FileInfoHeader(fileInfo, linkName)
		if err != nil {
			return fmt.Errorf("create tar header for %q: %w", fullPath, err)
		}

		header.Name = rel
		header.ModTime = zeroTime
		header.AccessTime = zeroTime
		header.ChangeTime = zeroTime
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""

		if fileInfo.IsDir() {
			header.Name += "/"
			header.Mode = 0o755
		} else if fileInfo.Mode()&os.ModeSymlink != 0 {
			header.Mode = 0o777
		} else {
			header.Mode = 0o644
		}

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header for %q: %w", fullPath, err)
		}

		if fileInfo.Mode().IsRegular() {
			file, err := os.Open(fullPath)
			if err != nil {
				return fmt.Errorf("open file %q: %w", fullPath, err)
			}

			if _, err := io.Copy(tw, file); err != nil {
				_ = file.Close()
				return fmt.Errorf("write file %q into tar: %w", fullPath, err)
			}

			if err := file.Close(); err != nil {
				return fmt.Errorf("close file %q: %w", fullPath, err)
			}
		}
	}

	return nil
}

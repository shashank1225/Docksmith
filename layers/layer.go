package layers

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"docksmith/cache"
)

func LayerExists(digest string) (bool, error) {
	path, err := cache.LayerPath(digest)
	if err != nil {
		return false, err
	}

	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}

	return false, fmt.Errorf("check layer %q: %w", digest, err)
}

func GetLayerSize(digest string) (int64, error) {
	layerPath, err := cache.LayerPath(digest)
	if err != nil {
		return 0, err
	}

	info, err := os.Stat(layerPath)
	if err != nil {
		return 0, fmt.Errorf("stat layer %q: %w", layerPath, err)
	}

	return info.Size(), nil
}

func WriteDeltaLayer(previousDir string, currentDir string) (string, int64, error) {
	tmp, err := os.CreateTemp("", "docksmith-layer-*.tar")
	if err != nil {
		return "", 0, fmt.Errorf("create temp layer: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("close temp layer file: %w", err)
	}
	defer os.Remove(tmpPath)

	file, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, fmt.Errorf("open temp layer %q: %w", tmpPath, err)
	}

	w := tar.NewWriter(file)
	paths, err := changedPaths(previousDir, currentDir)
	if err != nil {
		_ = w.Close()
		_ = file.Close()
		return "", 0, err
	}

	for _, rel := range paths {
		abs := filepath.Join(currentDir, filepath.FromSlash(rel))
		info, err := os.Lstat(abs)
		if err != nil {
			_ = w.Close()
			_ = file.Close()
			return "", 0, fmt.Errorf("stat %q: %w", abs, err)
		}

		var linkTarget string
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err = os.Readlink(abs)
			if err != nil {
				_ = w.Close()
				_ = file.Close()
				return "", 0, fmt.Errorf("read symlink %q: %w", abs, err)
			}
		}

		hdr, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			_ = w.Close()
			_ = file.Close()
			return "", 0, fmt.Errorf("create tar header for %q: %w", abs, err)
		}

		hdr.Name = rel
		if info.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}
		hdr.ModTime = time.Unix(0, 0)
		hdr.AccessTime = time.Unix(0, 0)
		hdr.ChangeTime = time.Unix(0, 0)
		hdr.Uid = 0
		hdr.Gid = 0
		hdr.Uname = ""
		hdr.Gname = ""

		if err := w.WriteHeader(hdr); err != nil {
			_ = w.Close()
			_ = file.Close()
			return "", 0, fmt.Errorf("write tar header for %q: %w", abs, err)
		}

		if info.Mode().IsRegular() {
			r, err := os.Open(abs)
			if err != nil {
				_ = w.Close()
				_ = file.Close()
				return "", 0, fmt.Errorf("open file %q: %w", abs, err)
			}

			_, copyErr := io.Copy(w, r)
			closeErr := r.Close()
			if copyErr != nil {
				_ = w.Close()
				_ = file.Close()
				return "", 0, fmt.Errorf("write file %q to tar: %w", abs, copyErr)
			}
			if closeErr != nil {
				_ = w.Close()
				_ = file.Close()
				return "", 0, fmt.Errorf("close file %q: %w", abs, closeErr)
			}
		}
	}

	if err := w.Close(); err != nil {
		_ = file.Close()
		return "", 0, fmt.Errorf("finalize tar %q: %w", tmpPath, err)
	}
	if err := file.Close(); err != nil {
		return "", 0, fmt.Errorf("close temp layer %q: %w", tmpPath, err)
	}

	digest, size, err := digestAndSize(tmpPath)
	if err != nil {
		return "", 0, err
	}

	layerPath, err := cache.LayerPath(digest)
	if err != nil {
		return "", 0, err
	}

	if _, err := os.Stat(layerPath); err == nil {
		return digest, size, nil
	} else if !os.IsNotExist(err) {
		return "", 0, fmt.Errorf("stat layer %q: %w", layerPath, err)
	}

	if err := os.Rename(tmpPath, layerPath); err != nil {
		if copyErr := copyFile(tmpPath, layerPath); copyErr != nil {
			return "", 0, fmt.Errorf("store layer %q: %w", layerPath, err)
		}
	}

	return digest, size, nil
}

func changedPaths(previousDir string, currentDir string) ([]string, error) {
	paths := make([]string, 0)

	err := filepath.WalkDir(currentDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == currentDir {
			return nil
		}

		rel, err := filepath.Rel(currentDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		currentInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}

		prevPath := filepath.Join(previousDir, filepath.FromSlash(rel))
		prevInfo, prevErr := os.Lstat(prevPath)
		if os.IsNotExist(prevErr) {
			paths = append(paths, rel)
			return nil
		}
		if prevErr != nil {
			return prevErr
		}

		changed, err := fileChanged(prevPath, prevInfo, path, currentInfo)
		if err != nil {
			return err
		}
		if changed {
			paths = append(paths, rel)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk current layer source %q: %w", currentDir, err)
	}

	sort.Strings(paths)
	return paths, nil
}

func fileChanged(prevPath string, prevInfo os.FileInfo, currentPath string, currentInfo os.FileInfo) (bool, error) {
	if prevInfo.Mode().Type() != currentInfo.Mode().Type() {
		return true, nil
	}

	if prevInfo.IsDir() && currentInfo.IsDir() {
		return prevInfo.Mode().Perm() != currentInfo.Mode().Perm(), nil
	}

	if prevInfo.Mode()&os.ModeSymlink != 0 && currentInfo.Mode()&os.ModeSymlink != 0 {
		oldTarget, err := os.Readlink(prevPath)
		if err != nil {
			return false, err
		}
		newTarget, err := os.Readlink(currentPath)
		if err != nil {
			return false, err
		}
		return oldTarget != newTarget, nil
	}

	if prevInfo.Mode().Perm() != currentInfo.Mode().Perm() || prevInfo.Size() != currentInfo.Size() {
		return true, nil
	}

	prevData, err := os.ReadFile(prevPath)
	if err != nil {
		return false, err
	}
	currentData, err := os.ReadFile(currentPath)
	if err != nil {
		return false, err
	}

	return !bytes.Equal(prevData, currentData), nil
}

func digestAndSize(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open layer file %q: %w", path, err)
	}
	defer file.Close()

	h := sha256.New()
	n, err := io.Copy(h, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash layer file %q: %w", path, err)
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

func copyFile(src string, dst string) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()

	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = io.Copy(w, r)
	return err
}

// ExtractLayer extracts a layer tar archive to the target directory.
func ExtractLayer(digest string, targetDir string) error {
	layerPath, err := cache.LayerPath(digest)
	if err != nil {
		return err
	}

	file, err := os.Open(layerPath)
	if err != nil {
		return fmt.Errorf("open layer archive %q: %w", layerPath, err)
	}
	defer file.Close()

	r := tar.NewReader(file)
	for {
		hdr, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read layer archive %q: %w", layerPath, err)
		}

		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("invalid path %q in layer %q", hdr.Name, layerPath)
		}
		if cleanName == "." {
			continue
		}

		targetPath := filepath.Join(targetDir, cleanName)
		if !strings.HasPrefix(targetPath, filepath.Clean(targetDir)+string(filepath.Separator)) && filepath.Clean(targetPath) != filepath.Clean(targetDir) {
			return fmt.Errorf("path traversal attempt %q in layer %q", hdr.Name, layerPath)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("create directory %q: %w", targetPath, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create parent directory for %q: %w", targetPath, err)
			}

			w, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("open file %q: %w", targetPath, err)
			}

			_, copyErr := io.Copy(w, r)
			closeErr := w.Close()
			if copyErr != nil {
				return fmt.Errorf("extract file %q: %w", targetPath, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close file %q: %w", targetPath, closeErr)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create parent directory for symlink %q: %w", targetPath, err)
			}
			if err := os.RemoveAll(targetPath); err != nil {
				return fmt.Errorf("clear existing path for symlink %q: %w", targetPath, err)
			}
			if err := os.Symlink(hdr.Linkname, targetPath); err != nil {
				return fmt.Errorf("create symlink %q -> %q: %w", targetPath, hdr.Linkname, err)
			}
		default:
			return fmt.Errorf("unsupported tar entry type %d for %q", hdr.Typeflag, hdr.Name)
		}
	}

	return nil
}

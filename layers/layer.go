package layers

import (
	"archive/tar"
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

func WriteSnapshotLayer(digest string, sourceDir string) error {
	layerPath, err := cache.LayerPath(digest)
	if err != nil {
		return err
	}

	file, err := os.Create(layerPath)
	if err != nil {
		return fmt.Errorf("create layer archive %q: %w", layerPath, err)
	}
	defer file.Close()

	w := tar.NewWriter(file)
	defer w.Close()

	paths := make([]string, 0)
	err = filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceDir {
			return nil
		}

		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		rel = filepath.ToSlash(rel)
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk layer source %q: %w", sourceDir, err)
	}

	sort.Strings(paths)
	for _, rel := range paths {
		abs := filepath.Join(sourceDir, filepath.FromSlash(rel))
		info, err := os.Lstat(abs)
		if err != nil {
			return fmt.Errorf("stat %q: %w", abs, err)
		}

		var linkTarget string
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err = os.Readlink(abs)
			if err != nil {
				return fmt.Errorf("read symlink %q: %w", abs, err)
			}
		}

		hdr, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return fmt.Errorf("create tar header for %q: %w", abs, err)
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
			return fmt.Errorf("write tar header for %q: %w", abs, err)
		}

		if info.Mode().IsRegular() {
			r, err := os.Open(abs)
			if err != nil {
				return fmt.Errorf("open file %q: %w", abs, err)
			}

			_, copyErr := io.Copy(w, r)
			closeErr := r.Close()
			if copyErr != nil {
				return fmt.Errorf("write file %q to tar: %w", abs, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close file %q: %w", abs, closeErr)
			}
		}
	}

	return nil
}

// ExtractLayer extracts a layer tar archive to the target directory
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
		if cleanName == "." || strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("invalid path %q in layer %q", hdr.Name, layerPath)
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

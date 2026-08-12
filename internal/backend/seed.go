package backend

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// tarEntrySafe reports whether a tar entry name (and, for symlinks, its link
// target) stays within the extraction root. It rejects absolute paths and any
// ".." traversal, which `tar -x` would otherwise honor and use to write
// outside the workspace (tar-slip).
func tarEntrySafe(name, linkname string) bool {
	if !relWithinRoot(name) {
		return false
	}
	if linkname != "" && !relWithinRoot(linkname) {
		return false
	}
	return true
}

// relWithinRoot reports whether p is a relative path that does not escape its
// root via "..".
func relWithinRoot(p string) bool {
	if p == "" {
		return true
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	clean := path.Clean(p)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

// filterTarSafe copies a tar stream from src to dst, passing through only
// entries whose paths stay within the extraction root and erroring on any
// unsafe entry. It lets a snapshot be sanitized host-side before it is streamed
// into a container's `tar -x`.
func filterTarSafe(dst io.Writer, src io.Reader) error {
	tr := tar.NewReader(src)
	tw := tar.NewWriter(dst)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("restore: read archive: %w", err)
		}
		if !tarEntrySafe(hdr.Name, hdr.Linkname) {
			_ = tw.Close()
			return fmt.Errorf("restore: unsafe archive entry %q -> %q", hdr.Name, hdr.Linkname)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := io.Copy(tw, tr); err != nil {
				return err
			}
		}
	}
	return tw.Close()
}

// verifySnapshot checks that the snapshot tarball still matches the digest
// recorded at capture time. A snapshot without a recorded digest (older
// captures) is accepted for backward compatibility.
func verifySnapshot(ref SnapshotRef) error {
	if ref.Sha256 == "" {
		return nil
	}
	sum, err := hashFile(ref.Location)
	if err != nil {
		return fmt.Errorf("restore: hash snapshot: %w", err)
	}
	if sum != ref.Sha256 {
		return fmt.Errorf("restore: snapshot integrity check failed (tarball digest %s != recorded %s)", sum, ref.Sha256)
	}
	return nil
}

// hashFile returns the hex SHA-256 of the file at path.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeDirTar tars the contents of srcDir into w with paths relative to
// srcDir. Regular files, directories, and symlinks only; sockets, devices,
// and fifos are skipped. The source is never modified.
func writeDirTar(w io.Writer, srcDir string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return fmt.Errorf("seed source %q: %w", srcDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("seed source %q is not a directory", srcDir)
	}
	root, err := os.OpenRoot(srcDir)
	if err != nil {
		return fmt.Errorf("seed source root %q: %w", srcDir, err)
	}
	defer root.Close()

	tw := tar.NewWriter(w)
	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == srcDir {
			return nil // don't emit the root itself
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)

		fi, err := d.Info()
		if err != nil {
			return err
		}
		mode := int64(fi.Mode().Perm())

		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: mode})
		case fi.Mode()&fs.ModeSymlink != 0:
			link, err := root.Readlink(rel)
			if err != nil {
				return err
			}
			return tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: link, Mode: mode})
		case fi.Mode().IsRegular():
			if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: fi.Size()}); err != nil {
				return err
			}
			f, err := root.Open(rel)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			_ = f.Close()
			return err
		default:
			return nil // skip sockets, devices, fifos
		}
	})
	if walkErr != nil {
		_ = tw.Close()
		return walkErr
	}
	return tw.Close()
}

// extractTar writes a tar stream's files, directories, and symlinks into
// destDir. Paths are sanitized so a malicious archive can't escape destDir.
func extractTar(r io.Reader, destDir string) error {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("export: create destination root: %w", err)
	}
	root, err := os.OpenRoot(destDir)
	if err != nil {
		return fmt.Errorf("export: open destination root: %w", err)
	}
	defer root.Close()

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("export: read archive: %w", err)
		}
		entry := archiveEntryPath(hdr.Name)
		mode := hdr.FileInfo().Mode().Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(entry, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
				return err
			}
			f, err := root.OpenFile(entry, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if !symlinkWithinArchive(entry, hdr.Linkname) {
				return fmt.Errorf("export: unsafe symlink %q -> %q in archive", hdr.Name, hdr.Linkname)
			}
			if err := root.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
				return err
			}
			if err := root.Remove(entry); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := root.Symlink(hdr.Linkname, entry); err != nil {
				return err
			}
		default:
			// skip fifos, devices, etc.
		}
	}
}

// archiveEntryPath returns a relative path suitable for os.Root. Prefixing
// with a slash before cleaning contains absolute names and parent traversal at
// the extraction root while preserving the established extraction behavior.
func archiveEntryPath(name string) string {
	clean := path.Clean("/" + filepath.ToSlash(name))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" {
		return "."
	}
	return filepath.FromSlash(clean)
}

// symlinkWithinArchive reports whether a link target resolves inside the
// archive root. os.Root then enforces the same boundary during later opens,
// including when a path component is replaced concurrently.
func symlinkWithinArchive(entry, linkname string) bool {
	if filepath.IsAbs(linkname) || filepath.VolumeName(linkname) != "" {
		return false
	}
	resolved := path.Clean(path.Join(path.Dir(filepath.ToSlash(entry)), filepath.ToSlash(linkname)))
	return resolved != ".." && !strings.HasPrefix(resolved, "../") && !strings.HasPrefix(resolved, "/")
}

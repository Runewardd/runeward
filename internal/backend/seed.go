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
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: link, Mode: mode})
		case fi.Mode().IsRegular():
			if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: fi.Size()}); err != nil {
				return err
			}
			f, err := os.Open(path)
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
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("export: read archive: %w", err)
		}
		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}
		mode := fs.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o600)
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
			if !symlinkWithinBase(destDir, target, hdr.Linkname) {
				return fmt.Errorf("export: unsafe symlink %q -> %q in archive", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// skip fifos, devices, etc.
		}
	}
}

// safeJoin joins name onto base, guaranteeing the result stays within base
// (a crafted entry like "../../etc/passwd" must not escape).
func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean("/" + filepath.ToSlash(name))
	target := filepath.Join(base, clean)
	if !withinBase(base, target) {
		return "", fmt.Errorf("export: unsafe path %q in archive", name)
	}
	return target, nil
}

// symlinkWithinBase reports whether a symlink at linkPath pointing at linkname
// resolves to a location inside base. Absolute targets are resolved as-is;
// relative ones are resolved against the link's own directory.
func symlinkWithinBase(base, linkPath, linkname string) bool {
	var resolved string
	if filepath.IsAbs(linkname) {
		resolved = filepath.Clean(linkname)
	} else {
		resolved = filepath.Clean(filepath.Join(filepath.Dir(linkPath), linkname))
	}
	return withinBase(base, resolved)
}

// withinBase reports whether target is base itself or nested under it.
func withinBase(base, target string) bool {
	return target == base || strings.HasPrefix(target, base+string(os.PathSeparator))
}

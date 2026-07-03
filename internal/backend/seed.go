package backend

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// writeDirTar streams the contents of srcDir into w as a tar archive with
// paths relative to srcDir, so extracting it at a sandbox workdir reproduces
// the tree there. Regular files, directories, and symlinks are included; other
// special files (sockets, devices, fifos) are skipped.
//
// It is used to seed a fresh, isolated workspace with a copy of a local
// directory: the source is only read, never modified, and extraction happens
// inside the sandbox as the sandbox user, so the copy is owned by that user.
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

// extractTar reads a tar stream from r and writes its regular files,
// directories, and symlinks into destDir. It is the inverse of writeDirTar and
// backs `runeward export`: pulling a sandbox workspace back out to a host
// directory. Paths are sanitized against traversal so a malicious archive
// cannot escape destDir.
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

// safeJoin joins name onto base, guaranteeing the result stays within base so a
// crafted archive entry (e.g. "../../etc/passwd") cannot write outside destDir.
func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean("/" + filepath.ToSlash(name))
	target := filepath.Join(base, clean)
	if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("export: unsafe path %q in archive", name)
	}
	return target, nil
}

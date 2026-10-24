// Package fsutil holds file helpers shared by the downloader and pipeline.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Move renames src to dst, falling back to copy+delete across filesystems.
func Move(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := Copy(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// Copy copies a regular file through a temporary name, so dst never exists
// half-written.
func Copy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	tmp := dst + ".tmp-copy"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// UniquePath returns path, or "name (2).ext", "name (3).ext"... if taken.
func UniquePath(path string) string {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		p := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
	}
}

var reUnsafe = regexp.MustCompile(`[/\\\x00-\x1f]`)

// SafeName makes a single path component out of an untrusted file name.
func SafeName(name string) string {
	name = reUnsafe.ReplaceAllString(name, "_")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	if len(name) > 240 {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		name = name[:240-len(ext)] + ext
	}
	return name
}

// IsArchive sniffs the file magic (rar, zip, 7z, gzip, xz, bzip2, zstd):
// downloaded names often carry no or a misleading extension.
func IsArchive(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [8]byte
	n, _ := io.ReadFull(f, hdr[:])
	b := hdr[:n]
	has := func(p string) bool { return len(b) >= len(p) && string(b[:len(p)]) == p }
	return has("Rar!") ||
		has("PK\x03\x04") || has("PK\x05\x06") || has("PK\x07\x08") ||
		has("7z\xbc\xaf'\x1c") ||
		has("\x1f\x8b\x08") ||
		has("\xfd7zXZ\x00") ||
		has("BZh") ||
		has("\x28\xb5\x2f\xfd")
}

// ListFiles returns every regular file below dir (recursive), sorted.
func ListFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// RemoveAll deletes every path, ignoring missing ones.
func RemoveAll(paths []string) {
	for _, p := range paths {
		os.Remove(p)
	}
}

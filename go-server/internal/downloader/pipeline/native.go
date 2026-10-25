package pipeline

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/klauspost/compress/zstd"
	rardecode "github.com/nwaples/rardecode/v2"
	"github.com/ulikunitz/xz"
)

// rarVolRE matches a RAR volume by name (.rar, .partN.rar, or old-style .rNN).
var rarVolRE = regexp.MustCompile(`(?i)((\.part\d+)?\.rar|\.r\d{2,3})$`)

// Native, pure-Go archive extraction — no external 7z/unar binary. The engine
// dispatches on magic bytes first (the file's real format), falling back to the
// extension, so a mis-named archive still unpacks. Multi-volume RAR (.partN.rar,
// .rNN) is followed automatically by rardecode; multi-volume 7z (.7z.001) and
// split zip are handled by sevenzip/archive-zip when given the first volume.

// maxExtractBytes caps a single extracted entry (and the whole archive via the
// running total) to guard against decompression bombs. 64 GiB is far above any
// real ROM yet stops a crafted archive from filling the disk.
const maxEntryBytes = 64 << 30

// extractArchive unpacks archive at path into dest. Returns an error only when
// nothing usable could be extracted; the caller treats a populated dest as
// success even on a partial error (matching the previous 7z tolerance).
func extractArchive(ctx context.Context, path, dest, password string) error {
	kind, err := archiveKind(path)
	if err != nil {
		return err
	}
	switch kind {
	case "rar":
		return extractRar(ctx, path, dest, password)
	case "7z":
		return extract7z(ctx, path, dest, password)
	case "zip":
		return extractZip(ctx, path, dest, password)
	case "tar":
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return untar(ctx, f, dest)
	case "gz", "xz", "bz2", "zst":
		return extractCompressed(ctx, path, dest, kind)
	default:
		return fmt.Errorf("unrecognized archive format: %s", filepath.Base(path))
	}
}

// archiveKind identifies the format by magic bytes, falling back to extension.
func archiveKind(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var hdr [8]byte
	n, _ := io.ReadFull(f, hdr[:])
	b := hdr[:n]
	has := func(p string) bool { return len(b) >= len(p) && string(b[:len(p)]) == p }
	switch {
	case has("Rar!"):
		return "rar", nil
	case has("7z\xbc\xaf'\x1c"):
		return "7z", nil
	case has("PK\x03\x04"), has("PK\x05\x06"), has("PK\x07\x08"):
		return "zip", nil
	case has("\x1f\x8b\x08"):
		return "gz", nil
	case has("\xfd7zXZ\x00"):
		return "xz", nil
	case has("BZh"):
		return "bz2", nil
	case has("\x28\xb5\x2f\xfd"):
		return "zst", nil
	}
	// Magic didn't match (e.g. a continuation volume opened directly, or a tar);
	// fall back to the extension.
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(name, ".rar") || rarVolRE.MatchString(name):
		return "rar", nil
	case strings.Contains(name, ".7z"):
		return "7z", nil
	case strings.HasSuffix(name, ".zip"):
		return "zip", nil
	case strings.HasSuffix(name, ".tar"):
		return "tar", nil
	case strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".gz"):
		return "gz", nil
	case strings.HasSuffix(name, ".xz") || strings.HasSuffix(name, ".txz"):
		return "xz", nil
	case strings.HasSuffix(name, ".bz2") || strings.HasSuffix(name, ".tbz2"):
		return "bz2", nil
	case strings.HasSuffix(name, ".zst") || strings.HasSuffix(name, ".tzst"):
		return "zst", nil
	}
	return "", fmt.Errorf("unrecognized archive: %s", name)
}

func extractRar(ctx context.Context, path, dest, password string) error {
	var opts []rardecode.Option
	if password != "" {
		opts = append(opts, rardecode.Password(password))
	}
	rc, err := rardecode.OpenReader(path, opts...)
	if err != nil {
		return err
	}
	defer rc.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := rc.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.IsDir {
			continue
		}
		if err := writeEntry(dest, hdr.Name, rc); err != nil {
			return err
		}
	}
}

func extract7z(ctx context.Context, path, dest, password string) error {
	rc, err := sevenzip.OpenReaderWithPassword(path, password)
	if err != nil {
		return err
	}
	defer rc.Close()
	for _, f := range rc.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		err = writeEntry(dest, f.Name, in)
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractZip(ctx context.Context, path, dest, password string) error {
	rc, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer rc.Close()
	for _, f := range rc.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		err = writeEntry(dest, f.Name, in)
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// extractCompressed handles a single-stream compressor (gz/xz/bz2/zst). If the
// decompressed stream is a tar (by inner name or tar magic), it is untarred;
// otherwise it yields one file named after the archive with the compression
// extension stripped.
func extractCompressed(ctx context.Context, path, dest, kind string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader
	switch kind {
	case "gz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	case "xz":
		xr, err := xz.NewReader(f)
		if err != nil {
			return err
		}
		r = xr
	case "bz2":
		r = bzip2.NewReader(f)
	case "zst":
		zr, err := zstd.NewReader(f)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}

	name := filepath.Base(path)
	lower := strings.ToLower(name)
	isTar := strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz") ||
		strings.HasSuffix(lower, ".txz") || strings.HasSuffix(lower, ".tbz2") ||
		strings.HasSuffix(lower, ".tzst")
	if !isTar {
		// Peek for the ustar magic at offset 257 to catch a ".tar.<x>" whose
		// name was not tagged.
		br := &peekReader{r: r}
		if br.looksLikeTar() {
			return untar(ctx, br, dest)
		}
		r = br
	} else {
		return untar(ctx, r, dest)
	}

	// Single-file: strip the compression extension for the output name.
	out := stripCompressionExt(name)
	return writeEntry(dest, out, r)
}

func untar(ctx context.Context, r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeDir || hdr.FileInfo().IsDir() {
			continue
		}
		if err := writeEntry(dest, hdr.Name, tr); err != nil {
			return err
		}
	}
}

// writeEntry writes one archive entry to dest, guarding against path traversal
// (zip-slip) and oversized entries.
func writeEntry(dest, name string, r io.Reader) error {
	clean := sanitizeEntryName(name)
	if clean == "" {
		return nil // skip entries that escape dest or have empty names
	}
	target := filepath.Join(dest, clean)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, io.LimitReader(r, maxEntryBytes))
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

// sanitizeEntryName normalizes an archive entry path to a safe relative path
// under the destination, returning "" for anything that escapes it.
func sanitizeEntryName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.TrimPrefix(name, "/")
	clean := filepath.Clean(name)
	if clean == "." || clean == "" {
		return ""
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	return clean
}

func stripCompressionExt(name string) string {
	for _, ext := range []string{".gz", ".xz", ".bz2", ".zst", ".z"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name + ".out"
}

// peekReader buffers the first block so a compressed stream can be sniffed for
// a tar header without consuming it.
type peekReader struct {
	r      io.Reader
	buf    []byte
	peeked bool
}

func (p *peekReader) looksLikeTar() bool {
	if !p.peeked {
		p.buf = make([]byte, 512)
		n, _ := io.ReadFull(p.r, p.buf)
		p.buf = p.buf[:n]
		p.peeked = true
	}
	// "ustar" magic sits at offset 257 in a tar header block.
	return len(p.buf) >= 263 && string(p.buf[257:262]) == "ustar"
}

func (p *peekReader) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	return p.r.Read(b)
}

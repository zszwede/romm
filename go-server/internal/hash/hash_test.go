package hash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Known vectors for "hello".
const (
	wantCRC  = "3610a686"
	wantMD5  = "5d41402abc4b2a76b9719d911017c592"
	wantSHA1 = "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
)

func TestReader_KnownVectors(t *testing.T) {
	d, err := Reader(strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if d.CRC32 != wantCRC {
		t.Errorf("crc32 = %s, want %s", d.CRC32, wantCRC)
	}
	if d.MD5 != wantMD5 {
		t.Errorf("md5 = %s, want %s", d.MD5, wantMD5)
	}
	if d.SHA1 != wantSHA1 {
		t.Errorf("sha1 = %s, want %s", d.SHA1, wantSHA1)
	}
}

func TestFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rom.bin")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := File(p)
	if err != nil {
		t.Fatal(err)
	}
	if d.CRC32 != wantCRC || d.MD5 != wantMD5 || d.SHA1 != wantSHA1 {
		t.Errorf("file digests = %+v", d)
	}
}

func TestFile_Missing(t *testing.T) {
	if _, err := File(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error for missing file")
	}
}

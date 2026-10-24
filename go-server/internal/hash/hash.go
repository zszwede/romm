// Package hash computes the ROM checksums RomM stores (crc_hash, md5_hash,
// sha1_hash). Doing it natively in Go removes the need for the RAHasher/sigil
// native binaries for the common case; it streams the file once through all
// three hashes. Specialised per-system hashing (e.g. headered iNES, N64 byte
// order, RetroAchievements content hashing) is layered on in a later step.
package hash

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// Digests holds the hex-encoded checksums, matching the DB column widths
// (crc 8 hex chars, md5 32, sha1 40).
type Digests struct {
	CRC32 string `json:"crc_hash"`
	MD5   string `json:"md5_hash"`
	SHA1  string `json:"sha1_hash"`
}

// Reader streams r through all three hashes in a single pass.
func Reader(r io.Reader) (Digests, error) {
	c := crc32.NewIEEE()
	m := md5.New()
	s := sha1.New()
	if _, err := io.Copy(io.MultiWriter(c, m, s), r); err != nil {
		return Digests{}, err
	}
	return Digests{
		CRC32: fmt.Sprintf("%08x", c.Sum32()),
		MD5:   hex.EncodeToString(m.Sum(nil)),
		SHA1:  hex.EncodeToString(s.Sum(nil)),
	}, nil
}

// File hashes a file by path.
func File(path string) (Digests, error) {
	f, err := os.Open(path)
	if err != nil {
		return Digests{}, err
	}
	defer f.Close()
	return Reader(f)
}

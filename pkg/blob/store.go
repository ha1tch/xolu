// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package blob implements a content-addressed filesystem blob store.
//
// Layout on disk:
//
//	{root}/{tenant}/{xx}/{sha256hex}
//
// where {xx} is the first two hex characters of the SHA-256 digest (git-style
// prefix sharding). This keeps directory entry counts manageable at scale.
//
// Blobs are written atomically: content is first written to a temp file in the
// same directory, then renamed into place. A partial write therefore never
// produces a visible corrupt blob.
//
// Deduplication is structural: two clients storing identical content produce
// the same SHA-256 key, and the second write is a no-op after the existence
// check.
//
// The key-alias index maps caller-supplied names to SHA-256 digests. Each
// alias is a small text file under {root}/{tenant}/.keys/{key} containing the
// hex SHA. This keeps the key lookup O(1) filesystem calls with no separate
// index database.
package blob

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound is returned when a blob or key does not exist.
var ErrNotFound = errors.New("blob: not found")

// ErrKeyInvalid is returned when a caller-supplied key contains disallowed
// characters or is otherwise malformed.
var ErrKeyInvalid = errors.New("blob: invalid key")

// ErrTooLarge is returned when the content exceeds the configured size limit.
var ErrTooLarge = errors.New("blob: content too large")

// ErrSHAInvalid is returned when a SHA-addressed operation receives a digest
// that is not exactly 64 lowercase hexadecimal characters. Validating the
// digest before it reaches the filesystem path prevents a panic on a short
// digest (hexSHA[:2]) and stops non-hex characters from contributing path
// components to the on-disk layout (D-004).
var ErrSHAInvalid = errors.New("blob: invalid sha256 digest")

// Meta holds metadata about a stored blob.
type Meta struct {
	Key         string    // caller-assigned name
	SHA256      string    // hex-encoded SHA-256 of the content
	MD5         string    // hex-encoded MD5 of the content (S3 ETag); "" if unknown
	Size        int64     // bytes
	ContentType string    // preserved from the original store call; may be empty
	StoredAt    time.Time // when this key was last written
}

// Store is a content-addressed blob store for a single xolu instance.
// It is safe for concurrent use.
type Store struct {
	root    string // absolute path to the root directory
	maxSize int64  // maximum blob size in bytes; 0 = no limit
}

// NewStore creates a Store rooted at dir. dir is created if it does not exist.
// maxSize is the upper bound on accepted blob sizes in bytes; 0 means no limit.
func NewStore(dir string, maxSize int64) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("blob: mkdir %s: %w", dir, err)
	}
	return &Store{root: dir, maxSize: maxSize}, nil
}

// ---------------------------------------------------------------------------
// Core operations
// ---------------------------------------------------------------------------

// Put stores content under key for tenant. Returns the SHA-256 hex digest and
// whether the blob was newly created (false = key existed and was overwritten
// with possibly different content). contentType is stored alongside the blob
// for retrieval; it may be empty.
//
// The content is read once, SHA-256 is computed in a single streaming pass,
// and the result is written atomically. If an identical blob already exists
// under the computed SHA, no file write is performed.
func (s *Store) Put(key string, r io.Reader, contentType string) (sha string, md5hex string, created bool, err error) {
	if err := validateKey(key); err != nil {
		return "", "", false, err
	}

	// Stream content into a temp file while computing the SHA-256.
	tmpDir := s.root
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", "", false, fmt.Errorf("blob: mkdir tenant dir: %w", err)
	}

	tmp, err := os.CreateTemp(tmpDir, ".blob-tmp-*")
	if err != nil {
		return "", "", false, fmt.Errorf("blob: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// Clean up temp on any error path.
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	h := sha256.New()
	hm := md5.New()
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			written += int64(n)
			if s.maxSize > 0 && written > s.maxSize {
				return "", "", false, ErrTooLarge
			}
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				return "", "", false, fmt.Errorf("blob: write temp: %w", werr)
			}
			h.Write(buf[:n])
			hm.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", false, fmt.Errorf("blob: read content: %w", readErr)
		}
	}
	if err = tmp.Close(); err != nil {
		return "", "", false, fmt.Errorf("blob: close temp: %w", err)
	}

	hexSHA := hex.EncodeToString(h.Sum(nil))
	hexMD5 := hex.EncodeToString(hm.Sum(nil))

	// Write blob file (idempotent — if same SHA exists, skip).
	blobPath := s.blobPath(hexSHA)
	if err = os.MkdirAll(filepath.Dir(blobPath), 0755); err != nil {
		return "", "", false, fmt.Errorf("blob: mkdir prefix dir: %w", err)
	}
	blobCreated := false
	if _, statErr := os.Stat(blobPath); os.IsNotExist(statErr) {
		if err = os.Rename(tmpPath, blobPath); err != nil {
			return "", "", false, fmt.Errorf("blob: rename to blob path: %w", err)
		}
		blobCreated = true
	} else {
		_ = os.Remove(tmpPath)
	}

	// Write content-type sidecar (always, even for existing blobs, in case
	// content-type differs from a previous store under a different key).
	ctPath := blobPath + ".ct"
	if contentType != "" {
		_ = os.WriteFile(ctPath, []byte(contentType), 0644)
	}

	// Write MD5 sidecar (the S3 ETag for simple uploads). MD5 is a property of
	// the content, so it is consistent for a given SHA-addressed blob.
	_ = os.WriteFile(blobPath+".md5", []byte(hexMD5), 0644)

	// Write or overwrite the key alias.
	keyPath := s.keyPath(key)
	if err = os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return "", "", false, fmt.Errorf("blob: mkdir keys dir: %w", err)
	}
	keyCreated := false
	if _, statErr := os.Stat(keyPath); os.IsNotExist(statErr) {
		keyCreated = true
	}

	if err = atomicWrite(keyPath, []byte(hexSHA)); err != nil {
		return "", "", false, fmt.Errorf("blob: write key alias: %w", err)
	}

	created = blobCreated || keyCreated
	return hexSHA, hexMD5, created, nil
}

// Get retrieves the content and metadata for key in tenant.
// The caller is responsible for closing the returned ReadCloser.
// Returns ErrNotFound when the key does not exist.
func (s *Store) Get(key string) (io.ReadCloser, Meta, error) {
	if err := validateKey(key); err != nil {
		return nil, Meta{}, err
	}

	hexSHA, err := s.resolveKey(key)
	if err != nil {
		return nil, Meta{}, err
	}

	return s.getBySHA(key, hexSHA)
}

// PutBySHA stores the blob content under an alias of key, where the blob file
// is already on disk under hexSHA. This is used when the SHA is computed
// during a prior Put and the alias needs to be re-targeted. If the blob file
// does not exist, ErrNotFound is returned.
func (s *Store) PutBySHA(key, hexSHA, contentType string) (string, bool, error) {
	if err := validateKey(key); err != nil {
		return "", false, err
	}
	// D-004: reject a malformed digest before it reaches blobPath.
	if err := validateSHA256Hex(hexSHA); err != nil {
		return "", false, err
	}
	blobPath := s.blobPath(hexSHA)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		return "", false, ErrNotFound
	}
	keyPath := s.keyPath(key)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return "", false, fmt.Errorf("blob: mkdir keys dir: %w", err)
	}
	created := false
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		created = true
	}
	if err := atomicWrite(keyPath, []byte(hexSHA)); err != nil {
		return "", false, fmt.Errorf("blob: write key alias: %w", err)
	}
	if contentType != "" {
		_ = os.WriteFile(blobPath+".ct", []byte(contentType), 0644)
	}
	return hexSHA, created, nil
}

// PutRaw stores content and returns the SHA-256 hex digest. No key alias is
// written. This is the correct path when the SHA itself is the identifier
// (e.g. the history versioning system, or any purely content-addressed use).
// Retrieval goes through GetBySHA; the result will not appear in List.
//
// If an identical blob already exists on disk the write is skipped and the
// existing SHA is returned — the deduplication guarantee still holds.
func (s *Store) PutRaw(r io.Reader, contentType string) (sha string, md5hex string, created bool, err error) {
	tmpDir := s.root
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", "", false, fmt.Errorf("blob: mkdir tenant dir: %w", err)
	}

	tmp, err := os.CreateTemp(tmpDir, ".blob-tmp-*")
	if err != nil {
		return "", "", false, fmt.Errorf("blob: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	h := sha256.New()
	hm := md5.New()
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			written += int64(n)
			if s.maxSize > 0 && written > s.maxSize {
				return "", "", false, ErrTooLarge
			}
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				return "", "", false, fmt.Errorf("blob: write temp: %w", werr)
			}
			h.Write(buf[:n])
			hm.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", false, fmt.Errorf("blob: read content: %w", readErr)
		}
	}
	if err = tmp.Close(); err != nil {
		return "", "", false, fmt.Errorf("blob: close temp: %w", err)
	}

	hexSHA := hex.EncodeToString(h.Sum(nil))
	hexMD5 := hex.EncodeToString(hm.Sum(nil))

	blobPath := s.blobPath(hexSHA)
	if err = os.MkdirAll(filepath.Dir(blobPath), 0755); err != nil {
		return "", "", false, fmt.Errorf("blob: mkdir prefix dir: %w", err)
	}
	if _, statErr := os.Stat(blobPath); os.IsNotExist(statErr) {
		if err = os.Rename(tmpPath, blobPath); err != nil {
			return "", "", false, fmt.Errorf("blob: rename to blob path: %w", err)
		}
		created = true
	} else {
		_ = os.Remove(tmpPath)
	}
	if contentType != "" {
		_ = os.WriteFile(blobPath+".ct", []byte(contentType), 0644)
	}
	_ = os.WriteFile(blobPath+".md5", []byte(hexMD5), 0644)
	return hexSHA, hexMD5, created, nil
}

// Root returns the root directory of the store. Used by the GC worker.
func (s *Store) Root() string { return s.root }

// ValidateKey is the exported form of the key validation check,
// for use by callers that want to pre-validate keys before calling Put.
func ValidateKey(key string) error {
	return validateKey(key)
}

// GetBySHA retrieves content directly by its SHA-256 hex digest, bypassing
// the key alias index. Useful when the caller already holds the SHA from a
// prior Put response. key is used only to populate Meta.Key; pass the SHA
// itself if no logical key is known.
func (s *Store) GetBySHA(key, hexSHA string) (io.ReadCloser, Meta, error) {
	return s.getBySHA(key, hexSHA)
}

func (s *Store) getBySHA(key, hexSHA string) (io.ReadCloser, Meta, error) {
	// D-004: reject a malformed digest before it reaches blobPath (hexSHA[:2]
	// would panic on a short digest, and non-hex characters could contribute
	// path components).
	if err := validateSHA256Hex(hexSHA); err != nil {
		return nil, Meta{}, err
	}
	blobPath := s.blobPath(hexSHA)
	f, err := os.Open(blobPath)
	if os.IsNotExist(err) {
		return nil, Meta{}, ErrNotFound
	}
	if err != nil {
		return nil, Meta{}, fmt.Errorf("blob: open: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Meta{}, fmt.Errorf("blob: stat: %w", err)
	}

	ct := ""
	if ctBytes, ctErr := os.ReadFile(blobPath + ".ct"); ctErr == nil {
		ct = strings.TrimSpace(string(ctBytes))
	}

	meta := Meta{
		Key:         key,
		SHA256:      hexSHA,
		MD5:         readMD5Sidecar(blobPath),
		Size:        info.Size(),
		ContentType: ct,
		StoredAt:    info.ModTime().UTC(),
	}
	return f, meta, nil
}

// readMD5Sidecar returns the hex MD5 stored alongside a blob, or "" if the
// sidecar is absent (e.g. a blob written before MD5 sidecars existed).
func readMD5Sidecar(blobPath string) string {
	if b, err := os.ReadFile(blobPath + ".md5"); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

// Head returns metadata for key without reading the blob content.
// Returns ErrNotFound when the key does not exist.
func (s *Store) Head(key string) (Meta, error) {
	if err := validateKey(key); err != nil {
		return Meta{}, err
	}

	hexSHA, err := s.resolveKey(key)
	if err != nil {
		return Meta{}, err
	}

	blobPath := s.blobPath(hexSHA)
	info, err := os.Stat(blobPath)
	if os.IsNotExist(err) {
		return Meta{}, ErrNotFound
	}
	if err != nil {
		return Meta{}, fmt.Errorf("blob: stat: %w", err)
	}

	ct := ""
	if ctBytes, ctErr := os.ReadFile(blobPath + ".ct"); ctErr == nil {
		ct = strings.TrimSpace(string(ctBytes))
	}

	return Meta{
		Key:         key,
		SHA256:      hexSHA,
		MD5:         readMD5Sidecar(blobPath),
		Size:        info.Size(),
		ContentType: ct,
		StoredAt:    info.ModTime().UTC(),
	}, nil
}

// Delete removes the key alias for key in tenant. The underlying blob file is
// not removed immediately — blob GC is a separate operation that scans for
// unreferenced SHAs. Returns ErrNotFound when the key does not exist.
func (s *Store) Delete(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}

	keyPath := s.keyPath(key)
	if err := os.Remove(keyPath); os.IsNotExist(err) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("blob: delete key: %w", err)
	}
	return nil
}

// List returns metadata for all keys in tenant whose names start with prefix.
// An empty prefix returns all keys. Results are in filesystem order (not
// guaranteed to be sorted); callers should sort if order matters.
func (s *Store) List(prefix string) ([]Meta, error) {
	keysDir := filepath.Join(s.root, ".keys")
	entries, err := os.ReadDir(keysDir)
	if os.IsNotExist(err) {
		return []Meta{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("blob: list keys: %w", err)
	}

	var results []Meta
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		keyPath := filepath.Join(keysDir, name)
		shaBytes, err := os.ReadFile(keyPath)
		if err != nil {
			continue // race: key deleted between ReadDir and ReadFile
		}
		hexSHA := strings.TrimSpace(string(shaBytes))

		blobPath := s.blobPath(hexSHA)
		info, err := os.Stat(blobPath)
		if err != nil {
			continue // blob may have been GC'd
		}

		ct := ""
		if ctBytes, ctErr := os.ReadFile(blobPath + ".ct"); ctErr == nil {
			ct = strings.TrimSpace(string(ctBytes))
		}

		results = append(results, Meta{
			Key:         name,
			SHA256:      hexSHA,
			MD5:         readMD5Sidecar(blobPath),
			Size:        info.Size(),
			ContentType: ct,
			StoredAt:    info.ModTime().UTC(),
		})
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Usage accounting
// ---------------------------------------------------------------------------

// Usage holds disk usage statistics for a single tenant's blob namespace.
type Usage struct {
	// BlobCount is the number of distinct blob files on disk (deduplicated).
	// Two keys pointing to the same content count as one blob.
	BlobCount int64
	// KeyCount is the number of key aliases.
	KeyCount int64
	// Bytes is the total size of all blob files in bytes, excluding sidecars.
	Bytes int64
}

// Usage returns disk usage for this (single-tenant) store. It walks the shard
// directories to count and size blob files, and the .keys directory to count
// key aliases. Orphaned blobs (no alias, awaiting GC) are included in
// BlobCount and Bytes but not in KeyCount.
func (s *Store) Usage() (Usage, error) {
	return s.usageFromDir(s.root)
}

// usageFromDir performs the usage walk over an absolute store root directory.
func (s *Store) usageFromDir(rootDir string) (Usage, error) {
	tenantDir := rootDir
	var u Usage

	// Count key aliases.
	keysDir := filepath.Join(tenantDir, ".keys")
	keyEntries, err := os.ReadDir(keysDir)
	if err != nil && !os.IsNotExist(err) {
		return Usage{}, fmt.Errorf("blob: usage keys: %w", err)
	}
	for _, e := range keyEntries {
		if !e.IsDir() {
			u.KeyCount++
		}
	}

	// Walk shard directories for blob count and size.
	shardEntries, err := os.ReadDir(tenantDir)
	if err != nil && !os.IsNotExist(err) {
		return Usage{}, fmt.Errorf("blob: usage shards: %w", err)
	}
	for _, se := range shardEntries {
		if !se.IsDir() || !isHexPrefix(se.Name()) {
			continue
		}
		shardDir := filepath.Join(tenantDir, se.Name())
		blobs, err := os.ReadDir(shardDir)
		if err != nil {
			continue
		}
		for _, be := range blobs {
			if be.IsDir() || strings.HasSuffix(be.Name(), ".ct") || strings.HasSuffix(be.Name(), ".md5") || strings.HasPrefix(be.Name(), ".") {
				continue
			}
			info, err := be.Info()
			if err != nil {
				continue
			}
			u.BlobCount++
			u.Bytes += info.Size()
		}
	}
	return u, nil
}

// isHexPrefix reports whether s is a two-character lowercase hex string,
// i.e. a valid shard directory name.
func isHexPrefix(s string) bool {
	if len(s) != 2 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// validateSHA256Hex reports whether hexSHA is a well-formed SHA-256 digest:
// exactly 64 lowercase hexadecimal characters. It returns ErrSHAInvalid
// otherwise. This is the boundary guard for every SHA-addressed operation.
func validateSHA256Hex(hexSHA string) error {
	if len(hexSHA) != sha256.Size*2 { // 32 bytes -> 64 hex chars
		return ErrSHAInvalid
	}
	for i := 0; i < len(hexSHA); i++ {
		c := hexSHA[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return ErrSHAInvalid
	}
	return nil
}

func (s *Store) blobPath(hexSHA string) string {
	return filepath.Join(s.root, hexSHA[:2], hexSHA)
}

func (s *Store) keyPath(key string) string {
	return filepath.Join(s.root, ".keys", key)
}

func (s *Store) resolveKey(key string) (string, error) {
	keyPath := s.keyPath(key)
	data, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("blob: read key alias: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// atomicWrite writes data to path via a temp file in the same directory,
// then renames into place. Safe against partial writes.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// validateKey checks that a caller-supplied key is safe to use as a filename.
func validateKey(key string) error {
	if key == "" {
		return ErrKeyInvalid
	}
	if strings.ContainsAny(key, "/\\") {
		return ErrKeyInvalid
	}
	if key == "." || key == ".." {
		return ErrKeyInvalid
	}
	if strings.HasPrefix(key, ".") {
		return ErrKeyInvalid // reserve dot-prefixed names for internal use
	}
	return nil
}

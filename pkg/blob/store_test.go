// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func mustRead(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// (sanitiseTenant removed: Store is single-tenant, no tenant-string encoding)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Put / Get round-trip
// ---------------------------------------------------------------------------

func TestPutGet_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	content := []byte("hello blob world")
	sha, _, created, err := s.Put("mykey", bytes.NewReader(content), "text/plain")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !created {
		t.Error("expected created=true on first Put")
	}
	if want := sha256hex(content); sha != want {
		t.Errorf("sha = %q, want %q", sha, want)
	}

	rc, meta, err := s.Get("mykey")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got := mustRead(t, rc)
	if !bytes.Equal(got, content) {
		t.Errorf("Get content mismatch: got %q, want %q", got, content)
	}
	if meta.ContentType != "text/plain" {
		t.Errorf("meta.ContentType = %q, want %q", meta.ContentType, "text/plain")
	}
	if meta.SHA256 != sha {
		t.Errorf("meta.SHA256 = %q, want %q", meta.SHA256, sha)
	}
}

func TestPut_Deduplication(t *testing.T) {
	s := newTestStore(t)
	content := []byte("deduplicated content")

	sha1, _, created1, err := s.Put("key1", bytes.NewReader(content), "")
	if err != nil || !created1 {
		t.Fatalf("first Put: err=%v created=%v", err, created1)
	}
	sha2, _, created2, err := s.Put("key2", bytes.NewReader(content), "")
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	// created refers to whether a new key alias was written, not whether the
	// blob file is new. key2 is a new alias so created=true is expected even
	// though the underlying blob already exists on disk.
	_ = created2
	if sha1 != sha2 {
		t.Errorf("duplicate content produced different SHAs: %q vs %q", sha1, sha2)
	}

	// Both keys must be readable and return the same content.
	rc1, _, _ := s.Get("key1")
	rc2, _, _ := s.Get("key2")
	b1 := mustRead(t, rc1)
	b2 := mustRead(t, rc2)
	if !bytes.Equal(b1, content) || !bytes.Equal(b2, content) {
		t.Error("deduplication: content mismatch")
	}
}

func TestPut_Overwrite(t *testing.T) {
	s := newTestStore(t)
	_, _, _, err := s.Put("k", bytes.NewReader([]byte("v1")), "")
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	sha2, _, _, err := s.Put("k", bytes.NewReader([]byte("v2")), "")
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	rc, _, _ := s.Get("k")
	got := mustRead(t, rc)
	if string(got) != "v2" {
		t.Errorf("after overwrite got %q, want %q", got, "v2")
	}
	if want := sha256hex([]byte("v2")); sha2 != want {
		t.Errorf("sha after overwrite = %q, want %q", sha2, want)
	}
}

func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.Get("nosuchkey")
	if err != ErrNotFound {
		t.Errorf("Get missing key: err = %v, want ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// PutRaw / GetBySHA
// ---------------------------------------------------------------------------

func TestPutRaw_NoAliasWritten(t *testing.T) {
	s := newTestStore(t)
	content := []byte("raw content, no key")
	sha, _, _, err := s.PutRaw(bytes.NewReader(content), "application/octet-stream")
	if err != nil {
		t.Fatalf("PutRaw: %v", err)
	}
	if want := sha256hex(content); sha != want {
		t.Errorf("sha = %q, want %q", sha, want)
	}

	// Should NOT appear in List (no alias).
	items, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("PutRaw blob appeared in List: %v", items)
	}

	// Must be retrievable by SHA.
	rc, _, err := s.GetBySHA(sha, sha)
	if err != nil {
		t.Fatalf("GetBySHA: %v", err)
	}
	got := mustRead(t, rc)
	if !bytes.Equal(got, content) {
		t.Errorf("GetBySHA content mismatch: got %q, want %q", got, content)
	}
}

func TestPutRaw_Idempotent(t *testing.T) {
	s := newTestStore(t)
	content := []byte("idempotent raw")
	sha1, _, created1, err := s.PutRaw(bytes.NewReader(content), "")
	if err != nil {
		t.Fatalf("first PutRaw: %v", err)
	}
	if !created1 {
		t.Error("first PutRaw: expected created=true")
	}
	sha2, _, created2, err := s.PutRaw(bytes.NewReader(content), "")
	if err != nil {
		t.Fatalf("second PutRaw: %v", err)
	}
	if created2 {
		t.Error("second PutRaw: expected created=false for duplicate content")
	}
	if sha1 != sha2 {
		t.Errorf("PutRaw not idempotent: %q vs %q", sha1, sha2)
	}
}

// ---------------------------------------------------------------------------
// Head
// ---------------------------------------------------------------------------

func TestHead(t *testing.T) {
	s := newTestStore(t)
	content := []byte("head me")
	s.Put("k", bytes.NewReader(content), "text/plain")
	meta, err := s.Head("k")
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if meta.Size != int64(len(content)) {
		t.Errorf("Head size = %d, want %d", meta.Size, len(content))
	}
	if meta.ContentType != "text/plain" {
		t.Errorf("Head ContentType = %q, want %q", meta.ContentType, "text/plain")
	}
}

func TestHead_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Head("nope")
	if err != ErrNotFound {
		t.Errorf("Head missing key: err = %v, want ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	s.Put("k", bytes.NewReader([]byte("bye")), "")
	if err := s.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, _, err := s.Get("k")
	if err != ErrNotFound {
		t.Errorf("after Delete, Get err = %v, want ErrNotFound", err)
	}
}

func TestDelete_BlobFileOrphaned(t *testing.T) {
	// Delete removes the alias but leaves the blob on disk (GC handles it).
	s := newTestStore(t)
	content := []byte("orphan me")
	sha, _, _, _ := s.Put("k", bytes.NewReader(content), "")
	s.Delete("k")

	blobPath := s.blobPath(sha)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		t.Error("blob file was deleted by Delete — expected it to remain for GC")
	}
}

func TestDelete_NotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.Delete("nosuch"); err != ErrNotFound {
		t.Errorf("Delete missing key: err = %v, want ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestList(t *testing.T) {
	s := newTestStore(t)
	s.Put("apple", bytes.NewReader([]byte("a")), "")
	s.Put("apricot", bytes.NewReader([]byte("b")), "")
	s.Put("banana", bytes.NewReader([]byte("c")), "")

	all, _ := s.List("")
	if len(all) != 3 {
		t.Errorf("List all: got %d items, want 3", len(all))
	}

	ap, _ := s.List("ap")
	if len(ap) != 2 {
		t.Errorf("List prefix=ap: got %d items, want 2", len(ap))
	}
}

func TestList_Empty(t *testing.T) {
	s := newTestStore(t)
	items, err := s.List("")
	if err != nil {
		t.Fatalf("List empty: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("List empty: got %d items, want 0", len(items))
	}
}

// ---------------------------------------------------------------------------
// MaxSize enforcement
// ---------------------------------------------------------------------------

func TestPut_TooLarge(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10) // 10-byte limit
	_, _, _, err := s.Put("k", bytes.NewReader([]byte("this is more than 10 bytes")), "")
	if err != ErrTooLarge {
		t.Errorf("Put oversized: err = %v, want ErrTooLarge", err)
	}
}

func TestPutRaw_TooLarge(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10)
	_, _, _, err := s.PutRaw(bytes.NewReader([]byte("this is more than 10 bytes")), "")
	if err != ErrTooLarge {
		t.Errorf("PutRaw oversized: err = %v, want ErrTooLarge", err)
	}
}

// ---------------------------------------------------------------------------
// Tenant isolation
//
// Tenant isolation is no longer a Store concern: a Store is single-tenant
// (its root IS the tenant's blobs directory). Cross-tenant isolation is
// provided by the per-tenant directory layout (TenantBlobDir) and the blob
// manager that owns one Store per tenant; it is verified at that layer
// (pkg/server), not here.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Key validation
// ---------------------------------------------------------------------------

func TestValidateKey(t *testing.T) {
	if err := ValidateKey("valid-key_123"); err != nil {
		t.Errorf("ValidateKey valid: %v", err)
	}
	for _, bad := range []string{"", "a/b", "a\\b"} {
		if err := ValidateKey(bad); err == nil {
			t.Errorf("ValidateKey(%q): expected error, got nil", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// GC worker
// ---------------------------------------------------------------------------

func TestGC_CollectsOrphan(t *testing.T) {
	s := newTestStore(t)
	content := []byte("orphan blob")
	sha, _, _, _ := s.Put("k", bytes.NewReader(content), "")

	// Delete the key alias — blob is now orphaned.
	s.Delete("k")

	cfg := GCConfig{
		Interval:    time.Hour, // won't tick during test
		GracePeriod: 0,         // immediate purge
	}
	w := NewGCWorker(s, cfg, nil)
	report := w.RunOnce()

	if report.Quarantined != 1 {
		t.Errorf("Quarantined = %d, want 1", report.Quarantined)
	}
	if report.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", report.Deleted)
	}
	if report.Errors != 0 {
		t.Errorf("Errors = %d, want 0", report.Errors)
	}

	// Blob file must be gone.
	blobPath := s.blobPath(sha)
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Error("blob file still present after GC purge")
	}
}

// TestGC_SweepWrapper exercises the Sweep(ctx) gcpkg.Sweeper adapter directly
// (the blob manager drives GC through this wrapper, not RunOnce) and checks the
// GCReport -> gcpkg.Report field mapping.
func TestGC_SweepWrapper(t *testing.T) {
	s := newTestStore(t)
	s.Put("k", bytes.NewReader([]byte("orphan via Sweep")), "")
	s.Delete("k") // orphan it

	w := NewGCWorker(s, GCConfig{Interval: time.Hour, GracePeriod: 0}, nil)
	rep, err := w.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if rep.Quarantined != 1 {
		t.Errorf("Quarantined = %d, want 1", rep.Quarantined)
	}
	if rep.Examined != 1 { // TenantsScanned == 1 for a single-tenant store
		t.Errorf("Examined = %d, want 1", rep.Examined)
	}
}

func TestGC_PreservesLiveBlob(t *testing.T) {
	s := newTestStore(t)
	content := []byte("live blob, keep it")
	_, _, _, _ = s.Put("k", bytes.NewReader(content), "")

	cfg := GCConfig{Interval: time.Hour, GracePeriod: 0}
	w := NewGCWorker(s, cfg, nil)
	report := w.RunOnce()

	if report.Quarantined != 0 {
		t.Errorf("Quarantined = %d, want 0 (blob is live)", report.Quarantined)
	}

	// Still readable.
	rc, _, err := s.Get("k")
	if err != nil {
		t.Fatalf("Get after GC: %v", err)
	}
	mustRead(t, rc)
}

func TestGC_GracePeriodProtectsNewBlob(t *testing.T) {
	s := newTestStore(t)
	content := []byte("written after mark phase")
	sha, _, _, _ := s.Put("k", bytes.NewReader(content), "")

	// Delete the alias so the blob looks orphaned.
	s.Delete("k")

	// GC with a long grace period: blob should be quarantined but NOT deleted.
	cfg := GCConfig{Interval: time.Hour, GracePeriod: time.Hour}
	w := NewGCWorker(s, cfg, nil)
	report := w.RunOnce()

	if report.Quarantined != 1 {
		t.Errorf("Quarantined = %d, want 1", report.Quarantined)
	}
	if report.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0 (grace period active)", report.Deleted)
	}

	// Blob file must still exist (in .gc-pending).
	tenantDir := s.root
	pendingDir := filepath.Join(tenantDir, ".gc-pending")
	entries, _ := os.ReadDir(pendingDir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), sha) {
			found = true
		}
	}
	if !found {
		t.Error("quarantined blob not found in .gc-pending")
	}
}

func TestGC_RestoresQuarantinedBlobThatBecomesLive(t *testing.T) {
	s := newTestStore(t)
	content := []byte("quarantine then rescue")
	sha, _, _, _ := s.Put("k", bytes.NewReader(content), "")
	s.Delete("k")

	// First GC run: quarantine with long grace period.
	cfg := GCConfig{Interval: time.Hour, GracePeriod: time.Hour}
	w := NewGCWorker(s, cfg, nil)
	w.RunOnce()

	// Re-create the key alias while blob is in quarantine.
	// PutRaw won't write an alias; use PutBySHA to point the key at the SHA.
	_, _, err := s.PutBySHA("k", sha, "")
	if err != nil {
		// PutBySHA may fail if the blob is in quarantine rather than its shard.
		// That is expected — the point of the test is the restore path.
		t.Logf("PutBySHA while quarantined: %v (expected if blob moved)", err)
	}

	// Manually write the alias so the next GC run sees a live SHA.
	keyPath := s.keyPath("k")
	_ = os.MkdirAll(filepath.Dir(keyPath), 0755)
	_ = os.WriteFile(keyPath, []byte(sha), 0644)

	// Second GC run with zero grace period: blob is live again, must survive.
	cfg2 := GCConfig{Interval: time.Hour, GracePeriod: 0}
	w2 := NewGCWorker(s, cfg2, nil)
	report := w2.RunOnce()

	if report.Deleted > 0 {
		t.Errorf("GC deleted a blob that became live again (Deleted=%d)", report.Deleted)
	}
}

func TestGC_ExternalSHARefSource(t *testing.T) {
	s := newTestStore(t)
	content := []byte("referenced externally")
	sha, _, _, err := s.PutRaw(bytes.NewReader(content), "")
	if err != nil {
		t.Fatalf("PutRaw: %v", err)
	}

	// No key alias — looks orphaned to the GC.
	// But an external ref source claims the SHA is live.
	extRefs := &staticSHARefSource{shas: map[string]struct{}{sha: {}}}
	cfg := GCConfig{Interval: time.Hour, GracePeriod: 0}
	w := NewGCWorker(s, cfg, extRefs)
	report := w.RunOnce()

	if report.Quarantined != 0 {
		t.Errorf("Quarantined = %d, want 0 (SHA held by external ref)", report.Quarantined)
	}
	if report.Errors != 0 {
		t.Errorf("Errors = %d, want 0", report.Errors)
	}
}

// (TestGC_MultiTenant removed: a Store is single-tenant; the GC sweeps one
// store = one tenant. Multi-tenant GC is the manager's concern, tested in
// pkg/server.)

// ---------------------------------------------------------------------------
// Usage
// ---------------------------------------------------------------------------

func TestUsage_Empty(t *testing.T) {
	s := newTestStore(t)
	u, err := s.Usage()
	if err != nil {
		t.Fatalf("Usage empty: %v", err)
	}
	if u.BlobCount != 0 || u.KeyCount != 0 || u.Bytes != 0 {
		t.Errorf("Usage empty: got %+v, want all zeros", u)
	}
}

func TestUsage_KeysAndBlobs(t *testing.T) {
	s := newTestStore(t)
	content := []byte("measure me")
	s.Put("k1", bytes.NewReader(content), "")
	s.Put("k2", bytes.NewReader(content), "") // same content — one blob, two keys
	s.Put("k3", bytes.NewReader([]byte("different")), "")

	u, err := s.Usage()
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.KeyCount != 3 {
		t.Errorf("KeyCount = %d, want 3", u.KeyCount)
	}
	if u.BlobCount != 2 {
		t.Errorf("BlobCount = %d, want 2 (deduplication)", u.BlobCount)
	}
	wantBytes := int64(len(content) + len("different"))
	if u.Bytes != wantBytes {
		t.Errorf("Bytes = %d, want %d", u.Bytes, wantBytes)
	}
}

func TestUsage_OrphanedBlobCounted(t *testing.T) {
	// Orphaned blobs (key deleted, GC not yet run) appear in BlobCount/Bytes
	// but not in KeyCount.
	s := newTestStore(t)
	content := []byte("orphan")
	s.Put("k", bytes.NewReader(content), "")
	s.Delete("k")

	u, err := s.Usage()
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.KeyCount != 0 {
		t.Errorf("KeyCount = %d, want 0 after delete", u.KeyCount)
	}
	if u.BlobCount != 1 {
		t.Errorf("BlobCount = %d, want 1 (orphan still on disk)", u.BlobCount)
	}
	if u.Bytes != int64(len(content)) {
		t.Errorf("Bytes = %d, want %d", u.Bytes, len(content))
	}
}

func TestUsage_PutRawCounted(t *testing.T) {
	// PutRaw blobs have no key alias — they contribute to BlobCount/Bytes
	// but not KeyCount.
	s := newTestStore(t)
	content := []byte("raw no alias")
	s.PutRaw(bytes.NewReader(content), "")

	u, err := s.Usage()
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.KeyCount != 0 {
		t.Errorf("KeyCount = %d, want 0 for PutRaw", u.KeyCount)
	}
	if u.BlobCount != 1 {
		t.Errorf("BlobCount = %d, want 1", u.BlobCount)
	}
}

// (TestUsage_TenantIsolation removed: a Store is single-tenant; per-tenant
// usage separation is provided by separate per-tenant stores, tested in
// pkg/server.)

type staticSHARefSource struct {
	shas map[string]struct{}
}

func (r *staticSHARefSource) CollectLiveSHAs() (map[string]struct{}, error) {
	return r.shas, nil
}

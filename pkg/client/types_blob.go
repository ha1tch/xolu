// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_blob.go — wire types for the /api/v1/blob* surface
// (pkg/server/blob_handlers.go). Shapes mirror the server's own
// blobPutResponse/blobMetaResponse/blobDeleteResponse/blobListResponse/
// blobUsageResponse structs field-for-field. The S3-compatible surface
// (/{bucket}/{key} etc.) is deliberately not wrapped here — it exists
// for real S3 SDKs and tools, not xolu's own first-party client; see
// T-142 in docs/RESOLVED.md for the scope decision.

import "time"

// BlobPutResult is the response to a successful blob upload.
type BlobPutResult struct {
	// Key is the stored key. Equal to the key BlobPut was called with,
	// or (when BlobPut was called with an empty key) the content's own
	// SHA256 — content-addressed storage with no separate alias.
	Key string `json:"key"`
	// SHA256 is the content hash, always populated.
	SHA256 string `json:"sha256"`
	// MD5 is the content hash in MD5, present for S3-compatible ETag
	// consumers. Empty is possible but not expected in practice.
	MD5 string `json:"md5,omitempty"`
	// Size is the stored content length in bytes.
	//
	// Known gap, not a client bug: the server's own PUT response does
	// not currently populate this field (confirmed directly against
	// blob_handlers.go's handleBlobPut, which never sets Size on the
	// blobPutResponse it writes) -- it will decode as 0 even for a
	// non-empty blob. Use BlobHead after a Put if the size is needed;
	// BlobHead's own response is populated correctly.
	Size int64 `json:"size"`
	// Created is true when this call created a new stored object
	// (false when the content was already present -- content-addressed
	// storage deduplicates identical bytes).
	Created bool `json:"created"`
}

// BlobMeta describes one stored blob's metadata, without its content.
// Returned by BlobList (one per result) and mirrored by BlobHeadResult
// for a single-key lookup.
type BlobMeta struct {
	Key         string    `json:"key"`
	SHA256      string    `json:"sha256"`
	MD5         string    `json:"md5,omitempty"`
	Size        int64     `json:"size"`
	ContentType string    `json:"content_type,omitempty"`
	StoredAt    time.Time `json:"stored_at"`
}

// BlobHeadResult is the response to BlobHead -- metadata only, no
// content. Fields mirror BlobMeta; kept as a distinct type (not a type
// alias) because the server's own HEAD response carries ETag and a
// couple of header-only fields BlobMeta's GET/LIST shape doesn't.
type BlobHeadResult struct {
	Key         string
	ContentType string
	Size        int64
	SHA256      string
	MD5         string
	// ETag is the raw ETag header value, quotes included (e.g.
	// `"deadbeef..."`), matching HTTP convention -- always equal to
	// `"` + SHA256 + `"` today, carried separately in case that
	// changes.
	ETag     string
	StoredAt time.Time
}

// BlobDeleteResult is the response to a successful blob deletion.
//
// Deletion removes the key alias only. The underlying content-addressed
// blob is not immediately removed -- xolu's own GC handles unreferenced
// blobs separately, per blob_handlers.go's own comment on this route.
type BlobDeleteResult struct {
	Key     string `json:"key"`
	Deleted bool   `json:"deleted"`
}

// BlobListResult is the response to BlobList.
type BlobListResult struct {
	Tenant string     `json:"tenant"`
	Prefix string     `json:"prefix,omitempty"`
	Count  int        `json:"count"`
	Blobs  []BlobMeta `json:"blobs"`
}

// BlobUsageResult is the response to BlobUsage -- the most recently
// sampled disk usage for the tenant's blob namespace, served from an
// in-memory cache the server's own background sampler maintains (the
// filesystem is never walked at request time). SampledAt is nil until
// the sampler completes its first walk; all counts read zero until
// then, not an error.
type BlobUsageResult struct {
	Tenant    string     `json:"tenant"`
	BlobCount int64      `json:"blob_count"`
	KeyCount  int64      `json:"key_count"`
	Bytes     int64      `json:"bytes"`
	SampledAt *time.Time `json:"sampled_at,omitempty"`
}

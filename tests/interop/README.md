# `tests/interop/` — real-client S3 interop

Verifies that xolu's S3 SigV4 verification path (`pkg/s3sig`, enforced in
`pkg/server/blob_s3_handlers.go` under `TenantAuthMode: scoped`) interoperates
with real, independently-written S3 clients — not just with its own `Sign`.

The unit tests in `pkg/s3sig` prove the implementation is self-consistent and
matches one AWS known-answer vector. This harness proves the stronger property:
that `mc` (MinIO, Go), `boto3` (the AWS reference, Python), and `s3cmd` (Python)
can each authenticate against a running xolu S3 listener, and that forged
signatures are rejected with the correct S3 error codes.

## Layout

```
tests/interop/
  README.md
  run.sh              launcher: boots the harness, runs every detected client
  boto_check.py       boto3 round-trip + negative checks (invoked by run.sh)
  server/main.go      the harness: a scoped xolu S3 listener with a known grant
```

The Go test functions for SigV4 live in `pkg/s3sig` (Go requires it); this
directory holds the orchestration and the real-client checks that need external
binaries.

## Fixture

The harness serves a fixed scoped configuration:

| | |
|---|---|
| bucket / tenant | `acme` |
| access key | `AKIAINTEROP` |
| secret | `interop-secret-key` |
| listen address | `:19091` (override with `-addr`) |

## Running

```
./tests/interop/run.sh           # run all detected clients
./tests/interop/run.sh --list    # show which clients are installed
```

Each client is checked for a valid put/get round-trip, a wrong-secret
rejection, and an unknown-key rejection. The script exits non-zero if any check
fails. It is **not** part of the release pipeline — it needs external client
binaries — so run it locally or in CI that installs them.

Install at least one client:

```
mc:    curl -sLO https://dl.min.io/client/mc/release/linux-amd64/mc && chmod +x mc
boto3: pip install boto3
s3cmd: pip install s3cmd
```

## Notes

`s3cmd` is the strictest client and is useful for surfacing S3 protocol-
conformance gaps beyond SigV4 (for example, ETag semantics on PUT). Those gaps
are tracked separately from the SigV4 work.

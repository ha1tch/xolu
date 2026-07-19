# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
#
# boto3 interop check for xolu's S3 SigV4 path. Invoked by tests/interop/run.sh.
# Exits 0 only if: a valid credential round-trips (put/get), a wrong secret is
# rejected with SignatureDoesNotMatch, and an unknown key is rejected.
#
# Usage: boto_check.py ENDPOINT BUCKET ACCESS_KEY SECRET

import sys

import boto3
import botocore
from botocore.client import Config


def client(endpoint, ak, secret):
    return boto3.client(
        "s3",
        endpoint_url=endpoint,
        aws_access_key_id=ak,
        aws_secret_access_key=secret,
        config=Config(signature_version="s3v4", s3={"addressing_style": "path"}),
        region_name="us-east-1",
    )


def main():
    endpoint, bucket, ak, secret = sys.argv[1:5]
    failures = 0

    # 1. Valid credential: put then get round-trip.
    try:
        c = client(endpoint, ak, secret)
        c.put_object(Bucket=bucket, Key="boto.txt", Body=b"interop payload")
        body = c.get_object(Bucket=bucket, Key="boto.txt")["Body"].read()
        if body == b"interop payload":
            print("  [ok]   valid put/get round-trip")
        else:
            print("  [FAIL] round-trip body mismatch:", body)
            failures += 1
    except Exception as e:  # noqa: BLE001
        print("  [FAIL] valid credential errored:", e)
        failures += 1

    # 2. Wrong secret -> SignatureDoesNotMatch.
    try:
        client(endpoint, ak, "WRONG-SECRET").put_object(Bucket=bucket, Key="bad.txt", Body=b"x")
        print("  [FAIL] wrong secret was accepted")
        failures += 1
    except botocore.exceptions.ClientError as e:
        code = e.response["Error"]["Code"]
        if code == "SignatureDoesNotMatch":
            print("  [ok]   wrong-secret rejected (SignatureDoesNotMatch)")
        else:
            print("  [ok]   wrong-secret rejected (" + code + ")")

    # 3. Unknown access key -> rejected.
    try:
        client(endpoint, "AKIAUNKNOWN", "whatever").put_object(Bucket=bucket, Key="u.txt", Body=b"x")
        print("  [FAIL] unknown key was accepted")
        failures += 1
    except botocore.exceptions.ClientError as e:
        print("  [ok]   unknown-key rejected (" + e.response["Error"]["Code"] + ")")

    # 4. Modern additional checksum: PUT with SHA256, then GET/HEAD with
    #    ChecksumMode=ENABLED. botocore validates the downloaded body against the
    #    returned checksum, so this is a real integrity round-trip.
    import base64
    import hashlib
    try:
        c = client(endpoint, ak, secret)
        payload = b"checksum integrity payload"
        want = base64.b64encode(hashlib.sha256(payload).digest()).decode()
        put = c.put_object(Bucket=bucket, Key="cks.txt", Body=payload, ChecksumAlgorithm="SHA256")
        if put.get("ChecksumSHA256") == want:
            print("  [ok]   PUT returns x-amz-checksum-sha256")
        else:
            print("  [FAIL] PUT checksum:", put.get("ChecksumSHA256"))
            failures += 1
        got = c.get_object(Bucket=bucket, Key="cks.txt", ChecksumMode="ENABLED")
        body = got["Body"].read()
        if got.get("ChecksumSHA256") == want and body == payload:
            print("  [ok]   GET ChecksumMode=ENABLED validates body")
        else:
            print("  [FAIL] GET checksum:", got.get("ChecksumSHA256"))
            failures += 1
        head = c.head_object(Bucket=bucket, Key="cks.txt", ChecksumMode="ENABLED")
        if head.get("ChecksumSHA256") == want:
            print("  [ok]   HEAD ChecksumMode=ENABLED returns checksum")
        else:
            print("  [FAIL] HEAD checksum:", head.get("ChecksumSHA256"))
            failures += 1
    except Exception as e:  # noqa: BLE001
        print("  [FAIL] checksum round-trip errored:", e)
        failures += 1

    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()

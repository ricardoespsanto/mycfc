import base64
import binascii
import hashlib
import json
import os
import re
from datetime import datetime, timezone

LOCATOR = re.compile(r"^[0-9a-f]{64}$")
KEY_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$")
MAX_PAYLOAD_BYTES = 64 * 1024


def _fail(message):
    raise ValueError(message)


def _decode_request(event):
    if not isinstance(event, dict) or set(event) - {
        "kind", "locator_key_id", "locator_digest", "payload",
        "checksum_sha256", "retain_until",
    }:
        _fail("invalid request")
    kind = event.get("kind")
    if kind not in ("intent", "closure"):
        _fail("invalid kind")
    locator = event.get("locator_digest")
    key_id = event.get("locator_key_id")
    if not isinstance(locator, str) or not LOCATOR.fullmatch(locator):
        _fail("invalid locator")
    if not isinstance(key_id, str) or not KEY_ID.fullmatch(key_id):
        _fail("invalid locator key id")
    try:
        payload = base64.b64decode(event.get("payload", ""), validate=True)
        supplied_checksum = base64.b64decode(event.get("checksum_sha256", ""), validate=True)
    except (binascii.Error, TypeError, ValueError):
        _fail("invalid encoding")
    if not payload or len(payload) > MAX_PAYLOAD_BYTES or len(supplied_checksum) != 32:
        _fail("invalid payload")
    digest = hashlib.sha256(payload).digest()
    if digest != supplied_checksum:
        _fail("checksum mismatch")

    retain_until = None
    if kind == "closure":
        value = event.get("retain_until")
        if not isinstance(value, str) or not value.endswith("Z"):
            _fail("invalid retention")
        try:
            retain_until = datetime.fromisoformat(value[:-1] + "+00:00")
        except ValueError:
            _fail("invalid retention")
        remaining_days = (retain_until - datetime.now(timezone.utc)).total_seconds() / 86400
        if remaining_days < 729 or remaining_days > 731:
            _fail("invalid retention window")
    elif event.get("retain_until") not in (None, ""):
        _fail("intent cannot set retention")

    return {
        "kind": kind,
        "locator_key_id": key_id,
        "locator_digest": locator,
        "payload": payload,
        "checksum": base64.b64encode(digest).decode("ascii"),
        "retain_until": retain_until,
    }


def _verified(head, request, version):
    if (head.get("VersionId") != version or
            head.get("ChecksumSHA256") != request["checksum"] or
            head.get("ContentLength") != len(request["payload"]) or
            head.get("ServerSideEncryption") != "aws:kms" or
            head.get("SSEKMSKeyId") != os.environ["LEDGER_KMS_KEY_ARN"] or
            head.get("Metadata", {}).get("locator-key-id") != request["locator_key_id"]):
        return False
    if request["kind"] == "intent":
        return True
    retained = head.get("ObjectLockRetainUntilDate")
    return (head.get("ObjectLockMode") == "COMPLIANCE" and
            retained is not None and
            retained.astimezone(timezone.utc) == request["retain_until"])


def handler(event, _context, s3_client=None):
    request = _decode_request(event)
    if s3_client is None:
        import boto3
        s3_client = boto3.client("s3")
    client = s3_client
    bucket = os.environ["LEDGER_BUCKET"]
    prefix = os.environ.get("LEDGER_PREFIX", "tombstones/")
    kms_key = os.environ["LEDGER_KMS_KEY_ARN"]
    key = f'{prefix}{request["kind"]}/{request["locator_digest"]}.json'
    put = {
        "Bucket": bucket,
        "Key": key,
        "Body": request["payload"],
        "ContentLength": len(request["payload"]),
        "ContentType": "application/json",
        "ChecksumSHA256": request["checksum"],
        "IfNoneMatch": "*",
        "ServerSideEncryption": "aws:kms",
        "SSEKMSKeyId": kms_key,
        "Metadata": {"locator-key-id": request["locator_key_id"]},
    }
    if request["kind"] == "closure":
        put["ObjectLockMode"] = "COMPLIANCE"
        put["ObjectLockRetainUntilDate"] = request["retain_until"]

    version = None
    try:
        version = client.put_object(**put).get("VersionId")
    except Exception as exc:
        status = getattr(exc, "response", {}).get("ResponseMetadata", {}).get("HTTPStatusCode")
        if status not in (409, 412):
            raise RuntimeError("privacy ledger unavailable") from None
        try:
            existing = client.head_object(Bucket=bucket, Key=key, ChecksumMode="ENABLED")
        except Exception:
            raise RuntimeError("privacy ledger unavailable") from None
        version = existing.get("VersionId")
        if not version or not _verified(existing, request, version):
            _fail("existing object mismatch")
    if not version:
        _fail("missing object version")

    try:
        head = client.head_object(
            Bucket=bucket, Key=key, VersionId=version, ChecksumMode="ENABLED"
        )
    except Exception:
        raise RuntimeError("privacy ledger unavailable") from None
    if not _verified(head, request, version):
        _fail("object verification failed")

    # Never log or return the opaque locator or ciphertext.
    print(json.dumps({"event": "privacy_ledger_append_verified", "kind": request["kind"]}))
    verified_at = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    written_at = head.get("LastModified")
    if written_at is None:
        _fail("missing object timestamp")
    written_at = written_at.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    return {
        "locator_key_id": request["locator_key_id"],
        "object_version": version,
        "ciphertext_sha256": request["checksum"],
        "size_bytes": len(request["payload"]),
        "written_at": written_at,
        "verified_at": verified_at,
    }

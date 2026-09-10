import base64
import hashlib
import importlib.util
import os
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location("handler", Path(__file__).with_name("handler.py"))
handler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(handler)


class BrokerTests(unittest.TestCase):
    def setUp(self):
        self.payload = b'{"ciphertext":"opaque"}'
        self.digest = base64.b64encode(hashlib.sha256(self.payload).digest()).decode()
        self.event = {
            "kind": "intent",
            "locator_key_id": "locator-key-v1",
            "locator_digest": base64.b64encode(b"a" * 32).decode(),
            "payload": base64.b64encode(self.payload).decode(),
            "checksum": self.digest,
        }
        self.env = patch.dict(os.environ, {
            "LEDGER_BUCKET": "ledger",
            "LEDGER_PREFIX": "tombstones/",
            "LEDGER_KMS_KEY_ARN": "arn:aws:kms:eu-west-1:123456789012:key/example",
        })
        self.env.start()
        self.addCleanup(self.env.stop)

    def head(self, version="v1", closure=False):
        result = {
            "VersionId": version,
            "ChecksumSHA256": self.digest,
            "ContentLength": len(self.payload),
            "LastModified": datetime.now(timezone.utc),
            "ServerSideEncryption": "aws:kms",
            "SSEKMSKeyId": os.environ["LEDGER_KMS_KEY_ARN"],
            "Metadata": {"locator-key-id": self.event["locator_key_id"]},
        }
        if closure:
            result.update({
                "ObjectLockMode": "COMPLIANCE",
                "ObjectLockRetainUntilDate": datetime.fromisoformat(self.event["retain_until"].replace("Z", "+00:00")),
            })
        return result

    def test_conditionally_creates_and_verifies_exact_version(self):
        client = Mock()
        client.put_object.return_value = {"VersionId": "v1"}
        client.head_object.return_value = self.head()
        receipt = handler.handler(self.event, None, client)
        self.assertEqual(receipt["object_version"], "v1")
        client.put_object.assert_called_once()
        self.assertEqual(client.put_object.call_args.kwargs["IfNoneMatch"], "*")
        self.assertEqual(client.put_object.call_args.kwargs["SSEKMSKeyId"], os.environ["LEDGER_KMS_KEY_ARN"])
        self.assertEqual(client.put_object.call_args.kwargs["Metadata"], {"locator-key-id": "locator-key-v1"})

    def test_closure_has_exact_compliance_retention(self):
        self.event["kind"] = "closure"
        self.event["retain_until"] = (datetime.now(timezone.utc) + timedelta(days=730)).isoformat().replace("+00:00", "Z")
        client = Mock()
        client.put_object.return_value = {"VersionId": "v1"}
        client.head_object.return_value = self.head(closure=True)
        handler.handler(self.event, None, client)
        args = client.put_object.call_args.kwargs
        self.assertEqual(args["ObjectLockMode"], "COMPLIANCE")
        self.assertEqual(args["ObjectLockRetainUntilDate"], datetime.fromisoformat(self.event["retain_until"].replace("Z", "+00:00")))

    def test_retry_accepts_only_matching_existing_object(self):
        class PreconditionFailed(Exception):
            response = {"Error": {"Code": "PreconditionFailed"}, "ResponseMetadata": {"HTTPStatusCode": 412}}

        client = Mock()
        client.put_object.side_effect = PreconditionFailed()
        client.head_object.return_value = self.head()
        self.assertEqual(handler.handler(self.event, None, client)["object_version"], "v1")
        client.head_object.return_value = {**self.head(), "ChecksumSHA256": base64.b64encode(b"x" * 32).decode()}
        with self.assertRaises(ValueError):
            handler.handler(self.event, None, client)

        client.head_object.return_value = {**self.head(), "Metadata": {"locator-key-id": "rotated-key-v2"}}
        with self.assertRaises(ValueError):
            handler.handler(self.event, None, client)

    def test_rejects_bad_locator_checksum_and_retention(self):
        client = Mock()
        for change in (
            {"locator_digest": base64.b64encode(b"short").decode()},
            {"checksum": base64.b64encode(b"x" * 32).decode()},
            {"kind": "closure", "retain_until": (datetime.now(timezone.utc) + timedelta(days=800)).isoformat().replace("+00:00", "Z")},
        ):
            bad = {**self.event, **change}
            with self.assertRaises(ValueError):
                handler.handler(bad, None, client)
        client.assert_not_called()

    def test_provider_errors_are_opaque(self):
        client = Mock()
        client.put_object.side_effect = RuntimeError("secret key and provider payload")
        with self.assertRaisesRegex(RuntimeError, "^privacy ledger unavailable$") as caught:
            handler.handler(self.event, None, client)
        self.assertNotIn("secret", str(caught.exception))

    def test_rejects_wrong_storage_encryption(self):
        client = Mock()
        client.put_object.return_value = {"VersionId": "v1"}
        client.head_object.return_value = {**self.head(), "SSEKMSKeyId": "arn:wrong"}
        with self.assertRaisesRegex(ValueError, "object verification failed"):
            handler.handler(self.event, None, client)


if __name__ == "__main__":
    unittest.main()

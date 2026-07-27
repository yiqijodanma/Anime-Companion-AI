from __future__ import annotations

import importlib.util
import os
import pathlib
import tempfile
import unittest
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("backup_to_oss.py")
SPEC = importlib.util.spec_from_file_location("backup_to_oss", MODULE_PATH)
assert SPEC and SPEC.loader
backup = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(backup)


class BackupToOssTests(unittest.TestCase):
    def test_rejects_url_scheme_in_endpoint(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "hostname without a URL scheme"):
            backup.validate_configuration(
                "valid-bucket", "https://oss-ap-southeast-1.aliyuncs.com", "ap-southeast-1", "backups"
            )

    def test_upload_uses_v4_environment_and_encrypted_multipart_options(self) -> None:
        environment = {
            "OSS_ACCESS_KEY_ID": "test-access-key-id",
            "OSS_ACCESS_KEY_SECRET": "test-access-key-secret",
            "OSS_SESSION_TOKEN": "test-session-token",
            "OSS_BUCKET": "valid-bucket",
            "OSS_ENDPOINT": "oss-ap-southeast-1.aliyuncs.com",
            "OSS_REGION": "ap-southeast-1",
            "OSS_PREFIX": "anime-companion/postgres",
        }
        with tempfile.TemporaryDirectory() as workdir:
            dump = pathlib.Path(workdir) / "backup.dump"
            dump.write_bytes(b"backup")
            with mock.patch.dict(os.environ, environment, clear=True), mock.patch.object(
                backup.subprocess, "run"
            ) as run:
                backup.upload_dump(dump, "anime-companion/postgres/test.dump")

        command = run.call_args.args[0]
        child_environment = run.call_args.kwargs["env"]
        self.assertIn("--metadata=x-oss-server-side-encryption:AES256", command)
        self.assertIn("--bigfile-threshold", command)
        self.assertIn("--part-size", command)
        self.assertEqual("https://oss-ap-southeast-1.aliyuncs.com", child_environment["OSS_ENDPOINT"])
        self.assertEqual("ap-southeast-1", child_environment["OSS_REGION"])
        self.assertEqual("test-session-token", child_environment["OSS_SESSION_TOKEN"])
        serialized_command = " ".join(command)
        self.assertNotIn("test-access-key-id", serialized_command)
        self.assertNotIn("test-access-key-secret", serialized_command)
        self.assertNotIn("test-session-token", serialized_command)
        run.assert_called_once()
        self.assertTrue(run.call_args.kwargs["check"])


if __name__ == "__main__":
    unittest.main()

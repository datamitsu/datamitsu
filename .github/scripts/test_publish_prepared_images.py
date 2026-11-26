import importlib.util
import unittest
from pathlib import Path
from unittest.mock import patch

SCRIPT = Path(__file__).with_name("publish-prepared-images.py")
spec = importlib.util.spec_from_file_location("publish_prepared_images", SCRIPT)
if spec is None or spec.loader is None:
    raise ImportError(f"cannot load {SCRIPT}")
images = importlib.util.module_from_spec(spec)
spec.loader.exec_module(images)


class NonstablePromotionTests(unittest.TestCase):
    repository = "ghcr.io/datamitsu/datamitsu"
    plans = (
        ("0.0.0-preview.1.2", "source", "local", "sha256:" + "a" * 64, ()),
        ("0.0.0-preview.1.2-alpine", "source", "local", "sha256:" + "b" * 64, ()),
    )

    def test_older_preparation_retains_both_aliases(self):
        with (
            patch.object(
                images, "destination_digest", side_effect=["sha256:old", "sha256:old"]
            ),
            patch.object(images, "verified_nonstable_order", return_value=(10, 2)),
        ):
            self.assertEqual(
                images.nonstable_promotions(
                    self.repository, self.plans, (9, 99), "preview"
                ),
                [],
            )

    def test_newer_preparation_plans_only_nonstable_aliases(self):
        with patch.object(images, "destination_digest", side_effect=[None, None]):
            result = images.nonstable_promotions(
                self.repository, self.plans, (11, 1), "preview"
            )
        self.assertEqual([item[1] for item in result], ["preview", "preview-alpine"])
        self.assertEqual([item[2] for item in result], [plan[3] for plan in self.plans])

    def test_equal_identity_with_different_bytes_fails_closed(self):
        with (
            patch.object(images, "destination_digest", return_value="sha256:old"),
            patch.object(images, "verified_nonstable_order", return_value=(11, 1)),
            self.assertRaisesRegex(ValueError, "same preparation identity"),
        ):
            images.nonstable_promotions(self.repository, self.plans, (11, 1), "preview")

    def test_current_alias_requires_matching_immutable_tag(self):
        labels = {
            "org.opencontainers.image.version": "0.0.0-preview.202611251830.11.1",
            "com.datamitsu.preparation-run": "11",
            "com.datamitsu.preparation-attempt": "1",
        }
        with (
            patch.object(
                images,
                "run",
                return_value=images.json.dumps({"config": {"Labels": labels}}),
            ),
            patch.object(images, "destination_digest", return_value="sha256:different"),
            self.assertRaisesRegex(ValueError, "immutable tag"),
        ):
            images.verified_nonstable_order(self.repository, "sha256:current", "")

    def test_platform_metadata_must_agree(self):
        calls = []

        def inspect(*args):
            calls.append(args)
            run_id = "11" if "amd64" in args else "12"
            return images.json.dumps(
                {
                    "config": {
                        "Labels": {
                            "org.opencontainers.image.version": "0.0.0-preview.1.2",
                            "com.datamitsu.preparation-run": run_id,
                            "com.datamitsu.preparation-attempt": "1",
                        }
                    }
                }
            ).encode()

        with (
            patch.object(images, "run", side_effect=inspect),
            self.assertRaisesRegex(ValueError, "platform image metadata disagree"),
        ):
            images.preparation_order("oci-archive:image")
        self.assertEqual(len(calls), 2)


if __name__ == "__main__":
    unittest.main()

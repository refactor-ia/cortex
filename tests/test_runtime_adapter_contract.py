from __future__ import annotations

import unittest
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parents[1]
CONTRACT_PATH = REPO_ROOT / "references" / "runtime-adapter-contract.yaml"
DOCUMENT_PATH = REPO_ROOT / "references" / "runtime-adapter-contract.md"


class RuntimeAdapterContractTests(unittest.TestCase):
    def test_runtime_outcomes_preserve_ordinary_work(self) -> None:
        contract = yaml.safe_load(CONTRACT_PATH.read_text(encoding="utf-8"))

        self.assertEqual(contract["schema_version"], 1)
        outcomes = contract["runtime_outcomes"]
        self.assertEqual(outcomes["present_compatible"]["action"], "include_in_transaction")
        self.assertTrue(outcomes["present_compatible"]["all_or_nothing"])
        self.assertEqual(outcomes["absent"]["action"], "warn_and_continue")
        self.assertFalse(outcomes["absent"]["install"])
        self.assertEqual(outcomes["known_incompatible"]["action"], "skip_and_report")
        self.assertFalse(outcomes["known_incompatible"]["touch_adapter"])
        self.assertEqual(outcomes["unknown_version"]["action"], "warn_and_report_uncertainty")
        self.assertEqual(outcomes["no_compatible"]["action"], "report_only")
        self.assertFalse(outcomes["no_compatible"]["install"])
        self.assertFalse(outcomes["no_compatible"]["block_ordinary_work"])
        self.assertFalse(outcomes["no_compatible"]["touch_unrelated_configuration"])

    def test_ownership_transaction_and_projection_rules_are_explicit(self) -> None:
        contract = yaml.safe_load(CONTRACT_PATH.read_text(encoding="utf-8"))

        ownership = contract["ownership"]
        self.assertEqual(ownership["owner"], "Cortex")
        self.assertTrue(ownership["preserve_user_owned_configuration"])
        self.assertTrue(ownership["preserve_unrelated_configuration"])

        transaction = contract["transaction_safety"]
        self.assertTrue(transaction["backup"]["verifiable"])
        self.assertTrue(transaction["write"]["transactional"])
        self.assertEqual(transaction["verification"]["success_condition"], "exact_read_back")
        self.assertEqual(transaction["rollback"]["direction"], "reverse_transaction")
        self.assertEqual(transaction["uninstall"]["scope"], "cortex_owned_material_only")

        self.assertEqual(
            set(contract["projection_results"]),
            {"exact", "translated", "unrepresentable"},
        )

    def test_human_reference_covers_the_machine_contract(self) -> None:
        document = DOCUMENT_PATH.read_text(encoding="utf-8")

        self.assertIn("runtime-adapter-contract.yaml", document)
        for heading in (
            "## Runtime outcomes",
            "## Safe mutations",
            "## Projection results",
            "## Boundary",
        ):
            with self.subTest(heading=heading):
                self.assertIn(heading, document)


if __name__ == "__main__":
    unittest.main()

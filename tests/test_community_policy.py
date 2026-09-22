from __future__ import annotations

import os
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
EXPECTED_FILES = {
        ".github/ISSUE_TEMPLATE/bug.yml",
        ".github/ISSUE_TEMPLATE/config.yml",
        ".github/ISSUE_TEMPLATE/feature.yml",
        ".github/PULL_REQUEST_TEMPLATE.md",
        ".github/workflows/ci.yml",
        ".github/workflows/pr-validation.yml",
        ".gitignore",
        "AGENTS.md",
        "CODE_OF_CONDUCT.md",
        "CONTRIBUTING.md",
        "GOVERNANCE.md",
        "LICENSE",
        "LICENSE-CONTENT.md",
        "README.md",
        "SECURITY.md",
        "SUPPORT.md",
        "docs/README.md",
        "docs/architecture/overview.md",
        "go.mod",
        "cmd/cortex/main.go",
        "catalog/catalog.json",
        "catalog/families/quality-assurance/family.json",
        "catalog/families/quality-assurance/router.md",
        "catalog/families/reasoning/family.json",
        "catalog/families/reasoning/router.md",
        "catalog/families/model-intelligence/family.json",
        "catalog/families/model-intelligence/router.md",
        "catalog/families/execution/family.json",
        "catalog/families/execution/router.md",
        "catalog/families/web/family.json",
        "catalog/families/web/router.md",
        "catalog/families/mobile/family.json",
        "catalog/families/mobile/router.md",
        "catalog/families/pcsoft/family.json",
        "catalog/families/pcsoft/router.md",
        "catalog/families/services/family.json",
        "catalog/families/services/router.md",
        "catalog/families/personal/family.json",
        "catalog/families/personal/router.md",
        "catalog/families/memory-integration/family.json",
        "catalog/families/memory-integration/router.md",
        "catalog/families/documentation/family.json",
        "catalog/families/documentation/router.md",
        "catalog/families/quality-assurance/capabilities/requirements-analyst.json",
        "catalog/families/quality-assurance/capabilities/test-designer.json",
        "catalog/families/quality-assurance/capabilities/exploratory-tester.json",
        "catalog/families/quality-assurance/capabilities/adversarial-tester.json",
        "catalog/families/quality-assurance/capabilities/test-runner.json",
        "catalog/families/quality-assurance/capabilities/evidence-auditor.json",
        "catalog/families/quality-assurance/sources/requirements-analyst.md",
        "catalog/families/quality-assurance/sources/test-designer.md",
        "catalog/families/quality-assurance/sources/exploratory-tester.md",
        "catalog/families/quality-assurance/sources/adversarial-tester.md",
        "catalog/families/quality-assurance/sources/test-runner.md",
        "catalog/families/quality-assurance/sources/evidence-auditor.md",
        "internal/artifact/bundle.go",
        "internal/artifact/bundle_test.go",
        "internal/artifact/manifest.go",
        "internal/artifact/manifest_test.go",
        "internal/backupjournal/manifest.go",
        "internal/backupjournal/manifest_test.go",
        "internal/backupjournal/fs.go",
        "internal/backupjournal/fs_test.go",
        "internal/backupjournal/create.go",
        "internal/backupjournal/create_test.go",
        "internal/backupjournal/open.go",
        "internal/backupjournal/open_test.go",
        "internal/backupjournal/intent.go",
        "internal/backupjournal/intent_test.go",
        "internal/backupjournal/transition.go",
        "internal/backupjournal/transition_test.go",
        "internal/backuprecovery/executor.go",
        "internal/backuprecovery/executor_test.go",
        "internal/backuprecovery/filesystem.go",
        "internal/backuprecovery/filesystem_test.go",
        "internal/backuprecovery/intent.go",
        "internal/backuprecovery/intent_test.go",
        "internal/backuprecovery/model.go",
        "internal/backuprecovery/model_test.go",
        "internal/filetxn/apply.go",
        "internal/filetxn/apply_test.go",
        "internal/filetxn/directories.go",
        "internal/filetxn/directories_external_test.go",
        "internal/filetxn/directories_test.go",
        "internal/filetxn/snapshot.go",
        "internal/filetxn/snapshot_test.go",
        "internal/filetxn/restart.go",
        "internal/filetxn/restart_test.go",
        "internal/installstate/manifest.go",
        "internal/installstate/manifest_test.go",
        "internal/installstate/v2_model.go",
        "internal/installstate/v2_model_test.go",
        "internal/installstate/v2_codec_test.go",
        "internal/installplan/plan.go",
        "internal/installplan/plan_test.go",
        "internal/installplan/actor_aware.go",
        "internal/installplan/actor_aware_test.go",
        "internal/installtxn/installtxn.go",
        "internal/installtxn/installtxn_test.go",
        "internal/uninstalltxn/uninstalltxn.go",
        "internal/uninstalltxn/uninstalltxn_test.go",
        "internal/installobserve/classify.go",
        "internal/installobserve/classify_test.go",
        "internal/installobserve/filesystem.go",
        "internal/installobserve/filesystem_internal_test.go",
        "internal/installobserve/filesystem_test.go",
        "internal/installobserve/identity.go",
        "internal/installobserve/identity_test.go",
        "internal/installobserve/admission.go",
        "internal/installobserve/admission_test.go",
        "internal/installobserve/shadows.go",
        "internal/installobserve/shadows_test.go",
        "internal/installobserve/uninstall.go",
        "internal/installobserve/uninstall_test.go",
        "internal/installcoord/candidate.go",
        "internal/installcoord/candidate_test.go",
        "internal/installcoord/installcoord.go",
        "internal/installcoord/installcoord_test.go",
        "internal/atomicfile/create.go",
        "internal/atomicfile/remove.go",
        "internal/atomicfile/remove_test.go",
        "internal/atomicfile/replace.go",
        "internal/atomicfile/replace_test.go",
        "internal/catalog/admission.go",
        "internal/catalog/admission_test.go",
        "internal/catalog/capability.go",
        "internal/catalog/capability_test.go",
        "internal/catalog/catalog.go",
        "internal/catalog/catalog_test.go",
        "internal/catalog/family.go",
        "internal/catalog/family_test.go",
        "internal/catalog/load.go",
        "internal/qaactor/actor_test.go",
        "internal/qaactor/render.go",
        "internal/qaactor/source.go",
        "internal/qaactor/validate.go",
        "internal/qaactor/project.go",
        "internal/qaactor/project_test.go",
        "internal/qaactor/bind.go",
        "internal/qaactor/bind_test.go",
        "internal/qaactor/destination.go",
        "internal/qaactor/destination_test.go",
        "internal/qaactor/testdata/pi-actors/requirements-analyst.golden",
        "internal/qaactor/testdata/pi-actors/test-designer.golden",
        "internal/qaactor/testdata/pi-actors/exploratory-tester.golden",
        "internal/qaactor/testdata/pi-actors/adversarial-tester.golden",
        "internal/qaactor/testdata/pi-actors/test-runner.golden",
        "internal/qaactor/testdata/pi-actors/evidence-auditor.golden",
        "internal/qarole/role.go",
        "internal/qarole/role_test.go",
        "internal/qarole/source.go",
        "internal/qarole/source_test.go",
        "internal/qaroute/policy.go",
        "internal/qaroute/profile.go",
        "internal/qaroute/resolve.go",
        "internal/qaroute/policy_test.go",
        "internal/qaroute/profile_test.go",
        "internal/qaadmission/receipt.go",
        "internal/qaadmission/identity.go",
        "internal/qaadmission/backend.go",
        "internal/qaadmission/backend_test.go",
        "internal/qaadmission/bounds.go",
        "internal/qaadmission/redact.go",
        "internal/qaadmission/receipt_test.go",
        "internal/qaadmission/identity_test.go",
        "internal/qaadmission/bounds_test.go",
        "internal/qaadmission/privacy_test.go",
        "internal/qaadmission/validate.go",
        "internal/qaadmission/validate_test.go",
        "internal/qagit/binding.go",
        "internal/qagit/command.go",
        "internal/qagit/binding_test.go",
        "internal/qagit/session.go",
        "internal/qagit/session_test.go",
        "internal/qagit/testdata/session-plan/marker.txt",
        "internal/qagit/testdata/session-plan/create.argv",
        "internal/qagit/testdata/session-plan/remove.argv",
        "internal/qagit/testdata/clean-binding/sha1-head.oid",
        "internal/qagit/testdata/clean-binding/sha1-tree.oid",
        "internal/qagit/testdata/clean-binding/sha256-head.oid",
        "internal/qagit/testdata/clean-binding/sha256-tree.oid",
        "internal/qapi/backend.go",
        "internal/qapi/backendselect.go",
        "internal/qapi/backendselect_test.go",
        "internal/qapi/availability_test.go",
        "internal/qapi/claude.go",
        "internal/qapi/contracts.go",
        "internal/qapi/claude_test.go",
        "internal/qapi/opencode.go",
        "internal/qapi/opencode_test.go",
        "internal/qapi/version.go",
        "internal/qapi/qualification.go",
        "internal/qapi/qualification_test.go",
        "internal/qapi/probes.go",
        "internal/qapi/probes_test.go",
        "internal/qapi/invocation.go",
        "internal/qapi/invocation_test.go",
        "internal/qapi/input.go",
        "internal/qapi/input_test.go",
        "internal/qapi/runner.go",
        "internal/qapi/run_test.go",
        "internal/qapi/runtime.go",
        "internal/qapi/runtime_test.go",
        "internal/qapi/report.go",
        "internal/qapi/report_test.go",
        "internal/qapi/response.go",
        "internal/qapi/response_test.go",
        "internal/qapi/testdata_test.go",
            "internal/qapi/request.go",
            "internal/qapi/request_test.go",
            "internal/qapi/profile.go",
            "internal/qapi/profile_test.go",
            "internal/qapi/catalog_binding.go",
            "internal/qapi/catalog_binding_test.go",
            "internal/qapi/preflight.go",
            "internal/qapi/preflight_test.go",
        "internal/qapi/testdata/pi-0.85.1/invocation/requirements-analyst.golden",
        "internal/qapi/testdata/pi-0.85.1/invocation/test-runner.golden",
        "internal/qapi/testdata/pi-0.85.1/input/requirements-analyst.golden",
        "internal/qapi/testdata/pi-0.85.1/input/test-runner.golden",
        "internal/qapi/testdata/pi-0.85.1/auth/ready.json",
        "internal/qapi/testdata/pi-0.85.1/models/model-list.txt",
        "internal/qapi/testdata/pi-0.85.1/qualification/sdk-positive.json",
        "internal/qapi/testdata/pi-0.85.1/qualification/sdk-extra-tool.json",
        "internal/qapi/testdata/pi-0.85.1/qualification/sdk-no-skill.json",
        "internal/qapi/testdata/pi-0.85.1/responses/source-derived-minimal.jsonl",
        "internal/qapi/testdata/claude-2.1.278/README.md",
        "internal/qapi/testdata/claude-2.1.278/auth/status-authenticated.json",
        "internal/qapi/testdata/claude-2.1.278/auth/status-unauthenticated.json",
        "internal/qapi/testdata/claude-2.1.278/responses/stream-success.jsonl",
        "internal/qapi/testdata/claude-2.1.278/responses/stream-unauthenticated.jsonl",
        "internal/qapi/testdata/opencode-1.18.25/README.md",
        "internal/qapi/testdata/opencode-1.18.25/auth/credentials-absent.txt",
        "internal/qapi/testdata/opencode-1.18.25/auth/credentials-present.txt",
        "internal/qapi/testdata/opencode-1.18.25/models/available.txt",
        "internal/qapi/testdata/opencode-1.18.25/models/unavailable.txt",
        "internal/qapi/testdata/opencode-1.18.25/responses/stream-error.json",
        "internal/qapi/testdata/opencode-1.18.25/responses/stream-success.json",
        "internal/catalog/load_catalog.go",
        "internal/catalog/load_catalog_test.go",
        "internal/catalog/snapshot.go",
        "internal/catalog/snapshot_test.go",
        "internal/releasecatalog/source.go",
        "internal/releasecatalog/source_test.go",
        "internal/releasecatalog/source_external_test.go",
        "internal/builtinassets/assets.go",
        "internal/builtinassets/assets_test.go",
        "internal/builtinassets/catalog/families/documentation/family.json",
        "internal/builtinassets/catalog/families/documentation/router.md",
        "internal/builtinassets/catalog/families/execution/family.json",
        "internal/builtinassets/catalog/families/execution/router.md",
        "internal/builtinassets/catalog/families/memory-integration/family.json",
        "internal/builtinassets/catalog/families/memory-integration/router.md",
        "internal/builtinassets/catalog/families/mobile/family.json",
        "internal/builtinassets/catalog/families/mobile/router.md",
        "internal/builtinassets/catalog/families/model-intelligence/family.json",
        "internal/builtinassets/catalog/families/model-intelligence/router.md",
        "internal/builtinassets/catalog/families/pcsoft/family.json",
        "internal/builtinassets/catalog/families/pcsoft/router.md",
        "internal/builtinassets/catalog/families/personal/family.json",
        "internal/builtinassets/catalog/families/personal/router.md",
        "internal/builtinassets/catalog/families/quality-assurance/capabilities/adversarial-tester.json",
        "internal/builtinassets/catalog/families/quality-assurance/capabilities/evidence-auditor.json",
        "internal/builtinassets/catalog/families/quality-assurance/capabilities/exploratory-tester.json",
        "internal/builtinassets/catalog/families/quality-assurance/capabilities/requirements-analyst.json",
        "internal/builtinassets/catalog/families/quality-assurance/capabilities/test-designer.json",
        "internal/builtinassets/catalog/families/quality-assurance/capabilities/test-runner.json",
        "internal/builtinassets/catalog/families/quality-assurance/family.json",
        "internal/builtinassets/catalog/families/quality-assurance/router.md",
        "internal/builtinassets/catalog/families/quality-assurance/sources/adversarial-tester.md",
        "internal/builtinassets/catalog/families/quality-assurance/sources/evidence-auditor.md",
        "internal/builtinassets/catalog/families/quality-assurance/sources/exploratory-tester.md",
        "internal/builtinassets/catalog/families/quality-assurance/sources/requirements-analyst.md",
        "internal/builtinassets/catalog/families/quality-assurance/sources/test-designer.md",
        "internal/builtinassets/catalog/families/quality-assurance/sources/test-runner.md",
        "internal/builtinassets/catalog/families/reasoning/family.json",
        "internal/builtinassets/catalog/families/reasoning/router.md",
        "internal/builtinassets/catalog/families/services/family.json",
        "internal/builtinassets/catalog/families/services/router.md",
        "internal/builtinassets/catalog/families/web/family.json",
        "internal/builtinassets/catalog/families/web/router.md",
        "internal/builtinassets/embedded_catalog_drift_test.go",
        "internal/builtinassets/catalog/catalog.json",
        "internal/catalog/load_test.go",
        "internal/cli/doctor.go",
        "internal/cli/doctor_test.go",
        "internal/cli/qa_test.go",
        "internal/cli/install.go",
        "internal/cli/qabackend.go",
        "internal/cli/install_test.go",
        "internal/cli/lifecycle_e2e_test.go",
        "internal/cli/pi_real_smoke_test.go",
        "internal/cli/subscription_auth_test.go",
        "internal/cli/opencode_real_smoke_test.go",
        "internal/cli/claude_real_smoke_test.go",
        "internal/cli/uninstall_test.go",
        "internal/cli/update.go",
        "internal/cli/update_test.go",
        "internal/lifecycleharness/install_update_parity_test.go",
        "internal/lifecycleharness/uninstall_parity_test.go",
        "internal/skillrender/render.go",
        "internal/skillrender/render_test.go",
        "internal/skillprojection/project.go",
        "internal/skillprojection/project_test.go",
        "internal/skillartifact/bind.go",
        "internal/skillartifact/bind_test.go",
        "internal/skilldest/plan.go",
        "internal/skilldest/plan_test.go",
        "internal/skilldest/qa.go",
        "internal/skilldest/qa_test.go",
        "internal/skilldest/qa_catalog_test.go",
        "internal/skillroot/resolve.go",
        "internal/skillroot/resolve_test.go",
        "internal/safepath/path.go",
        "internal/safepath/path_test.go",
        "internal/runtimematrix/matrix.go",
        "internal/runtimematrix/matrix_test.go",
        "internal/runtimeprobe/probe.go",
        "internal/runtimeprobe/probe_test.go",
        "internal/runtimecompat/policy.go",
        "internal/runtimecompat/evidence_test.go",
        "internal/runtimecompat/policy_test.go",
        "internal/smokeplan/plan.go",
        "internal/smokeplan/plan_test.go",
        "internal/adapterplan/plan.go",
        "internal/adapterplan/plan_test.go",
        "internal/ownership/plan.go",
        "internal/ownership/plan_test.go",
        "internal/projection/plan.go",
        "internal/projection/plan_test.go",
        "internal/projection/result.go",
        "internal/projection/result_test.go",
        "references/runtime-adapter-contract.md",
        "references/runtime-adapter-contract.yaml",
        "schemas/artifact.schema.json",
        "schemas/capability.schema.json",
        "schemas/catalog.schema.json",
        "schemas/family.schema.json",
        "schemas/install-state.schema.json",
        "tests/__init__.py",
        "tests/test_community_policy.py",
        "tests/test_github_policy.py",
        "tests/test_runtime_adapter_contract.py",
}
IGNORED_LOCAL_DIRECTORIES = {
        ".ai",
        ".atl",
        ".codegraph",
        ".git",
        ".pytest_cache",
        ".qa-test-cache",
        ".qa-test-tmp",
        ".ruff_cache",
        "__pycache__",
        "odd",
}
CHOOSER_URL = "https://github.com/refactor-ia/cortex/issues/new/choose"


def read_text(relative_path: str) -> str:
        return (REPO_ROOT / relative_path).read_text(encoding="utf-8")


class CommunityPolicyTests(unittest.TestCase):
        def test_publication_inventory_is_exact_and_not_linked(self) -> None:
                actual_files = set()
                for directory, directory_names, file_names in os.walk(
                        REPO_ROOT, topdown=True, followlinks=False
                ):
                        directory_path = Path(directory)
                        publishable_directories = []
                        for name in directory_names:
                                path = directory_path / name
                                if path.is_symlink():
                                        actual_files.add(
                                                path.relative_to(REPO_ROOT).as_posix()
                                        )
                                elif name in IGNORED_LOCAL_DIRECTORIES:
                                        continue
                                else:
                                        publishable_directories.append(name)
                        directory_names[:] = publishable_directories

                        actual_files.update(
                                path.relative_to(REPO_ROOT).as_posix()
                                for name in file_names
                                if (path := directory_path / name).is_file()
                                or path.is_symlink()
                        )

                self.assertEqual(len(EXPECTED_FILES), 343)
                self.assertEqual(len(actual_files), 343)
                self.assertEqual(actual_files, EXPECTED_FILES)

                gitignore_entries = set(read_text(".gitignore").splitlines())
                for local_state_entry in (
                        ".atl/",
                        ".ai/",
                        ".codegraph/",
                        ".pytest_cache/",
                        "__pycache__/",
                ):
                        with self.subTest(gitignore_entry=local_state_entry):
                                self.assertIn(local_state_entry, gitignore_entries)

                for relative_path in EXPECTED_FILES:
                        with self.subTest(path=relative_path):
                                self.assertFalse(
                                        (REPO_ROOT / relative_path).is_symlink()
                                )

                pi = read_text("internal/cli/pi_real_smoke_test.go")
                opencode = read_text("internal/cli/opencode_real_smoke_test.go")
                claude = read_text("internal/cli/claude_real_smoke_test.go")
                for source in (pi, opencode, claude):
                        self.assertNotIn("CORTEX_REAL_SMOKE_CREDENTIAL_ENV", source)
                        self.assertNotIn("smokeCredentials", source)
                self.assertIn("auth=subscription_copy", pi)
                self.assertIn("auth=subscription_copy", opencode)
                self.assertIn("XDG_DATA_HOME=", opencode)
                self.assertIn("auth=subscription_oauth_token", claude)
                self.assertNotIn("CORTEX_REAL_SMOKE_SUBSCRIPTION_AUTH_FILE", claude)

        def test_foundation_documents_state_clean_target_status(self) -> None:
                readme = read_text("README.md")
                for expected in (
                        "https://github.com/refactor-ia/cortex",
                        "target architecture and community foundation",
                        "Implementation is not complete",
                        "docs/README.md",
                        "CONTRIBUTING.md",
                        "LICENSE-CONTENT.md",
                ):
                        self.assertIn(expected, readme)
                self.assertNotIn("legacy harness", readme.lower())

                contributing = read_text("CONTRIBUTING.md")
                for expected in (
                        "Refs #123",
                        "Fixes #123",
                        CHOOSER_URL,
                        "below 400 changed lines",
                        "do not push directly to `main`",
                        "Required checks and review must pass",
                        "GitHub repository permissions and branch protection are the authorization authority",
                        ".env.*",
                        ".ai/**",
                        ".atl/**",
                        "in English",
                ):
                        self.assertIn(expected, contributing)
                for forbidden in ("owner bypass", "TARS", "git-specialist", "harness"):
                        self.assertNotIn(forbidden, contributing)

                agents = read_text("AGENTS.md")
                for expected in (
                        "Architecture documentation is the authority",
                        "public communication in English",
                        "private maintainer conversation may use another language",
                        "`.env*`, `.ai/**`, or `.atl/**`",
                        "generation or tool attribution",
                        "Refs #N` or `Fixes #N",
                        "400-line review budget",
                        "conventional commits",
                        "user-owned",
                        "`pnpm` rather than `npm`",
                        "no Git, SDD, TDD, or review authority",
                        "`git-specialist` is permanently excluded",
                ):
                        self.assertIn(expected, agents)
                self.assertEqual(agents.count("git-specialist"), 1)

        def test_documentation_authorities_are_local_and_current(self) -> None:
                docs = read_text("docs/README.md")
                for expected in (
                        "Root README",
                        "architecture/overview.md",
                        "CONTRIBUTING.md",
                        "CODE_OF_CONDUCT.md",
                        "SECURITY.md",
                        "GOVERNANCE.md",
                        "SUPPORT.md",
                        "LICENSE-CONTENT.md",
                        "Legacy source material remains outside this repository",
                ):
                        self.assertIn(expected, docs)
                for forbidden in (
                        "CODEBASE-GUIDE",
                        "three-brain-guide",
                        "release-publishing",
                ):
                        self.assertNotIn(forbidden, docs)

                architecture = read_text("docs/architecture/overview.md")
                self.assertIn(
                        "Legacy source material remains outside this repository and does not define it.",
                        architecture,
                )
                self.assertIn(
                        "imports approved public artifacts only and does not inherit legacy history wholesale",
                        architecture,
                )
                self.assertNotIn("Repository relocation is not performed", architecture)

        def test_content_license_and_community_documents_link_correctly(self) -> None:
                content_license = read_text("LICENSE-CONTENT.md")
                for expected in (
                        "MIT License",
                        "CC BY-SA 4.0",
                        "https://creativecommons.org/licenses/by-sa/4.0/",
                        "Third-party or adapted material",
                        "CODE_OF_CONDUCT.md",
                ):
                        self.assertIn(expected, content_license)

                self.assertIn("## Reporting an Issue", read_text("CODE_OF_CONDUCT.md"))
                support = read_text("SUPPORT.md")
                for expected in (
                        CHOOSER_URL,
                        "security@refactoria.dev",
                        "conduct@refactoria.dev",
                ):
                        self.assertIn(expected, support)

        def test_ci_is_minimal_and_pins_every_check(self) -> None:
                workflow = read_text(".github/workflows/ci.yml")
                for expected in (
                        "pull_request:",
                        "push:",
                        "- main",
                        "contents: read",
                        "concurrency:",
                        "cancel-in-progress: true",
                        "timeout-minutes:",
                        "actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10",
                        "persist-credentials: false",
                        "actions/setup-python@a309ff8b426b58ec0e2a45f0f869d46889d02405",
                        'python-version: "3.11"',
                        "PyYAML==6.0.2",
                        # Every check is pinned by name. Removing one must turn this
                        # suite red, including the Go tests this file previously
                        # asserted CI ran without ever checking for them.
                        "go vet ./...",
                        "go test ./...",
                        "go test -race ./...",
                        "python -m unittest tests.test_community_policy",
                        "python -m unittest tests.test_github_policy",
                        "python -m unittest tests.test_runtime_adapter_contract",
                ):
                        self.assertIn(expected, workflow)
                for forbidden in (
                        "secrets",
                        "contents: write",
                        "upload-artifact",
                        "publish",
                        "legacy",
                        "npm ",
                ):
                        with self.subTest(forbidden=forbidden):
                                self.assertNotIn(forbidden, workflow)


if __name__ == "__main__":
        unittest.main()

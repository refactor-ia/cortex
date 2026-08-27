from __future__ import annotations

import ast
import re
import unittest
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
FORMS = {
    "feature.yml": {
        "label": "type:feature",
        "fields": {
            "problem": "Problem",
            "outcome": "Desired outcome",
            "scope": "Scope and non-goals",
            "acceptance": "Acceptance criteria",
            "evidence": "Evidence",
        },
    },
    "bug.yml": {
        "label": "type:bug",
        "fields": {
            "problem": "Problem",
            "outcome": "Expected outcome",
            "scope": "Affected scope",
            "acceptance": "Acceptance criteria",
            "evidence": "Evidence",
        },
    },
}
TYPES = (
    "type:feature",
    "type:bug",
    "type:docs",
    "type:refactor",
    "type:chore",
    "type:breaking-change",
)
BRANCH_PATTERN = (
    r"^(feat|fix|chore|docs|style|refactor|perf|test|build|ci|revert)/[a-z0-9._-]+$"
)
PROHIBITED_REQUIRED_PROMPTS = re.compile(
    r"\b(?:i\s+(?:agree|confirm|acknowledge|affirm)|password|secret|token|credential|private key)\b",
    re.IGNORECASE,
)


def text(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def load_yaml(path: str) -> Any:
    """Load policy YAML without coercing the GitHub Actions `on` key to bool."""
    return yaml.load(text(path), Loader=yaml.BaseLoader)  # nosec B506: BaseLoader parses scalars as strings.


def mapping(value: Any, name: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise AssertionError(f"{name} must be a mapping")
    return value


class GitHubPolicyTests(unittest.TestCase):
    def test_yaml_loader_preserves_workflow_event_key(self) -> None:
        workflow = mapping(load_yaml(".github/workflows/pr-validation.yml"), "workflow")
        self.assertIn("pull_request", mapping(workflow["on"], "workflow events"))

    def test_issue_chooser_disables_blank_issues_and_contacts(self) -> None:
        config = mapping(
            load_yaml(".github/ISSUE_TEMPLATE/config.yml"), "issue chooser"
        )
        self.assertEqual(config["blank_issues_enabled"], "false")
        self.assertEqual(config["contact_links"], [])

    def test_issue_forms_require_concrete_governance_evidence(self) -> None:
        for name, expected in FORMS.items():
            with self.subTest(form=name):
                form = mapping(load_yaml(f".github/ISSUE_TEMPLATE/{name}"), name)
                self.assertEqual(form["labels"], [expected["label"]])
                self.assertNotIn("status:approved", form["labels"])

                body = form["body"]
                self.assertIsInstance(body, list)
                self.assertEqual(len(body), len(expected["fields"]) + 1)
                introduction = mapping(body[0], "form introduction")
                self.assertEqual(introduction["type"], "markdown")
                fields = [mapping(item, "form field") for item in body[1:]]
                self.assertEqual(
                    [field.get("id") for field in fields], list(expected["fields"])
                )

                for field, (field_id, field_label) in zip(
                    fields, expected["fields"].items(), strict=True
                ):
                    attributes = mapping(field["attributes"], f"{field_id} attributes")
                    validations = mapping(
                        field["validations"], f"{field_id} validations"
                    )
                    self.assertEqual(field["type"], "textarea")
                    self.assertEqual(attributes["label"], field_label)
                    self.assertEqual(validations["required"], "true")
                    prompt = " ".join(
                        str(attributes.get(key, ""))
                        for key in ("label", "description", "value", "placeholder")
                    )
                    self.assertNotRegex(prompt, PROHIBITED_REQUIRED_PROMPTS)

                self.assertNotIn("checkboxes", [field["type"] for field in fields])

    def test_pull_request_template_has_required_review_contract(self) -> None:
        template = text(".github/PULL_REQUEST_TEMPLATE.md")
        required_headings = (
            "Linked Issue",
            "PR Type",
            "Summary",
            "Changes",
            "Focused Tests",
            "Runtime Harness",
            "Rollback",
            "Chain Dependencies / Out of Scope",
            "Checklist",
        )
        for heading in required_headings:
            self.assertRegex(template, rf"(?m)^##\s+{re.escape(heading)}\s*$")

            references = re.findall(
                r"(?im)^\s*(refs|closes|fixes|resolves)\s+#N\s*$", template
            )
            self.assertEqual(references, ["Refs"])
        for phrase in (
            "Linked issue is open and `status:approved`.",
            "Exactly one matching `type:*` label is applied.",
            "Focused tests and runtime harness evidence are recorded.",
            "Rollback and out-of-scope boundaries are documented.",
        ):
            self.assertRegex(
                template, rf"(?m)^\s*-\s*\[\s*\]\s*{re.escape(phrase)}\s*$"
            )

        type_checkboxes = re.findall(
            r"(?m)^\s*-\s*\[\s*\]\s*`(type:[a-z-]+)`\s+(.+?)\s*$", template
        )
        self.assertEqual(
            type_checkboxes,
            list(
                zip(
                    TYPES,
                    (
                        "Feature",
                        "Bug fix",
                        "Documentation",
                        "Refactoring",
                        "Maintenance",
                        "Breaking change",
                    ),
                )
            ),
        )

    def test_workflow_enforces_safe_pr_policy(self) -> None:
        workflow = mapping(load_yaml(".github/workflows/pr-validation.yml"), "workflow")
        events = mapping(workflow["on"], "workflow events")
        self.assertEqual(
            mapping(events["pull_request"], "pull request event")["types"],
            ["opened", "edited", "labeled", "unlabeled", "synchronize", "reopened"],
        )
        self.assertEqual(
            workflow["permissions"], {"issues": "read", "pull-requests": "read"}
        )

        jobs = mapping(workflow["jobs"], "workflow jobs")
        job = mapping(jobs["validate"], "validation job")
        self.assertEqual(job["runs-on"], "ubuntu-latest")
        self.assertEqual(
            job["env"],
            {
                "GH_TOKEN": "${{ github.token }}",
                "PR_NUMBER": "${{ github.event.pull_request.number }}",
                "PR_BRANCH": "${{ github.head_ref }}",
                "PR_BODY": "${{ github.event.pull_request.body }}",
            },
        )
        self.assertEqual(len(job["steps"]), 1)
        step = mapping(job["steps"][0], "validation step")
        self.assertEqual(set(step), {"name", "run"})
        self.assertEqual(step["name"], "Validate pull request metadata")
        script = step["run"]
        self.assertIsInstance(script, str)

        def keys(value: Any) -> set[str]:
            if isinstance(value, dict):
                return set(value) | set().union(
                    *(keys(child) for child in value.values())
                )
            if isinstance(value, list):
                return set().union(*(keys(child) for child in value))
            return set()

        self.assertNotIn("pull_request_target", keys(workflow))
        self.assertNotIn("uses", keys(workflow))
        self.assertRegex(
            script,
            re.compile(
                rf'(?m)^\s*if \[\[ ! "\$PR_BRANCH" =~ {re.escape(BRANCH_PATTERN)} \]\]; then$'
            ),
        )
        pattern_match = re.search(
            r're\.findall\(\s*(r"(?:[^"\\]|\\.)*")\s*,', script
        )
        if pattern_match is None:
            self.fail("workflow must find issue references with a regular expression")
        issue_reference_pattern = re.compile(ast.literal_eval(pattern_match.group(1)))
        for keyword in ("Refs", "Closes", "Fixes", "Resolves"):
            with self.subTest(keyword=keyword):
                self.assertEqual(issue_reference_pattern.findall(f"{keyword} #42"), ["42"])
                self.assertEqual(issue_reference_pattern.findall(f"{keyword.upper()} #42"), ["42"])
        for invalid_body in (
            "Reference #42",
            "Refs #0",
            "prefix Refs #42",
            "Refs #42 suffix",
        ):
            with self.subTest(invalid_body=invalid_body):
                self.assertEqual(issue_reference_pattern.findall(invalid_body), [])
        self.assertNotEqual(
            len(issue_reference_pattern.findall("Refs #42\nFixes #43")), 1
        )
        self.assertRegex(script, r"len\(matches\)\s*!=\s*1")

        api_paths = re.findall(
            r'(?m)^\s*\w+="\$\(gh api --method GET "([^"]+)"\)"$', script
        )
        self.assertEqual(
            api_paths,
            [
                "repos/${GITHUB_REPOSITORY}/issues/${issue_number}",
                "repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}",
            ],
        )
        self.assertRegex(script, r"\.state\s*==\s*\"open\"")
        self.assertRegex(script, r"index\(\"status:approved\"\)\s*!=\s*null")
        self.assertRegex(script, r"startswith\(\"type:\"\)")
        self.assertRegex(script, r"\$\{#type_labels\[@\]\}\s*!=\s*1")

        allowed_types = re.search(
            r"(?s)case\s+\"\$\{type_labels\[0\]\}\"\s+in\s+([a-z:|-]+)\s*\)\s*;;",
            script,
        )
        if allowed_types is None:
            self.fail("type-label allowlist must be present")
        self.assertEqual(tuple(allowed_types.group(1).split("|")), TYPES)
        self.assertRegex(
            script, r'\*\)\s*echo\s+"unknown type:\* label";\s*exit 1\s*;;'
        )
        self.assertNotRegex(
            script, r"(?im)\b(?:echo|printf)\b[^\n]*(?:\$PR_BODY|\$\{PR_BODY\})"
        )


if __name__ == "__main__":
    unittest.main()

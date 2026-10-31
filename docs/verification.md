# Verification and release evidence

This page carries the audit material referenced from the [root README](../README.md): how to verify a downloaded archive, what runtime admission was tested with what evidence, and how to report results. It records what was tested; it does not gate what you may install.

## Verify an archive

Each alpha20 archive contains `cortex`, `LICENSE`, `LICENSE-CONTENT.md`, `BUILDINFO.json` (source commit, tree, Go version, target, and binary SHA-256), and the matching `catalog/` tree. The release also provides two standalone BUILDINFO files and `SHA256SUMS`.

Run this Bash block in a new, empty directory. On Linux AMD64, change `PLATFORM` to `linux_amd64`. The subshell stops on failure; exactly one matching checksum entry is required before extraction. Checksums verify integrity against the downloaded manifest, not independent publisher authentication.

```bash
(
  set -eu
  VERSION=v0.1.0-alpha.20
  PLATFORM=darwin_arm64
  archive="cortex-$VERSION-$PLATFORM.tar.gz"
  base="https://github.com/refactor-ia/cortex/releases/download/$VERSION"
  curl -fL --output "$archive" "$base/$archive"
  curl -fL --output SHA256SUMS "$base/SHA256SUMS"
  awk -v archive="$archive" '$2 == archive { print; n++ } END { if (n != 1) exit 1 }' \
    SHA256SUMS > archive.sha256
  case "$PLATFORM" in
    darwin_arm64) shasum -a 256 -c archive.sha256 ;;
    linux_amd64) sha256sum -c archive.sha256 ;;
    *) printf 'Unsupported platform: %s\n' "$PLATFORM" >&2; exit 1 ;;
  esac
  tar xzf "$archive"
  ./cortex --version
)
```

Expected identity: `version=v0.1.0-alpha.20`, revision `d00bc17a305232d5fed9ed15407b372d902587c2`, `build_dirty=false`, and the catalog ID below. macOS ARM64 and Linux AMD64 are the published targets; Windows is not supported and does not currently build.

### Building from source

```bash
git clone https://github.com/refactor-ia/cortex.git
cd cortex
go build -o cortex ./cmd/cortex
```

For a release-style source build, inject both identity fields into the binary (substitute the intended version and build commit):

```bash
go build -ldflags="-X github.com/refactor-ia/cortex/internal/cli.buildVersion=v0.1.0-alpha.20 -X github.com/refactor-ia/cortex/internal/cli.buildRevision=0123456789abcdef" -o cortex ./cmd/cortex
./cortex --version
```

`--version` reports `version`, `revision`, `build_dirty`, and `embedded_catalog` from the **running binary**. Each injected field overrides only its corresponding Go build-info field; without injection, Go module version and `vcs.revision` are used when embedded. Missing fields and the Go `(devel)` version report `unavailable`. `build_dirty` reflects Go's embedded `vcs.modified` setting (`true`, `false`, or `unavailable`), even with explicit release fields; it does not certify the injected revision. The catalog ID is resolved from the admitted embedded snapshot. A catalog in a source cache, checkout, or adjacent directory is not evidence about the active binary, and `--version` never reads one.

## Alpha20 delivered evidence

[v0.1.0-alpha.20](https://github.com/refactor-ia/cortex/releases/tag/v0.1.0-alpha.20) is an **uncertified evaluation prerelease**, built from commit `d00bc17a305232d5fed9ed15407b372d902587c2`, tree `199642d0813a1158c6442019b0e75ad4cce73dc5`, with Go 1.27.1 and CGO disabled. The embedded catalog is `catalog.1.cb6c8289cc0d4d3fd043328550bd7a2ae2b363445a7a203bce96e46c033ed432`: twelve capabilities across reasoning, quality assurance and documentation.

| Evidence | Observed scope |
| --- | --- |
| Source CI | Postmerge vet, full tests, race and repository policy checks passed at the release source. |
| Release artifacts | Both archives' safe paths/types, binary hashes, BUILDINFO, licenses and 47 catalog files checked against clean source; independent read-only artifact review passed. All five published assets downloaded and compared byte-for-byte; tag resolves to the source commit. |
| Packaged native lifecycle | Extracted macOS binary installed, previewed/applied updates, read back twelve skills and uninstalled in disposable state. Preview wrote nothing; a foreign sentinel survived cleanup. Published alpha19 to alpha20 upgrade increased ten skills to twelve. |
| Real deployments after publication | Alpha20 binaries executed on macOS ARM64 and Linux AMD64. Two Linux deployments updated Pi, OpenCode and Claude Code, with twelve skills and all three maintainer sources read back per runtime. The macOS deployment updated OpenCode and Claude Code; Pi refused unverified external guidance and remained unchanged. |
| Evidence boundary | Deployment execution was operator-observed; independent review inspected supplied remote results, not independent remote execution. Binary backups and Cortex transaction backups were recorded. These observations do not establish preservation of every unrelated filesystem byte. |

The release initially carried Linux cross-build/header evidence only; Linux execution above was observed during subsequent deployment, not retroactively part of the prerelease audit. Packaging uses normalized archive metadata, but no independent reproducible rebuild was demonstrated.

Two isolated local QA/update tests previously failed with the same errors on pristine main and both integration heads. That accepted baseline gap is not reported as a local full-suite pass; source CI passed. A deployment verifier also initially failed after a successful Pi update because an unrelated bare skill and the Cortex-prefixed skill coexisted. Recovery selected the ownership manifest's path and hash, continued without reapplying Pi, and verified the foreign skill unchanged during recovery. This was not a Cortex update failure.

**Still unproven:** a complete live-model QA showcase from the published binary, the external review/triage pilot, full runtime-family parity and release certification. Installation, `doctor` exit 0, model-availability probes and source tests are not substitutes for real capability invocations. Beta requires the [bounded exit criteria](../ROADMAP.md#beta1-exit-criteria); full parity still requires the architecture's evidence below.

## Evaluation-alpha evidence scope

The [architecture release contract](architecture/overview.md#build-parity-and-release-gates) permits an uncertified evaluation-alpha prerelease with a declared, bounded verification matrix. It does not certify the full eleven-family by three-runtime target. This section defines how to record that evidence; it does not assert that a new alpha has passed or been published, and it does not retroactively add evidence to alpha20.

Declare the matrix before verification. For every claimed supported platform/runtime-version/capability combination, record structural conformance, isolated install and readback, and at least one real capability invocation. Bind each result to the source revision and archive/binary identity, with its command, exit status and bounded redacted outcome. Keep credentials, private prompts, user configuration and host-identifying paths out of the evidence.

List other combinations explicitly as experimental or excluded, including empty families, and record absent-runtime, unknown-version and known-incompatible-adapter results. Unknown versions remain no-write; incompatibility skips only the affected adapter. Missing or failed required evidence blocks publication for the declared matrix. Preserve failures if the matrix is revised, declare the revised scope, and complete its gates before publication.

The existing full-matrix smoke planner remains a certification planner, not an executor for this bounded matrix. Installation, availability probes and source tests do not replace real capability invocations. Release notes must distinguish verified evaluation scope from excluded scope and must not describe either as full parity or certification.

## Historical runtime admission evidence

This predates alpha20 and does not certify its runtime versions. The following isolated marker installation, readback, acknowledgement, and cleanup evidence succeeded with zero retries, exit 0, and no timeouts or overflows. Marker `98372fc8807c2965cb1062664614ea8c1773f240bca7fb97c2d1dcb78b9fe3f6`; snapshot `6f08ee25dc84c7cba2be78deab7eeaca8585d5fa1528795a9256e642854fac88`.

| Runtime | Exact version | Evidence records | Duration |
| --- | --- | --- | --- |
| Pi | 0.85.1 | source `b3364d923bf20abc242c7bd4cae25ec07731bc36`; command `71a9ab3f17ac06a26c31e61eafdeb7131ab2dfdf2ce9735dd9cc6a0b97349e75` | 9345 ms |
| OpenCode | 1.18.25 | source `07f8979cf4969f0bb977d401e3792bd5d754e963`; command `6062b37fc943e60d1828a27ed69ed738b8572b1a27bacd068f8a679de19f2f8a`; config `61e3b70f80acc12049f71307760e500778f288b30dc0ee9d42c3cb43b091f3e0`; `skill_tool_completed=true` | 5000 ms |
| Claude Code | 2.1.251 | source `f68758e784287cff78599c24db8632a13890c206`; command `0cba56fe517827ac8169a49cd36291d13875d06883be4729654fa34183aa8133`; schema `f7374878f385a402aa47362c62c882bdafba7abafac4d266ab81047448a5d46b`; auth `subscription_oauth_token` | 2342 ms |

This is verified-version evidence, not full 11×3 runtime-family parity and not release certification. A future parity claim requires structural conformance, isolated installation and readback, and at least one real smoke invocation for every family and runtime: 11 families across 3 runtimes, for at least 33 invocations. Release evidence must also cover absent runtimes, unidentified versions, and known-incompatible adapters. A bounded evaluation-alpha matrix does not satisfy this full-certification requirement.

## Reporting results

Share the platform, archive filename and SHA-256, backend/version, QA role, exit status, and bounded redacted output. Do not share credentials, private prompts, user configuration, or host-identifying paths.

## Content licensing

Catalog content is admitted only with explicit licensing, provenance, and redistribution permission. Cortex-owned knowledge and capability content uses CC BY-SA; third-party material stays outside the catalog until compatible redistribution rights are proven.

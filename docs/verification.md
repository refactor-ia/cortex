# Verification and release evidence

This page carries the audit material referenced from the [root README](../README.md): how to verify a downloaded archive, what runtime admission was tested with what evidence, and how to report results. It records what was tested; it does not gate what you may install.

## Verify an archive

Each release archive contains `cortex`, `LICENSE`, `BUILDINFO.json` (source commit, tree, Go version, target, and binary SHA-256), and the matching `catalog/` tree. Verify before extracting; a checksum failure must stop extraction.

```bash
archive=cortex_v0.1.0-alpha.8_darwin_arm64.tar.gz
curl -fL -O "https://github.com/refactor-ia/cortex/releases/download/v0.1.0-alpha.8/$archive"
curl -fL -O https://github.com/refactor-ia/cortex/releases/download/v0.1.0-alpha.8/SHA256SUMS
shasum -a 256 -c --ignore-missing SHA256SUMS && tar xzf "$archive"
```

On Linux, set `archive=cortex_v0.1.0-alpha.8_linux_amd64.tar.gz` and use `sha256sum -c --ignore-missing SHA256SUMS` instead of the macOS checksum line. macOS arm64 and Linux amd64 are the published targets; Windows is not supported and does not currently build.

### Building from source

```bash
git clone https://github.com/refactor-ia/cortex.git
cd cortex
go build -o cortex ./cmd/cortex
```

For a release-style source build, inject both identity fields into the binary (substitute the intended version and build commit):

```bash
go build -ldflags="-X github.com/refactor-ia/cortex/internal/cli.buildVersion=v0.1.0-alpha.8 -X github.com/refactor-ia/cortex/internal/cli.buildRevision=0123456789abcdef" -o cortex ./cmd/cortex
./cortex --version
```

`--version` reports `version`, `revision`, `build_dirty`, and `embedded_catalog` from the **running binary**. Each injected field overrides only its corresponding Go build-info field; without injection, Go module version and `vcs.revision` are used when embedded. Missing fields and the Go `(devel)` version report `unavailable`. `build_dirty` reflects Go's embedded `vcs.modified` setting (`true`, `false`, or `unavailable`), even with explicit release fields; it does not certify the injected revision. The catalog ID is resolved from the admitted embedded snapshot. A catalog in a source cache, checkout, or adjacent directory is not evidence about the active binary, and `--version` never reads one.

## Runtime admission evidence

The following isolated marker installation, readback, acknowledgement, and cleanup evidence succeeded with zero retries, exit 0, and no timeouts or overflows. Marker `98372fc8807c2965cb1062664614ea8c1773f240bca7fb97c2d1dcb78b9fe3f6`; snapshot `6f08ee25dc84c7cba2be78deab7eeaca8585d5fa1528795a9256e642854fac88`.

| Runtime | Exact version | Evidence records | Duration |
| --- | --- | --- | --- |
| Pi | 0.85.1 | source `b3364d923bf20abc242c7bd4cae25ec07731bc36`; command `71a9ab3f17ac06a26c31e61eafdeb7131ab2dfdf2ce9735dd9cc6a0b97349e75` | 9345 ms |
| OpenCode | 1.18.25 | source `07f8979cf4969f0bb977d401e3792bd5d754e963`; command `6062b37fc943e60d1828a27ed69ed738b8572b1a27bacd068f8a679de19f2f8a`; config `61e3b70f80acc12049f71307760e500778f288b30dc0ee9d42c3cb43b091f3e0`; `skill_tool_completed=true` | 5000 ms |
| Claude Code | 2.1.251 | source `f68758e784287cff78599c24db8632a13890c206`; command `0cba56fe517827ac8169a49cd36291d13875d06883be4729654fa34183aa8133`; schema `f7374878f385a402aa47362c62c882bdafba7abafac4d266ab81047448a5d46b`; auth `subscription_oauth_token` | 2342 ms |

This is verified-version evidence, not full 11×3 runtime-family parity and not release certification. A future parity claim requires structural conformance, isolated installation and readback, and at least one real smoke invocation for every family and runtime: 11 families across 3 runtimes, for at least 33 invocations. Release evidence must also cover absent runtimes, unidentified versions, and known-incompatible adapters.

## Reporting results

Share the platform, archive filename and SHA-256, backend/version, QA role, exit status, and bounded redacted output. Do not share credentials, private prompts, user configuration, or host-identifying paths.

## Content licensing

Catalog content is admitted only with explicit licensing, provenance, and redistribution permission. Cortex-owned knowledge and capability content uses CC BY-SA; third-party material stays outside the catalog until compatible redistribution rights are proven.

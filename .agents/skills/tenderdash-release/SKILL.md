---
name: tenderdash-release
description: Analyze or prepare a Tenderdash release for Dash Platform, choosing SemVer from actual history and compatibility changes; publish only when the user explicitly requests a release.
---

# Tenderdash release

Tenderdash is a Dash Platform component. Read the repository's `AGENTS.md`,
`RELEASES.md`, and the current PR template. This skill is the release workflow;
do not substitute a generic version-bump skill or infer versions from branch names.

An analysis, preparation, or release-PR request authorizes that scope only, not
tags, GitHub releases, image publication, or deployment. An explicit release
request may authorize those steps; honor its limits and existing approvals
without asking again. Never change protections, move an existing release tag,
or publish embargoed fixes without the coordinated disclosure authorization.

## Establish the release context

Resolve the intended Dash Platform version/network and operation: next release,
prerelease, or stable hotfix. Inspect live refs, published tags/releases, and open
release PRs before selecting commits. Fetch the necessary refs/tags; do not trust
stale local tracking refs or assume the GitHub default is the release source.

- **Next release:** use `origin/dev`, or explicitly `origin/v1.8-dev` during the
  planned rename. Record its exact SHA. Freeze that candidate; a later tip is a
  new candidate requiring review. The helper accepts that reviewed ancestor of
  the fetched integration tip without substituting newer commits. A temporary
  release branch can isolate a freeze.
- **Current-stable hotfix:** start from `origin/master` and its latest published
  tag. Isolate only required fixes in a feature branch; exclude next-version
  development. Plan the forward-port to dev as part of completion.
- **Another deployed Platform line:** require an explicit supported-deployment
  decision and start from that line's last released tag, not master or dev.
  This is an exceptional manual path: the helper intentionally validates only
  next releases and current-stable hotfixes. Do not weaken it to admit another line.

Choose the latest **published stable tag reachable from the candidate** for the
compatibility baseline. Verify its commit and GitHub release are the intended
Platform lineage. If a newer stable release exists elsewhere, reconcile why it
is absent instead of using an older baseline silently. For a prerelease series,
also compare the preceding published prerelease to identify incremental changes;
the stable baseline still defines the aggregate compatibility change.

From the repository root, collect committed-tree evidence with:

```sh
python3 scripts/release/plan.py --mode next --source SOURCE_SHA --baseline vX.Y.Z > /absolute/artifact/path/release-evidence.json
# For an isolated stable fix candidate, use --mode hotfix instead.
```

Write evidence outside the checkout. The helper reads local Git state only; it
neither verifies remote publication nor chooses a version. Its preparation base
is not the eventual full-release PR target. Read its commits, complete net diff,
and stable patch-equivalence result; large diffs may be reviewed by affected
subsystem, but the version decision must cover the entire candidate.

## Decide compatibility and SemVer

Review code, tests, docs, and release history rather than trusting conventional
commit labels or counting `feat`/`fix` subjects. Inspect both individual changes
and the net baseline-to-candidate diff. A reverted feature absent from the net
tree is not a new feature; a patch-equivalent backport may already be released.
Explain cherry-picks, merges, existing release bumps, and partial reverts rather
than counting them twice. Pure release metadata is not a new compatibility change.

Assess these contracts explicitly:

| Contract | Evidence and decision |
| --- | --- |
| Exported non-internal Go API and RPC | Removed/changed types, methods, fields, response semantics, caller migration |
| ABCI | Application methods, messages, return handling and app coordination; review `ABCISemVer` separately |
| P2P and consensus | Message/channel formats, proposer selection, peer compatibility; review `P2PProtocol` |
| Blocks, votes, commits, state validity | Serialization/hash/signing/validity changes and persisted data; review `BlockProtocol` |
| Configuration, CLI, storage | Previously supported settings or workflows rejected, defaults changed, database migration/rollback |
| Dash Platform integration | Required Drive/ABCI/Dashmate updates, network activation, mixed-version operation, operator rollout |

For the current 1.x-and-later software contract: incompatible supported behavior
requires a major; compatible new functionality requires a minor; compatible fixes
require a patch. A security or consensus fix is not automatically patch-safe.
Tightened validation that breaks supported configuration or application behavior
must be called out and classified from the actual contract. Internal-only work
may need no release; do not manufacture a bump for an analysis request.

Protocol counters and `ABCISemVer` are independent of `TMVersionDefault`: explain
whether each must change, and why. Do not mechanically increment every counter,
or use a software major to conceal an unhandled network compatibility change.
If the diff contains unresolved compatibility questions, report them and defer
publication until the Platform/release owner resolves them.

Select the next unused `X.Y.Z` or `X.Y.Z-dev.N` consistent with the analysis and
published history. A continuation of a prerelease series increments its suffix;
if its compatibility scope changes, reconsider the core version. Promotion of an
unchanged prerelease uses its reviewed core version. Never reuse or retarget a
published version. Record baseline/source SHAs, rationale, affected contracts,
Platform rollout, and required tests in the release plan.

## Prepare the reviewable release

Work in an isolated feature/release checkout at the reviewed source SHA. Preserve
unrelated work. Update `TMVersionDefault` in `version/version.go` to the chosen
version, and only the protocol versions justified above. Prepare `CHANGELOG.md`
from the reviewed changes, preserving existing release sections. Include the
version/date, meaningful user-facing changes, breaking behavior and migration;
exclude already-released patches and reverted effects. Commit prefixes can help
organize the notes but cannot replace diff review. Do not regenerate old history
or rewrite an existing release section.

Run formatting, relevant tests and lint, and the applicable Platform integration
and network-upgrade checks. Record exact commands, results, candidate tree, and
anything not verified. Signed binary building does not prove consensus or upgrade
compatibility. Fill every applicable repository PR-template section with the
compatibility rationale and validation, including AI attribution.

A prerelease preparation PR targets the integration branch; a full-release PR
promotes the frozen candidate to master and preserves ancestry under the agreed
release merge strategy. A stable patch PR targets master. Respect protections:
master's current linear-history rule conflicts with the documented promotion
merge, so publication must wait for maintainers to reconcile that policy; never
use an automatic administrative bypass. Stop after the requested preparation or
PR phase if no release publication was requested.

## Finalize an explicitly requested release

Verify the release PR is merged, identify its exact merge SHA, and inspect the
resulting tree/version/changelog against the reviewed candidate. Include any
merge-time differences in validation. Do not use a moving `master`/`dev` tip as
the tag target. Recheck remote tags and release drafts before acting.

For a new version, create the release tag explicitly at that reviewed merge SHA
(sign it when the project's release signing key is available), and push only
that tag using the authorized release workflow. If the tag already exists,
compare its peeled commit to the reviewed SHA and reuse it only on an exact
match; a mismatch stops the release. Never force-update or delete a release tag.
Create a draft GitHub release with `gh release create TAG --verify-tag --draft
--notes-file NOTES` (add `--prerelease` for `-dev.N`). Existing drafts must match
the same tag/SHA; inspect and reuse rather than deleting/recreating them.

When signed binaries are required, run from a checkout with that exact tag:

```sh
bash scripts/release/build-artifacts.sh REPO TAG REVIEWED_MERGE_SHA OUTPUT_DIRECTORY
```

The helper builds Linux amd64/arm64, signs each binary with GPG (optional
`GPG_KEY_ID`), verifies signatures, and produces tarballs, `SHA256SUMS`, and
`SOURCE_COMMIT`. It clones the reviewed source, never changes the caller's branch,
and never uploads. Use a fresh output directory under the user's artifact root.
Retain provenance and verify checksums/signatures before uploading to the matching
draft. Compare existing assets rather than overwriting them with `--clobber`.
For unsigned/dev releases, state explicitly which binary artifacts are omitted;
Docker publication remains separate.

Publishing the draft triggers `.github/workflows/docker.yml`. Check that the
Platform rollout and release request authorize this publication. Stable releases
currently promote Docker `latest` and the major alias: an older-line security
release must not run that promotion without a separately approved safe path.
After publication, verify the remote tag SHA, release assets, Docker image tags
and digests. If asynchronous publishing fails, report the partial state and resume
only the missing step; do not move tags or repeat a completed release blindly.

Forward-port stable fixes and release metadata to dev with a reviewed merge or
focused equivalent changes, preserving next-version metadata and resolving
changelog conflicts deliberately. Report release/tag SHA, version decision,
artifact links/provenance, Platform coordination, and any unfinished rollout.

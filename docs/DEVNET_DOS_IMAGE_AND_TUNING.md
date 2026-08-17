# Devnet: custom Tenderdash DoS-hardened image + config-knob plumbing

Scope: how to build/publish a `dashpay/tenderdash:<tag>` image from the
consensus-DoS-hardening branch and point a devnet at it, plus the exact
change-set to plumb the three new `[consensus]` DoS knobs through
dashmate → dash-network-deploy → dash-network-configs **if/when infra wants to
tune them**.

Repos referenced:
- Tenderdash: https://github.com/dashpay/tenderdash (branch `fix/consensus-vote-flood`)
- Platform / dashmate: https://github.com/dashpay/platform (`packages/dashmate`, inspected at `v4.2-dev`)
- Deploy tooling: https://github.com/dashpay/dash-network-deploy (inspected at `v1.0-dev`)
- Network configs: https://github.com/dashpay/dash-network-configs (inspected at `master`)

---

## 0. TL;DR — is any plumbing needed for a first devnet? **No.**

**An absent key falls back to Tenderdash's compiled-in default. A first devnet
needs ZERO cross-repo changes; it runs the shipped defaults.** The plumbing in
Part B is only for *overriding* those defaults (limit sweeps).

Why this is certain (three independent confirmations):

1. **Load path.** `cmd/tenderdash/main.go` does
   `commands.ParseConfig(config.DefaultConfig())` — the generated `config.toml`
   is unmarshalled *on top of* the default struct. Any key missing from the TOML
   keeps its compiled-in value. Defaults (from `config/config.go`
   `DefaultConsensusConfig()`):
   - `verification-rate-limit` = **300**
   - `peer-vote-rate-limit` = **600**
   - `peer-data-rate-limit` = **500**

2. **dashmate template.** `packages/dashmate/templates/platform/drive/tenderdash/config.toml.dot`
   emits a `[consensus]` section that stops at `peer-query-maj23-sleep-duration`
   — it does **not** emit any of the three rate-limit keys, and does not carry a
   stale `peer-vote-rate-limit = 100`. So dashmate-managed validators get the
   built-in defaults.

3. **Seed-node template.** `dash-network-deploy` ships a separate seed-node
   Tenderdash config at
   `ansible/roles/tenderdash/templates/tenderdash/config.toml.j2`; its
   `[consensus]` block also stops at `peer-query-maj23-sleep-duration` and
   carries no stale value. Seed nodes get the built-in defaults too.

So every node type on a devnet built from this image runs `300 / 600 / 500` with
no config work. Part B is off the critical path.

---

# Part A — Building & publishing the image (the headline)

## A.1 Current branch/credential reality (important)

- The branch `fix/consensus-vote-flood` is on the **`lklimek/tenderdash` fork**.
  It is **NOT** on `dashpay/tenderdash` yet (`GET /repos/dashpay/tenderdash/branches/fix/consensus-vote-flood` → **404**).
- `dashpay/tenderdash` **does** hold the Docker Hub push credentials as repo
  secrets: `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` are present.
- The publish workflow is `.github/workflows/docker.yml` (name: "Docker"),
  `workflow_dispatch` with inputs `tag` (string) and `platforms` (choice:
  `linux/amd64,linux/arm64` | `linux/amd64` | `linux/arm64`). Its
  `docker/metadata-action` hard-codes `images: dashpay/tenderdash`, so it always
  pushes into the `dashpay/tenderdash` namespace. BLS/CGO is built inside
  `DOCKER/Dockerfile` (multi-stage; no host toolchain needed).

**Consequence:** you cannot produce a `dashpay/tenderdash:<tag>` image via the
CI workflow without the branch (and the workflow file) living on
`dashpay/tenderdash`, because `workflow_dispatch --ref <branch>` runs the
workflow from that ref *in that repo*, and the Docker Hub secrets only exist on
`dashpay/tenderdash`.

## A.2 Path 1 — CI workflow on `dashpay/tenderdash` (Docker Hub publish)

Prerequisite: push the branch to `dashpay/tenderdash` (requires push rights):

```bash
git push git@github.com:dashpay/tenderdash.git fix/consensus-vote-flood
```

`workflow_dispatch` **can** target a non-default branch — pass `--ref`. The
workflow file must exist on that ref (it does on this branch):

```bash
gh workflow run docker.yml \
  --repo dashpay/tenderdash \
  --ref fix/consensus-vote-flood \
  -f tag=1.6.1-dos-devnet1 \
  -f platforms=linux/amd64
```

Result: `dashpay/tenderdash:1.6.1-dos-devnet1` pushed to Docker Hub (multi-arch
if you pass the default `platforms`). Operator needs: **push + Actions-run
permission on `dashpay/tenderdash`** (Docker Hub creds are already repo secrets).

Caveat on the embedded version string for this path: `actions/checkout@v7`
checks out a **detached HEAD**, so the Makefile's
`git symbolic-ref -q --short HEAD` returns empty and `VERSION` falls to the
`git describe --tags` branch of the Makefile — the CI image will **not** carry a
clean `unreleased-fix/consensus-vote-flood-<sha>` string. (The local build in
Path 2 does — see A.4.)

## A.3 Path 2 — local `make build-docker` (tarball or self-push)

The repo Makefile target (`Makefile` → `build-docker`) builds the full BLS/CGO
image from `DOCKER/Dockerfile` and tags it `dashpay/tenderdash:latest`
(single-arch, `--load` into the local daemon). To get a distinct tag:

```bash
git checkout fix/consensus-vote-flood
docker buildx build --load \
  --cache-from=type=registry,ref=dashpay/tenderdash:buildcache-deps \
  -t dashpay/tenderdash:1.6.1-dos-devnet1 \
  -f DOCKER/Dockerfile .
```

Note: this is **single-platform (host arch)**. The devnet runs on AWS
(`linux/amd64`); on an Apple-silicon Mac add `--platform linux/amd64` (a
`--load` buildx build supports one platform at a time).

Hand-off options:
- Push to Docker Hub yourself (needs `docker login` for the `dashpay` org):
  `docker push dashpay/tenderdash:1.6.1-dos-devnet1`
- Or give infra a tarball (no registry creds needed):
  ```bash
  docker save dashpay/tenderdash:1.6.1-dos-devnet1 | gzip > tenderdash-dos-devnet1.tar.gz
  # infra side: gunzip -c tenderdash-dos-devnet1.tar.gz | docker load
  ```

## A.4 Recommendation

For a **one-off custom devnet image**, **Path 2 (local build) is cleaner**:
- no need to push a work-in-progress DoS branch onto the canonical
  `dashpay/tenderdash` repo just to build,
- no experimental tag pushed into the public `dashpay/tenderdash` Docker Hub
  namespace,
- it yields the self-identifying version string
  `unreleased-fix/consensus-vote-flood-<full-40-char-sha>` (Makefile `VERSION`
  uses `git symbolic-ref` + `git rev-parse HEAD`; `.git` is `COPY`'d into the
  build and there is no `.dockerignore`, so on a branch checkout the branch name
  is embedded).

Use Path 1 only if you specifically want the image on Docker Hub under
`dashpay/tenderdash` and are comfortable pushing the branch there.

## A.5 What to tell the infra team about this image

- **Image:** `dashpay/tenderdash:1.6.1-dos-devnet1` (pick your own tag), built
  from branch `fix/consensus-vote-flood`.
- **Point the devnet at it** via the network YAML key `tendermint_image` — it
  controls **both** seed nodes and dashmate validators:
  `tendermint_image: dashpay/tenderdash:1.6.1-dos-devnet1`.
- **Verifying it's live:** it's a branch build, so (Path 2) the Tenderdash RPC
  `/status` → `node_info.version` reads
  `unreleased-fix/consensus-vote-flood-<sha>`. (Path 1 / CI build shows a
  `git describe` version instead — see A.2.)
- **Baked-in defaults:** `verification-rate-limit=300`, `peer-vote-rate-limit=600`,
  `peer-data-rate-limit=500`. A devnet with no config changes runs these.
- **Two config traps (from `UPGRADING.md`) — but they bite hand-written
  `config.toml` ONLY:** dashmate regenerates `config.toml` (and the seed-node
  template omits these keys), so **managed devnet nodes are unaffected**. For
  awareness:
  1. `verification-rate-limit` now has a floor: it must be **`0` or ≥ 33**
     (`1 + MaxVoteExtensions`). A value between 1 and 32 fails validation and the
     **node won't start**.
  2. `peer-vote-rate-limit` **changed units and default**: it now counts
     verification-work/sec (default **600**), not messages/sec (old default
     100). A stale `= 100` isn't rejected — it silently throttles vote gossip
     (missed votes / slower rounds), never a disconnect.
- **Metrics:** Tenderdash Prometheus is exposed under namespace prefix
  **`drive_tenderdash`** on port **`:36660`** (dashmate ansible default
  `dashmate_platform_tenderdash_metrics_port: 36660`). One gauge was removed
  (`consensus_vote_gate_fail_open`); several were added.

---

# Part B — Plumbing the three knobs (only needed to OVERRIDE the defaults)

Apply in dependency order: dashmate (A) → dash-network-deploy (B) →
dash-network-configs (C). Each lower layer references the one above.

Naming (established): TOML `verification-rate-limit` ⇄ dashmate camelCase
`verificationRateLimit` ⇄ ansible snake `..._verification_rate_limit`. Same for
the other two. Grep confirms **none of these keys exist anywhere in
`dashpay/platform` today** (0 hits for all six spellings), so every snippet
below is a pure add. Target dashmate package version is **4.1.0** (current on
`v4.2-dev`); ship the migration keyed at the *next* release.

## B.1 dashmate (`dashpay/platform`, `packages/dashmate`) — 4 files

### (1) Config template — `templates/platform/drive/tenderdash/config.toml.dot`

Rate limits are floats → emit as **bare numbers** (no quotes). Add three lines
right after `peer-query-maj23-sleep-duration`:

```diff
 # Reactor sleep duration parameters
 peer-gossip-sleep-duration = "{{= it.platform.drive.tenderdash.consensus.peer.gossipSleepDuration }}"
 peer-query-maj23-sleep-duration = "{{= it.platform.drive.tenderdash.consensus.peer.queryMaj23SleepDuration }}"

+# Per-peer vote-channel budget (verification-work/sec). 0 disables.
+peer-vote-rate-limit = {{= it.platform.drive.tenderdash.consensus.peerVoteRateLimit }}
+# Node-wide BLS verification budget (ops/sec). Must be 0 or >= 33.
+verification-rate-limit = {{= it.platform.drive.tenderdash.consensus.verificationRateLimit }}
+# Per-peer data-channel budget (verification-work/sec). 0 disables.
+peer-data-rate-limit = {{= it.platform.drive.tenderdash.consensus.peerDataRateLimit }}
+
 ### Unsafe Timeout Overrides ###
```

### (2) Default config — `configs/defaults/getBaseConfigFactory.js`

Add the three keys to the `consensus` object (siblings of `peer`):

```diff
             consensus: {
               createEmptyBlocks: true,
               createEmptyBlocksInterval: '3m',
               peer: {
                 gossipSleepDuration: '100ms',
                 queryMaj23SleepDuration: '2s',
               },
+              verificationRateLimit: 300,
+              peerVoteRateLimit: 600,
+              peerDataRateLimit: 500,
               unsafeOverride: {
```

### (3) JSON schema — `src/config/configJsonSchema.js`

Under `...drive.properties.consensus.properties`, add three number properties and
extend the `required` array (the object is `additionalProperties: false`, so both
edits are mandatory — otherwise a config carrying the keys fails validation):

```diff
                 consensus: {
                   type: 'object',
                   properties: {
                     createEmptyBlocks: {
                       type: 'boolean',
                     },
                     createEmptyBlocksInterval: {
                       $ref: '#/definitions/duration',
                     },
                     peer: {
                       type: 'object',
                       properties: {
                         gossipSleepDuration: { $ref: '#/definitions/duration' },
                         queryMaj23SleepDuration: { $ref: '#/definitions/duration' },
                       },
                       additionalProperties: false,
                       required: ['gossipSleepDuration', 'queryMaj23SleepDuration'],
                     },
+                    verificationRateLimit: {
+                      type: 'number',
+                      minimum: 0,
+                    },
+                    peerVoteRateLimit: {
+                      type: 'number',
+                      minimum: 0,
+                    },
+                    peerDataRateLimit: {
+                      type: 'number',
+                      minimum: 0,
+                    },
                     unsafeOverride: {
                       /* ...unchanged... */
                     },
                   },
                   additionalProperties: false,
-                  required: ['createEmptyBlocks', 'createEmptyBlocksInterval', 'peer', 'unsafeOverride'],
+                  required: ['createEmptyBlocks', 'createEmptyBlocksInterval', 'peer', 'unsafeOverride', 'verificationRateLimit', 'peerVoteRateLimit', 'peerDataRateLimit'],
                 },
```

Note: the schema only enforces `minimum: 0`; the "0 or ≥ 33" rule for
`verificationRateLimit` is enforced by Tenderdash's own `ValidateBasic` at node
start. (You *could* encode it in schema with an `anyOf: [{const: 0}, {minimum: 33}]`,
but leaving it to Tenderdash keeps the schema simple and is consistent with how
dashmate treats the other Tenderdash-validated numbers.)

### (4) Migration — `configs/getConfigFileMigrationsFactory.js`  ← EASY TO MISS

Because the schema now **requires** these keys, every *existing* config must gain
them or it fails validation after upgrade. Add a version-keyed migration that
backfills the shipped defaults. Mirror the existing `typeof ... === 'undefined'`
+ `defaultConfig.getStored(...)` pattern (`getDefaultConfigByNameOrGroup` is
already in scope). Key it at the dashmate release that ships this change (shown
as `4.2.0` — replace with the real version):

```js
      '4.2.0': (configFile) => {
        // Consensus DoS-hardening knobs (Tenderdash 1.6.1). Backfill the shipped
        // defaults so a regenerated config.toml carries the new [consensus]
        // rate limits. Keyed one release ahead: the runner skips
        // fromVersion === toVersion, so a key equal to an operator's current
        // version never fires.
        Object.entries(configFile.configs)
          .forEach(([name, options]) => {
            const defaultConfig = getDefaultConfigByNameOrGroup(name, options.group);
            const consensus = options.platform?.drive?.tenderdash?.consensus;
            if (!consensus) {
              return;
            }
            if (typeof consensus.verificationRateLimit === 'undefined') {
              consensus.verificationRateLimit = defaultConfig.getStored('platform.drive.tenderdash.consensus.verificationRateLimit');
            }
            if (typeof consensus.peerVoteRateLimit === 'undefined') {
              consensus.peerVoteRateLimit = defaultConfig.getStored('platform.drive.tenderdash.consensus.peerVoteRateLimit');
            }
            if (typeof consensus.peerDataRateLimit === 'undefined') {
              consensus.peerDataRateLimit = defaultConfig.getStored('platform.drive.tenderdash.consensus.peerDataRateLimit');
            }
          });

        return configFile;
      },
```

## B.2 dash-network-deploy (`dashpay/dash-network-deploy`) — 2 files

### (5) Ansible vars — `ansible/roles/dashmate/defaults/main.yml`

Add next to the existing consensus vars (after
`..._consensus_peer_query_maj23_sleep_duration`):

```diff
 dashmate_platform_drive_tenderdash_consensus_peer_gossip_sleep_duration: "100ms"
 dashmate_platform_drive_tenderdash_consensus_peer_query_maj23_sleep_duration: "2s"
+dashmate_platform_drive_tenderdash_consensus_verification_rate_limit: 300
+dashmate_platform_drive_tenderdash_consensus_peer_vote_rate_limit: 600
+dashmate_platform_drive_tenderdash_consensus_peer_data_rate_limit: 500
```

### (6) dashmate config template — `ansible/roles/dashmate/templates/dashmate.json.j2`

This repo drives dashmate by **writing `dashmate.json` from this Jinja template**
(not via `dashmate config set`). Inject the three vars into the `consensus`
object, right after the `peer` block and before `unsafeOverride`:

```diff
             "consensus": {
               "createEmptyBlocks": true,
               "createEmptyBlocksInterval": "3m",
               "peer": {
                 "gossipSleepDuration": "{{dashmate_platform_drive_tenderdash_consensus_peer_gossip_sleep_duration}}",
                 "queryMaj23SleepDuration": "{{dashmate_platform_drive_tenderdash_consensus_peer_query_maj23_sleep_duration}}"
               },
+              "verificationRateLimit": {{dashmate_platform_drive_tenderdash_consensus_verification_rate_limit}},
+              "peerVoteRateLimit": {{dashmate_platform_drive_tenderdash_consensus_peer_vote_rate_limit}},
+              "peerDataRateLimit": {{dashmate_platform_drive_tenderdash_consensus_peer_data_rate_limit}},
               "unsafeOverride": {
```

**Version coupling (must ship together):** the `dashmate.json` this template
writes is validated by the *installed* dashmate against its schema, and
`consensus` is `additionalProperties: false`. So the `dashmate_version` pinned in
the network YAML must be a build that contains B.1 (the new schema keys).
Emitting these keys against an older dashmate → validation error; a newer
dashmate whose schema *requires* them while the template omits them → also an
error (unless the migration backfills first). Bump `dashmate_version` and land
B.1 + B.2 in lockstep.

## B.3 dash-network-configs (`dashpay/dash-network-configs`) — per-network YAML

Per-network files are flat ansible-var overrides (see `testnet.yml`, which already
sets e.g. `tendermint_image:` and `dashmate_platform_drive_tenderdash_mempool_size:`).
To override the limits for one network (and point it at the custom image), add to
that network's YAML (top-level, alongside the other
`dashmate_platform_drive_tenderdash_*` keys). Example limit-sweep block:

```yaml
# custom DoS-hardened Tenderdash for this devnet
tendermint_image: dashpay/tenderdash:1.6.1-dos-devnet1

# consensus DoS knobs (override the 300/600/500 defaults)
dashmate_platform_drive_tenderdash_consensus_verification_rate_limit: 0     # 0 = disabled, else >= 33
dashmate_platform_drive_tenderdash_consensus_peer_vote_rate_limit: 1200
dashmate_platform_drive_tenderdash_consensus_peer_data_rate_limit: 1000
```

For a devnet specifically, put this in the devnet's config set / branch YAML
(the repo has `devnet-*` branches, e.g. `devnet-two-islands`,
`devnet-testnet-merge`).

**Dependency:** B.3 only takes effect once B.2 (the template references the var)
and B.1 (dashmate knows the key) are in place — the Jinja template dereferences
`dashmate_platform_drive_tenderdash_consensus_*`, so the var must at minimum
exist in B.2's defaults. Setting it only here without B.1/B.2 does nothing (or
errors on an undefined Jinja var).

---

## Version-coupling summary

| Layer | Repo | Depends on |
|---|---|---|
| A. dashmate template + schema + defaults + migration | dashpay/platform (dashmate ≥ the release shipping B.1) | Tenderdash image that accepts the keys (any 1.6.1+ / this branch) |
| B. ansible defaults + dashmate.json.j2 | dashpay/dash-network-deploy | `dashmate_version` pinned to a build containing A |
| C. per-network override | dashpay/dash-network-configs | B (var referenced) + A (key known/validated) |

For a **first devnet**, none of A/B/C is required — ship the image, set
`tendermint_image`, done (Part A.5). A/B/C are the path for tuning the limits
later.

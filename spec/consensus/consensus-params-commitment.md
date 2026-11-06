# Full Consensus Parameter Commitment

**DRAFT.** This is a normative design and source schema proposal,
not implemented consensus behavior. The schema has generated bindings; runtime
support and activation are deliberately deferred. Only the application-version
removal is partly implemented; see
[application version](#application-version-is-not-a-consensus-parameter).
MUST, MUST NOT and SHOULD describe the target implementation.

In this document FULL means effective `Version.ConsensusVersion >= 2`, and
LEGACY means a lower consensus version.

## Acceptance scenarios

| Scenario | Required result |
| --- | --- |
| Change any effective parameter, including evidence limits, timeout or threshold presence | The full commitment changes; a substituted snapshot fails authentication. |
| Equivalent complete state reached through different partial updates | Identical canonical bytes and hash; raw ABCI patches are never hashed. |
| Omit the version group in a partial update | Retain the current consensus version. Any update lowering it is rejected. |
| Raise the consensus version to 2 at height H | H-1 signs the full next-parameter hash; H uses the full current-parameter hash. |
| Attempt activation with incomplete groups or duration fields | Reject before accepting a candidate; unauthenticated legacy values cannot fill gaps. |
| Bootstrap from FULL history with a changed threshold | Authenticate each required height's parameters and validator context; do not pin a legitimate mutable threshold to genesis. |
| Replay or restart across activation | Hash each height's parameters by their own consensus version; produce the original block IDs and verify the original signatures. |
| Receive unknown consensus versions, unsupported parameter fields or noncanonical snapshot bytes | Fail closed; never silently downgrade or omit a new parameter. |

## Existing commitment and threat boundary

At this proposal's base, `types/params.go:HashConsensusParams` hashes only
`Block.MaxBytes`, `Block.MaxGas` and `Version.ConsensusVersion` through the existing
`HashedParams` message. Evidence limits, validator key types and voting threshold,
synchrony bounds, timeouts and ABCI rechecking are outside that digest. The
application version leaves consensus parameters entirely; see
[application version](#application-version-is-not-a-consensus-parameter).
Actual evidence proofs already contribute to `EvidenceHash`; this proposal adds
their configurable limits, not a second commitment to the proofs.

`Header.Hash` already includes `ConsensusHash` and `NextConsensusHash`. Its result
enters `BlockID.Hash`, the canonical block-ID digest, `CanonicalVote` and the
quorum-bound BLS signing digest. `Vote.SignBytes` in
[`proto/tendermint/types/types.go`](../../proto/tendermint/types/types.go) uses
[`libs/bytes/marshal.go`](../../libs/bytes/marshal.go)'s fixed-size field encoding,
not ordinary Protobuf wire encoding of the vote. Authenticating both parameter
hashes therefore needs no new signature field and no new `StateID` field. Preserve
that existing vote layout, fixed-width height/round encoding and signing domains. See
[signing](signing.md), [block headers](../core/data_structures.md#header),
[`types/block.go`](../../types/block.go),
[`types/canonical.go`](../../types/canonical.go) and
[`types/quorum_sign_data.go`](../../types/quorum_sign_data.go).

A threshold BLS signature proves recovery under the quorum key. It does not prove
that a raised application voting-power threshold was met by enough distinct
signers. Adding signer-count attestation for that separate issue is out of scope.

## Impact of forged parameters today

A node bootstrapped by state sync takes `ConsensusParams` from its state
provider: the configured primary RPC server
([`internal/statesync/stateprovider.go:207`](../../internal/statesync/stateprovider.go))
or, with P2P state sync, its peers (`stateprovider.go:355`).
`verifyConsensusParams` (`stateprovider.go:509`) checks only `Block.MaxBytes`,
`Block.MaxGas` and `Version.ConsensusVersion` against the signed `ConsensusHash`,
and pins `Validator.VotingPowerThreshold` to the locally trusted genesis/InitChain
value (`stateprovider.go:522`,
[`internal/statesync/reactor.go:280`](../../internal/statesync/reactor.go)).
Every other value is taken on trust.

| Parameter | Forged lower or narrower | Forged higher or wider |
| --- | --- | --- |
| `evidence.max_age_*` (both) | Rejects valid evidence ([`internal/evidence/verify.go:53`](../../internal/evidence/verify.go)); halts on a committed block containing it. | Accepts expired evidence; its own proposals carrying it are rejected. |
| `evidence.max_bytes` | Rejects blocks with more evidence ([`internal/state/validation.go:129`](../../internal/state/validation.go)); halts on such a committed block. | Its proposals with more evidence are rejected. |
| `validator.pub_key_types` | Rejects valid validator updates (`validation.go:245`); halts at the next rotation. | Accepts key types honest nodes reject; it cannot commit alone, so this matters only if validators share the forged view. |
| `validator.voting_power_threshold` | Not forgeable at bootstrap: a mismatch with the pinned value fails state sync. If it were, the node would unlock on minority polkas (below). | Not forgeable at bootstrap; if it were, it would refuse valid polkas and commits (`vote_set.go:427`) and halt. |
| `synchrony.*` | Treats timely proposals as too early or late and prevotes nil ([`internal/consensus/state_data.go:361`](../../internal/consensus/state_data.go)). | Accepts untimely proposals. |
| `timeout.*` | Leaves rounds early and casts more nil votes (`state_data.go:636`, `:651`); local unsafe overrides take precedence. | Slower rounds; affects its own liveness. |
| `abci.recheck_tx` | Invalid transactions linger in the mempool and in its proposals ([`internal/state/execution.go:766`](../../internal/state/execution.go)). | Needless `CheckTx` load. |

A lowered voting-power threshold cannot finalize a commit on its own: precommit
finalization recovers a BLS threshold signature, which needs the DKG threshold
`QuorumTypeThresholdVotingPower`
([`types/vote_set.go:418`](../../types/vote_set.go), `:437`;
[`types/validator_set.go:608`](../../types/validator_set.go)). Prevote +2/3, the
polka behind POL, uses the configurable `QuorumVotingThresholdPower`
(`vote_set.go:411`, `validator_set.go:585`) with no signature recovery. A low
threshold therefore lets a small minority create polkas that unlock honest
validators across rounds
([`internal/consensus/state_prevoter.go:147`](../../internal/consensus/state_prevoter.go)),
so the classic cross-round fork no longer needs at least 1/3 Byzantine power.
This applies to any threshold below the strict 2/3+1 floor, which the override
from [#1052](https://github.com/dashpay/tenderdash/pull/1052) permits through
genesis or `consensus_param_updates`. It is a configuration risk, not a
bootstrap-forgery one: state sync pins the threshold to the trusted local value.
Dropping that pin without the full commitment would let a forged low threshold
make a state-synced validator unlock on minority polkas.

Halting is the common outcome and survives restarts: the forged values are
stored, and later partial updates replace only the groups they carry
([`types/params.go:408`](../../types/params.go)). Recovery requires wiping the
node and re-syncing from a trusted source. Divergence is the worse case: a node
serving clients answers from a different parameter view. A validator with forged
parameters reduces liveness in proportion to its voting power. Nothing detects
the forgery today, because the full-node `ConsensusHash` check
([`internal/state/validation.go:66`](../../internal/state/validation.go)) also
covers only three fields. A network-wide safety impact requires many nodes or validators
to sync from the same malicious source, such as a widely used public RPC
endpoint. With the full commitment, state sync rejects forged parameters at
bootstrap.

## Consensus version selects the hash rule

`VersionParams.consensus_version` selects the hash rule of the parameter set that
contains it:

| Effective `consensus_version` | Hash rule for `ConsensusHash` and `NextConsensusHash` |
| --- | --- |
| `CONSENSUS_VERSION_0`, `CONSENSUS_VERSION_1` | Legacy `HashedParams` hash, unchanged |
| `CONSENSUS_VERSION_2` | `FullHash` of the complete `HashedParamsV2` snapshot below |
| Any other value | Rejected |

`CONSENSUS_VERSION_2` keeps the version 1 proposer selection
(`NewHeightRoundProposerSelector`) and adds the full commitment.
`NewProposerSelector`
([`internal/consensus/versioned/selectproposer/proposer_selector.go:59`](../../internal/consensus/versioned/selectproposer/proposer_selector.go))
MUST map version 2 accordingly; today it rejects every value above 1.

The rule is self-describing: each parameter set is hashed by its own version. At
H-1, `NextConsensusHash` is computed from the next parameters, which already carry
version 2, so it uses the full rule while `ConsensusHash` stays legacy. The domain
prefix starts with byte `0x74`, which no legacy `HashedParams` encoding (tags 1–3)
can start with, so legacy and full preimages never coincide.

Validation MUST add two checks that do not exist today:

1. **Known versions only.** `ValidateConsensusParams`
   ([`types/params.go:289`](../../types/params.go)) does not range-check
   `Version.ConsensusVersion`. It MUST reject values outside the defined enum,
   including negative values, for genesis, InitChain, updates and state sync.
2. **Never decrease.** Where an ABCI update is merged and validated
   ([`internal/state/current_round_state.go:171`](../../internal/state/current_round_state.go)),
   reject any candidate whose consensus version is lower than the current one.
   `ValidateConsensusParams` sees only the candidate, so this check compares the
   current and candidate parameters. This changes today's behavior, which
   accepts any decrease, including 1 to 0.

Activation uses the existing mechanism: the application returns
`consensus_param_updates` with `version.consensus_version = 2`, as e2e testnets do
with `consensus_version_updates`
([`test/e2e/pkg/manifest.go:63`](../../test/e2e/pkg/manifest.go)). A genesis
document without a version group resolves to version 0
([`types/params.go:143`](../../types/params.go)) and therefore LEGACY.

Activation MUST NOT bump `version.BlockProtocol`; `CONSENSUS_VERSION_2` is the
only switch. `Header.ValidateBasic` requires every header's `Version.Block` to
equal the binary constant
([`types/block.go:477`](../../types/block.go)), state takes that constant at
genesis and rollback
([`internal/state/state.go:37`](../../internal/state/state.go),
[`internal/state/rollback.go:77`](../../internal/state/rollback.go)) and
otherwise copies it from trusted headers in state sync, and peers must match it
([`types/node_info.go:148`](../../types/node_info.go)). A bump would make the new
binary reject every historical block in block sync, replay and state sync, and
would split the network while nodes upgrade instead of switching at height H.

`UpdateConsensusParams` replaces the whole version group
([`types/params.go:420`](../../types/params.go)). Before `app_version` was
removed, a group sent only to set it also overwrote `consensus_version`,
typically with 0. The group now carries only `consensus_version`, so that pitfall
is gone. Protobuf cannot distinguish an omitted enum inside a present group from
an explicit 0, so a present version group with a lower value, including 0, is a
decrease and is rejected; omitting the group retains the version.

The `V2` in `HashedParamsV2` and the hash domain names the snapshot schema, not a
release. A later consensus version may keep this schema or define a new one.

## Complete snapshot and frozen mapping

Start with a validated, fully resolved effective `ConsensusParams` value. Apply
the normal ABCI candidate update and completion rules before constructing this
snapshot. Do not hash a response patch, pointer addresses, JSON, local timeout
overrides or opaque encoded `ConsensusParams` bytes. Nodes MUST use the same
effective parameter values for validation and commitment.

The source of truth is
[`proto/tendermint/types/params.proto`](../../proto/tendermint/types/params.proto).
`HashedParamsV2` is a flat message; the following mapping is exhaustive for the
seven parameter groups in this base. Durations are signed 64-bit nanosecond counts.
`Version.AppVersion` is deprecated and excluded rather than committed (see
[application version](#application-version-is-not-a-consensus-parameter)).
Tags are contiguous; the schema is not frozen yet.

| Tag | Snapshot field | Protobuf type | Effective source |
| --- | --- | --- | --- |
| 1 | `consensus_version` | `int32` | `Version.ConsensusVersion`; always 2 or higher, so always encoded |
| 2 | `block_max_bytes` | `int64` | `Block.MaxBytes` |
| 3 | `block_max_gas` | `int64` | `Block.MaxGas` |
| 4 | `evidence_max_age_num_blocks` | `int64` | `Evidence.MaxAgeNumBlocks` |
| 5 | `evidence_max_age_duration_ns` | `int64` | `Evidence.MaxAgeDuration` |
| 6 | `evidence_max_bytes` | `int64` | `Evidence.MaxBytes` |
| 7 | `validator_pub_key_types` | repeated `string` | `Validator.PubKeyTypes`, preserving order |
| 8 | `validator_voting_power_threshold` | optional `uint64` | `Validator.VotingPowerThreshold`, preserving absence versus explicit zero |
| 9 | `synchrony_message_delay_ns` | `int64` | `Synchrony.MessageDelay` |
| 10 | `synchrony_precision_ns` | `int64` | `Synchrony.Precision` |
| 11 | `timeout_propose_ns` | `int64` | `Timeout.Propose` |
| 12 | `timeout_propose_delta_ns` | `int64` | `Timeout.ProposeDelta` |
| 13 | `timeout_vote_ns` | `int64` | `Timeout.Vote` |
| 14 | `timeout_vote_delta_ns` | `int64` | `Timeout.VoteDelta` |
| 15 | `abci_recheck_tx` | `bool` | `ABCI.RecheckTx` |

Do not sort or deduplicate public-key types during hashing. Their order is part of
the committed value even if current validation treats the list as a membership
set. Null and zero voting thresholds remain distinct snapshots; resolving a
threshold to a quorum default MUST NOT erase this distinction. Duration conversion
MUST be exact and within int64 range; reject overflow and invalid effective values.

The `HashedParamsV2` field meanings, tags, wire types and canonical encoding MUST
be frozen before activation. A future parameter or changed interpretation requires
a new consensus version with a reviewed hash rule, not silent omission or mutation
of this snapshot. The existing `HashedParams` schema and historical bytes remain
unchanged.

## Canonical bytes and domain

For validated effective parameters `P`, define:

```text
FullHash(P) = SHA256(ASCII("tenderdash/consensus-params/v2") || 0x00 || EncodeV2(P))
```

`EncodeV2` is Protobuf wire encoding with these additional normative constraints:

1. Encode fields in ascending tag order. Encode each singular field at most once;
   repeated strings appear consecutively in their original order.
2. Use minimal unsigned varints for tags, lengths, unsigned integers and positive
   signed integers. Negative `int64` values use their two's-complement 64-bit
   value as a ten-byte varint; `block_max_gas = -1` is valid. Do not use zigzag
   encoding. Supported consensus-version values must pass protocol validation.
3. Omit zero non-presence scalar fields, false booleans and absent optional fields.
   Encode a present optional threshold even when its value is zero. A nonempty
   repeated list retains each entry, including an empty string if validation ever
   permits it; current parameter validation still applies.
4. Strings MUST be valid UTF-8 and are encoded as their exact UTF-8 bytes without
   Unicode normalization. No maps, nested messages or `Duration` wire encodings
   occur in this snapshot.
5. Reject unknown snapshot tags, duplicate singular fields, wrong wire types,
   nonminimal varints, malformed lengths, invalid UTF-8 and any byte sequence
   differing from the canonical re-encoding. Reject unsupported effective
   parameter fields before constructing the snapshot rather than dropping them.

Stable-marshaler options in the source schema support the implementation but are
not the cross-language definition. Generic “deterministic protobuf” serialization
alone is not a sufficient contract; see the official
[serialization is not canonical](https://protobuf.dev/programming-guides/serialization-not-canonical/)
guidance. Implementations MUST satisfy these rules and the shared conformance
vectors. The domain prefix is included only for FULL;
never apply it retroactively to a legacy hash.

## Activation and ordinary updates

Let H be the first height whose current parameters have consensus version 2 or
higher. The software and application upgrade MUST be deployed before height H-1,
whose candidate already needs to construct and validate a full next-parameter
commitment.

| Block height | Current version and `ConsensusHash` | Next version and `NextConsensusHash` |
| --- | --- | --- |
| H-2 | Below 2 / historical hash | Below 2 / historical hash |
| H-1 | Below 2 / historical hash | 2 / `FullHash(P_H)` |
| H and later | 2 or higher / `FullHash(P_h)` | 2 or higher / `FullHash(P_(h+1))` |

The parameter update returned for H-1 that raises the consensus version to 2 MUST
explicitly supply all seven parameter-group messages: block, evidence, validator,
version, synchrony, timeout and ABCI. Every duration field MUST be supplied. Each supplied group is
the complete intended replacement under the normal update semantics; zero scalar
values are intentional values where valid. Within the complete validator group,
an absent threshold explicitly means no configured override, not inheritance.
Recompute the candidate validator set's live voting threshold from these complete
parameters even when membership does not change: absence MUST clear a copied
override to the quorum-default representation (`VotingPowerThreshold = 0`).
Explicit zero also selects that default at runtime, while remaining distinct in
the snapshot. A signed parameter hash MUST NOT coexist with a stale inherited
validator-set override.

Reject activation patches that omit a group or duration, require inherited
uncommitted values, or fail normal validation. Validate completeness before any
completion routine can fill omissions. This prevents an earlier unauthenticated
state-sync parameter from seeding the first full snapshot. Validate the complete
candidate after normal ABCI processing and require the proposer and validator to
derive identical next commitments before acceptance. A chain starting at
consensus version 2 MUST likewise supply a complete validated genesis/InitChain parameter snapshot.

After activation, ordinary partial updates are allowed: merge into the last
authenticated full state, retain the consensus version when the version group is
omitted, reject any decrease, validate the result and hash the complete
effective value. Unchanged updates produce unchanged hashes.
Persist the selected current and next states atomically with their normal block
application state; restart and replay MUST recover the original transition.

## State sync, light verification and history

Both RPC and P2P state providers MUST select the hash rule from the candidate's
consensus version, verify the result against the signed header hash, and reject
mismatches or unknown versions before installing state. A peer's claimed version
is authenticated only by reproducing the signed hash under the rule it selects. Authenticate the snapshot at the
height whose `ConsensusHash` is used; if using a prior header's `NextConsensusHash`,
bind it to the successor height. Do not infer authenticity from structural
validation, a claimed response height or a claimed version of 2 or higher.

For FULL, allow legitimate threshold updates only after complete-hash verification;
do not keep the legacy genesis pin as a substitute for that authentication.
Bootstrap may require different parameter snapshots for `LastValidators` and
`Validators`: bind each validator set to its own height's signed parameters rather
than applying one current threshold to both. Preserve Dash Core quorum membership,
key and quorum-hash verification; `ValidatorSet.Hash` alone does not commit every
member or a voting-power override. Never trust remote quorum/threshold data merely
because it accompanies an application snapshot.

For LEGACY history, retain existing genesis-threshold and quorum-binding defenses;
FULL is not a reason to weaken verification of old blocks. Keep each historical
hash variant applicable to its original protocol and height. Old clients that
do not know version 2 cannot safely follow activation: interoperability is an
upgrade requirement, not a fallback to legacy verification.

## Application version is not a consensus parameter

`ConsensusParams.Version.AppVersion` (`VersionParams.app_version`, field 1) is
removed. The legacy `HashedParams` never included it, so no historical or LEGACY
hash changes, and the removal needs no activation height.

This change implements the following:

1. **Schema.** Field 1 and the name `app_version` are reserved in `VersionParams`;
   `types.VersionParams.AppVersion` and its conversions are removed.
2. **Block header.** `ResponsePrepareProposal.app_version` is the only source of
   `Header.Version.App` and MUST be non-zero. `ResponsePrepareProposal.Validate`
   already rejects zero ([`abci/types/types.go:275`](../../abci/types/types.go),
   called at [`internal/state/execution.go:301`](../../internal/state/execution.go)),
   so the proposer cannot produce a proposal without it. The value enters
   `StateID.AppVersion` ([`types/block.go:553`](../../types/block.go)) and is signed.
   Tenderdash does not check `Version.App` on non-proposers
   ([`internal/state/validation.go:33`](../../internal/state/validation.go)); the
   application MUST verify it in ProcessProposal.
3. **Committed state.** `State.Update` sets `State.Version.Consensus.App` from the
   committed header's `Version.App`
   ([`internal/state/execution.go:795`](../../internal/state/execution.go)), not
   from parameters. InitChain keeps the value that the handshake took from
   `ResponseInfo` at height 0
   ([`internal/consensus/replay.go:273`](../../internal/consensus/replay.go)).
   Rollback takes it from the header of the block rolled back to
   ([`internal/state/rollback.go:78`](../../internal/state/rollback.go)).
4. **Example applications.** The kvstore example and the e2e application do not
   send the field in parameter updates.
5. **InitChain.** `RequestInitChain.consensus_params` carries no application
   version, and Tenderdash provides no replacement channel. The application MUST
   determine its initial protocol version itself, for example per network or from
   its own configuration.
6. **Compatibility.** Protobuf decoding ignores field 1 sent by older applications
   in `InitChain`, `PrepareProposal` or `FinalizeBlock` parameter updates, and in
   stored parameters. Genesis JSON containing
   `consensus_params.version.app_version` still loads, because
   [`types/genesis.go`](../../types/genesis.go) decodes with lenient
   `json.Unmarshal`, but the value is ignored and not passed to InitChain or
   written back. Generated ABCI clients drop the field, which requires a
   coordinated application release.

**Breaking for Dash Platform.** Platform selects its starting `PlatformVersion`
from `RequestInitChain.consensus_params.version.app_version`, the genesis value
`1` on mainnet and testnet, and falls back to its desired version when the value
is 0
([`rs-drive-abci` `init_chain`](https://github.com/dashpay/platform/blob/de80f86d80ffcfa0a91ae59ce43cfeb161e27bc5/packages/rs-drive-abci/src/execution/engine/initialization/init_chain/mod.rs#L27-L52)).
Platform MUST determine its initial protocol version itself before it upgrades to
a Tenderdash without this field. Otherwise a node syncing from genesis starts at
the wrong protocol version and halts on an app-hash mismatch.

The following remain for the implementation:

1. **Node state.** Remove `State.Version.Consensus.App`, its height-0 write from
   `ResponseInfo` and its use in `State.MakeBlock`
   ([`internal/state/state.go:291`](../../internal/state/state.go)). Then
   `RequestPrepareProposal.version` carries only the block version and that field's
   comment changes. The `ver > 0` guard at `execution.go:324` becomes an
   unconditional assignment. The shared `tendermint.version.Consensus` message
   keeps `app` because `Header.Version` uses it.
2. **Node info.** Take `NodeInfo.ProtocolVersion.App` from
   `ResponseInfo.app_version`, which the handshake already returns
   ([`internal/consensus/replay.go:287`](../../internal/consensus/replay.go),
   [`node/node.go:514`](../../node/node.go)), instead of from state
   ([`node/node.go:533`](../../node/node.go),
   [`node/setup.go:447`](../../node/setup.go) for seeds, which have no application
   and report 0). Peer compatibility checks only the block version
   ([`types/node_info.go:148`](../../types/node_info.go)).
3. **State sync.** `verifyApp`
   ([`internal/statesync/syncer.go:697`](../../internal/statesync/syncer.go)) MUST
   require `ResponseInfo.app_version >= Header.Version.App` of the trusted snapshot
   header instead of equality; the header value is reached through the state
   provider's `State.Version`
   ([`internal/statesync/stateprovider.go:177`](../../internal/statesync/stateprovider.go)).
   Applications may report their latest supported protocol version in Info, as
   Dash Platform does.

## Integration and implementation sequence

1. Resolve the integration blocker before freezing this snapshot. This document
   allocates `CONSENSUS_VERSION_2`. Open
   [#1572](https://github.com/dashpay/tenderdash/pull/1572) also assigns
   `ConsensusVersion = 2` to heartbeat behavior and adds two timeouts to its legacy
   hash; [#1583](https://github.com/dashpay/tenderdash/pull/1583) also uses version 2
   for randomness. Reconcile these number assignments before any of them merges.
   Any heartbeat parameters merged before activation MUST be added to the full
   mapping/schema and vectors before freezing it. Preserve any merged legacy hash
   variant for replay. The 15-field draft above MUST NOT activate while that
   coverage is stale.
2. Implement the version range check, the never-decrease check, conversions,
   defaults and equality in `types/params.go` and ABCI/state messages, and map
   version 2 in `NewProposerSelector`. `Equals` currently omits voting-threshold
   presence/value; include it to avoid incorrect empty/no-op shortcuts.
3. Implement the canonical snapshot encoder and dispatch `HashConsensusParams` by
   consensus version; retain the existing legacy encoder. Wire current/next hashes
   through `internal/state/current_round_state.go`, `internal/state/validation.go` and
   proposal/block validation. Enforce the complete transition in genesis/InitChain
   and proposal processing. Recompute derived validator thresholds, including
   clearing a prior positive override when the complete new snapshot has none;
   do not depend on a membership update to perform this reset.
4. Update persisted-state conversions, replay and startup handling. Update both
   `internal/statesync/stateprovider.go` providers, `verifyConsensusParams`,
   `applyVotingPowerThreshold` and P2P parameter-response conversion. Audit RPC,
   light-client and SDK consumers for consensus-version transport and height binding.
5. Publish cross-language vectors and run the tests below. Coordinate an explicit
   H, deploy before H-1, exercise mixed-version rejection on a test network, and
   permit activation only after all blockers and acceptance tests pass.

## Required verification before runtime release

| Area | Required tests |
| --- | --- |
| Coverage | Mutate every mapped scalar and each list element/order; test nil, present zero and positive threshold separately. Every effective change changes the full hash. |
| Updates | Different patches reaching the same state hash equally; omitted groups/durations retain values only after activation; invalid values, unknown consensus versions, unsupported fields and any consensus-version decrease, including a present version group with 0, fail closed. |
| Transition | Complete H-1 proposal accepted, incomplete transition rejected; current/next hashes at H-2/H-1/H agree across proposer, validators, stored state and RPC. Old positive live threshold to FULL with no override clears the derived threshold even with unchanged members. Complete genesis at consensus version 2 works. |
| Encoding | Cross-language bytes and hashes for valid complete snapshots, zero-value omission, threshold presence, `max_gas = -1`, list order, UTF-8 and duration boundaries; reject duplicate/unknown fields and nonminimal encodings. |
| Authentication | Substitute each previously unhashed field in RPC/P2P responses; correlate response height and signed header; require per-height thresholds when validator sets cross an update. |
| Application version | Proposal with zero `app_version` rejected; committed and rolled-back state follow the header app version; legacy hashes unchanged; field 1 from an old application and in genesis JSON ignored; state sync accepts Info above the header version and rejects below. |
| Recovery | Replay and restart before/at/after H; historical signature/block-ID vectors unchanged; persisted consensus version and optional threshold survive round trips. |
| Quorum and policy | Retain Core-backed membership/key binding and all legacy trust defenses; authenticate FULL mutable thresholds without genesis pinning; do not claim raised-threshold signer-count attestation. |

## Tracking

No dedicated GitHub issue was found for the complete commitment at drafting.
This committed plan is the implementation tracker. Related work:
[#1442](https://github.com/dashpay/tenderdash/pull/1442) (legacy state-sync trust),
[#1476](https://github.com/dashpay/tenderdash/issues/1476) (peer/height correlation),
[#1546](https://github.com/dashpay/tenderdash/issues/1546) (deferred evidence) and
[#1547](https://github.com/dashpay/tenderdash/pull/1547) (evidence mode).

<sub>🤖 Co-authored by [Claudius the Magnificent](https://github.com/lklimek/claudius) AI Agent</sub>

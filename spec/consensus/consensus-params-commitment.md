# Full Consensus Parameter Commitment

**DRAFT.** This is a normative design and source schema proposal,
not implemented consensus behavior. Generated bindings, runtime support and
activation are deliberately deferred. The current Go implementation is unchanged.
MUST, MUST NOT and SHOULD describe the target implementation.

## Acceptance scenarios

| Scenario | Required result |
| --- | --- |
| Change any effective parameter, including evidence limits, timeout or threshold presence | The full commitment changes; a substituted snapshot fails authentication. |
| Equivalent complete state reached through different partial updates | Identical canonical bytes and hash; raw ABCI patches are never hashed. |
| Omit the commitment selector in a partial update | Retain the current selector. An explicit FULL-to-LEGACY downgrade is rejected. |
| Activate FULL at height H | H-1 signs the full next-parameter hash; H uses the full current-parameter hash. |
| Attempt activation with incomplete groups or duration fields | Reject before accepting a candidate; unauthenticated legacy values cannot fill gaps. |
| Bootstrap from FULL history with a changed threshold | Authenticate each required height's parameters and validator context; do not pin a legitimate mutable threshold to genesis. |
| Replay or restart across activation | Preserve selector presence and historical hash rules; produce the original block IDs and verify the original signatures. |
| Receive unknown selectors, unsupported parameter fields or noncanonical snapshot bytes | Fail closed; never silently downgrade or omit a new parameter. |

## Existing commitment and threat boundary

At this proposal's base, `types/params.go:HashConsensusParams` hashes only
`Block.MaxBytes`, `Block.MaxGas` and `Version.ConsensusVersion` through the existing
`HashedParams` message. Evidence limits, validator key types and voting threshold,
synchrony bounds, timeouts and ABCI rechecking are outside that digest. The
application version leaves consensus parameters entirely; see
[application version](#application-version-is-not-a-consensus-parameter). Actual evidence proofs already contribute to `EvidenceHash`; this
proposal adds their configurable limits, not a second commitment to the proofs.

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

## Independent commitment selector

`ConsensusParams.commitment_version`, optional field 8, selects the hash rule:

| Value | Meaning |
| --- | --- |
| `COMMITMENT_VERSION_LEGACY = 0` | Preserve the hash rule applicable to that historical protocol/height. |
| `COMMITMENT_VERSION_FULL = 1` | Domain-separated hash of the complete `HashedParamsV2` snapshot below. |

Omission in an existing genesis document means LEGACY. Omission in an ABCI partial
update means retain the current value, including FULL. An explicit selector is
validated before merging the update; unknown values and FULL-to-LEGACY transitions
MUST be rejected. ABCI patch conversions and no-op detection MUST preserve selector
presence and value: an explicit LEGACY value and an omitted update are different
inputs. Materialized state MUST serialize its resolved selector explicitly through
domain/protobuf/JSON, state-store and state-sync conversions; state equality MUST
compare that value as well as threshold presence and value.

This selector is independent of `VersionParams.ConsensusVersion`, the block wire
protocol version, and release numbering. FULL does not allocate
`CONSENSUS_VERSION_2` or any new block protocol constant. The `V2` in
`HashedParamsV2` and the hash domain names the snapshot schema, not a release.

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
Tag 9 and the name `version_app_version` are reserved and never assigned:
`Version.AppVersion` is removed rather than committed (see
[application version](#application-version-is-not-a-consensus-parameter)), so
the snapshot covers every remaining effective parameter.

| Tag | Snapshot field | Protobuf type | Effective source |
| --- | --- | --- | --- |
| 1 | `commitment_version` | `uint32` | Constant 1 for FULL |
| 2 | `block_max_bytes` | `int64` | `Block.MaxBytes` |
| 3 | `block_max_gas` | `int64` | `Block.MaxGas` |
| 4 | `evidence_max_age_num_blocks` | `int64` | `Evidence.MaxAgeNumBlocks` |
| 5 | `evidence_max_age_duration_ns` | `int64` | `Evidence.MaxAgeDuration` |
| 6 | `evidence_max_bytes` | `int64` | `Evidence.MaxBytes` |
| 7 | `validator_pub_key_types` | repeated `string` | `Validator.PubKeyTypes`, preserving order |
| 8 | `validator_voting_power_threshold` | optional `uint64` | `Validator.VotingPowerThreshold`, preserving absence versus explicit zero |
| 10 | `version_consensus_version` | `int32` | `Version.ConsensusVersion` |
| 11 | `synchrony_message_delay_ns` | `int64` | `Synchrony.MessageDelay` |
| 12 | `synchrony_precision_ns` | `int64` | `Synchrony.Precision` |
| 13 | `timeout_propose_ns` | `int64` | `Timeout.Propose` |
| 14 | `timeout_propose_delta_ns` | `int64` | `Timeout.ProposeDelta` |
| 15 | `timeout_vote_ns` | `int64` | `Timeout.Vote` |
| 16 | `timeout_vote_delta_ns` | `int64` | `Timeout.VoteDelta` |
| 17 | `abci_recheck_tx` | `bool` | `ABCI.RecheckTx` |

Do not sort or deduplicate public-key types during hashing. Their order is part of
the committed value even if current validation treats the list as a membership
set. Null and zero voting thresholds remain distinct snapshots; resolving a
threshold to a quorum default MUST NOT erase this distinction. Duration conversion
MUST be exact and within int64 range; reject overflow and invalid effective values.

The `HashedParamsV2` field meanings, tags, wire types and canonical encoding MUST
be frozen before activation. A future parameter or changed interpretation requires
an explicit reviewed commitment-version upgrade, not silent omission or mutation
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

Let H be the first height whose current parameters select FULL. The software and
application upgrade MUST be deployed before height H-1, whose candidate already
needs to construct and validate a full next-parameter commitment.

| Block height | Current selector and `ConsensusHash` | Next selector and `NextConsensusHash` |
| --- | --- | --- |
| H-2 | LEGACY / historical hash | LEGACY / historical hash |
| H-1 | LEGACY / historical hash | FULL / `FullHash(P_H)` |
| H and later | FULL / `FullHash(P_h)` | FULL / `FullHash(P_(h+1))` |

At H-1 the application MUST explicitly supply `commitment_version = FULL` and all
seven parameter-group messages: block, evidence, validator, version, synchrony,
timeout and ABCI. Every duration field MUST be supplied. Each supplied group is
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
derive identical next commitments before acceptance. A chain starting in FULL
MUST likewise supply a complete validated genesis/InitChain parameter snapshot.

After activation, ordinary partial updates are allowed: merge into the last
authenticated full state, preserve an omitted selector, validate the result and
hash the complete effective value. Unchanged updates produce unchanged hashes.
Persist the selected current and next states atomically with their normal block
application state; restart and replay MUST recover the original transition.

## State sync, light verification and history

Both RPC and P2P state providers MUST select the commitment rule from authenticated
protocol/height context and the candidate selector, verify it against the signed
header hash, and reject mismatches or unknown rules before installing state.
A peer's selector is not independently trusted. Authenticate the snapshot at the
height whose `ConsensusHash` is used; if using a prior header's `NextConsensusHash`,
bind it to the successor height. Do not infer authenticity from structural
validation, a claimed response height or the mere presence of FULL.

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
ignore the selector cannot safely follow activation: interoperability is an
upgrade requirement, not a fallback to legacy verification.

## Application version is not a consensus parameter

`ConsensusParams.Version.AppVersion` (`VersionParams.app_version`, field 1) is
removed. Field 1 and its name are reserved in `VersionParams`. The legacy
`HashedParams` never included it, so no historical or LEGACY hash changes, and the
removal needs no activation height. Citations below describe this base.

1. **Block header.** `ResponsePrepareProposal.app_version` MUST be non-zero and is
   the only source of `Header.Version.App`. `ResponsePrepareProposal.Validate`
   already rejects zero ([`abci/types/types.go:275`](../../abci/types/types.go),
   called at [`internal/state/execution.go:301`](../../internal/state/execution.go));
   the proposer cannot produce a proposal without it, and the `ver > 0` guard at
   `execution.go:324` becomes an unconditional assignment. The value enters
   `StateID.AppVersion` ([`types/block.go:553`](../../types/block.go)) and is signed.
   Tenderdash does not check `Version.App` on non-proposers
   ([`internal/state/validation.go:33`](../../internal/state/validation.go)); the
   application MUST verify it in ProcessProposal.
2. **Node state.** `State.Version.Consensus.App` is removed with its writers:
   parameters at commit
   ([`internal/state/current_round_state.go:100`](../../internal/state/current_round_state.go)),
   `ResponseInfo` at height 0
   ([`internal/consensus/replay.go:273`](../../internal/consensus/replay.go)) and
   previous parameters on rollback
   ([`internal/state/rollback.go:78`](../../internal/state/rollback.go)).
   `State.MakeBlock` does not seed the header's app version from state
   ([`internal/state/state.go:291`](../../internal/state/state.go)), so
   `RequestPrepareProposal.version` carries only the block version; Tenderdash
   leaves `app` unset. The shared `tendermint.version.Consensus` message keeps
   `app` because `Header.Version` uses it.
3. **Node info.** `NodeInfo.ProtocolVersion.App` is taken from
   `ResponseInfo.app_version`, which the handshake already returns
   ([`internal/consensus/replay.go:287`](../../internal/consensus/replay.go),
   [`node/node.go:514`](../../node/node.go)), instead of from state
   ([`node/node.go:533`](../../node/node.go),
   [`node/setup.go:447`](../../node/setup.go) for seeds, which have no application
   and report 0). Peer compatibility checks only the block version
   ([`types/node_info.go:148`](../../types/node_info.go)).
4. **State sync.** `verifyApp`
   ([`internal/statesync/syncer.go:697`](../../internal/statesync/syncer.go)) MUST
   require `ResponseInfo.app_version >= Header.Version.App` of the trusted snapshot
   header instead of equality; the header value is today reached through the state
   provider's `State.Version`
   ([`internal/statesync/stateprovider.go:176`](../../internal/statesync/stateprovider.go)).
   Applications may report their latest supported protocol version in Info, as
   Dash Platform does.
5. **Compatibility.** Protobuf decoding ignores field 1 sent by older applications
   in `InitChain`, `PrepareProposal` or `FinalizeBlock` parameter updates. Genesis
   JSON containing `consensus_params.version.app_version` still loads, because
   [`types/genesis.go`](../../types/genesis.go) decodes with lenient
   `json.Unmarshal`. Generated ABCI clients drop the field, which requires a
   coordinated application release.

## Integration and implementation sequence

1. Resolve the integration blocker before freezing this snapshot. Open
   [#1572](https://github.com/dashpay/tenderdash/pull/1572) assigns heartbeat behavior
   `ConsensusVersion = 2` and adds two timeouts to its legacy hash;
   [#1583](https://github.com/dashpay/tenderdash/pull/1583) also uses version 2 for
   randomness. Reconcile those protocol assignments separately. Any heartbeat
   parameters merged before activation MUST be added to the full mapping/schema
   and vectors before freezing it. Preserve any merged legacy hash variant for
   replay. The 16-field draft above MUST NOT activate while that coverage is stale.
2. Generate bindings and implement selector-preserving conversions, defaults,
   equality and update validation in `types/params.go` and ABCI/state messages.
   `Equals` currently omits voting-threshold presence/value; include both that
   comparison and the selector to avoid incorrect empty/no-op shortcuts.
3. Implement the canonical snapshot encoder and version dispatch; retain the
   existing legacy encoder. Wire current/next hashes through
   `internal/state/current_round_state.go`, `internal/state/validation.go` and
   proposal/block validation. Enforce the complete transition in genesis/InitChain
   and proposal processing. Recompute derived validator thresholds, including
   clearing a prior positive override when the complete new snapshot has none;
   do not depend on a membership update to perform this reset. Do not allocate a
   new consensus version implicitly.
4. Update persisted-state conversions, replay and startup handling. Update both
   `internal/statesync/stateprovider.go` providers, `verifyConsensusParams`,
   `applyVotingPowerThreshold` and P2P parameter-response conversion. Audit RPC,
   light-client and SDK consumers for selector transport and height binding.
5. Publish cross-language vectors and run the tests below. Coordinate an explicit
   H, deploy before H-1, exercise mixed-version rejection on a test network, and
   permit activation only after all blockers and acceptance tests pass.

## Required verification before runtime release

| Area | Required tests |
| --- | --- |
| Coverage | Mutate every mapped scalar and each list element/order; test nil, present zero and positive threshold separately. Every effective change changes the full hash. |
| Updates | Different patches reaching the same state hash equally; omitted groups/durations retain values only after activation; invalid values, unknown selectors, unsupported fields and downgrade attempts fail closed. |
| Transition | Complete H-1 proposal accepted, incomplete transition rejected; current/next hashes at H-2/H-1/H agree across proposer, validators, stored state and RPC. Old positive live threshold to FULL with no override clears the derived threshold even with unchanged members. Complete FULL genesis works. |
| Encoding | Cross-language bytes and hashes for valid complete snapshots, zero-value omission, threshold presence, `max_gas = -1`, list order, UTF-8 and duration boundaries; reject duplicate/unknown fields and nonminimal encodings. |
| Authentication | Substitute each previously unhashed field in RPC/P2P responses; correlate response height and signed header; require per-height thresholds when validator sets cross an update. |
| Application version | Proposal with zero `app_version` rejected; header app version always equals the proposer response; legacy hashes unchanged; field 1 from an old application and in genesis JSON ignored; state sync accepts Info above the header version and rejects below. |
| Recovery | Replay and restart before/at/after H; historical signature/block-ID vectors unchanged; persisted selector and optional threshold survive round trips. |
| Quorum and policy | Retain Core-backed membership/key binding and all legacy trust defenses; authenticate FULL mutable thresholds without genesis pinning; do not claim raised-threshold signer-count attestation. |

## Tracking

No dedicated GitHub issue was found for the complete commitment at drafting.
This committed plan is the implementation tracker. Related work:
[#1442](https://github.com/dashpay/tenderdash/pull/1442) (legacy state-sync trust),
[#1476](https://github.com/dashpay/tenderdash/issues/1476) (peer/height correlation),
[#1546](https://github.com/dashpay/tenderdash/issues/1546) (deferred evidence) and
[#1547](https://github.com/dashpay/tenderdash/pull/1547) (evidence mode).

<sub>🤖 Co-authored by [Claudius the Magnificent](https://github.com/lklimek/claudius) AI Agent</sub>

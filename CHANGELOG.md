## [0.11.1] - 2023-05-02

### ABCI++

- Update new protos to use enum instead of bool (#8158)

### ADR

- Protocol Buffers Management (#8029)

### Bug Fixes

- Backport e2e tests (#248)
- Remove option c form linux build (#305)
- Cannot read properties of undefined
- Network stuck due to outdated proposal block (#327)
- Don't process WAL logs for old rounds (#331)
- Network stuck due to outdated proposal block (#327)
- Don't process WAL logs for old rounds (#331)
- Use thread-safely way to get pro-tx-hash from peer-state (#344)
- Slightly modify a way of interacting with p2p channels in consensus reactor (#357)
- Remove select block to don't block sending a witness response (#336)
- Unsupported priv validator type - dashcore.RPCClient (#353)
- Add a missed "info" field to broadcast-tx-response (#369)
- Consolidate all prerelease changes in latest full release changelog
- First part of modification after merge
- Mishandled pubkey read errors
- Eliminate compile level issues
- Unit tests in abci/example/kvstore
- Unit tests in dash/quorum package
- Deadlock at types.MockPV
- Blocksync package
- Evidence package
- Made some fixes/improvements
- Change a payload hash of a message vote
- Remove using a mutex in processPeerUpdate to fix a deadlock
- Remove double incrementing
- Some modifications for fixing unit tests
- Modify TestVoteString
- Some fixes / improvements
- Some fixes / improvements
- Override genesis time for pbst tests
- Pbst tests
- Disable checking duplicate votes
- Use the current time always when making proposal block
- Consensus state tests
- Consensus state tests
- Consensus state tests
- The tests inside state package
- Node tests
- Add custom marshalling/unmarshalling for coretypes.ResultValidators
- Add checking on nil in Vote.MarshalZerologObject
- Light client tests
- Rpc tests
- Remove duplicate test TestApp_Height
- Add mutex for transport_mconn.go
- Add required option "create-proof-block-range" in a config testdata
- Type error in generateDuplicateVoteEvidence
- Use thread safe way for interacting with consensus state
- Use a normal time ticker for some consensus unit tests
- E2e tests
- Lint issues
- Abci-cli
- Detected data race
- TestBlockProtoBuf
- Lint (proto and golang) modifications
- ProTxHash not correctly initialized
- Lint issue
- Proto lint
- Reuse setValSetUpdate to update validator index and validator-set-updates item in a storage
- Reuse setValSetUpdate to update validator index and validator-set-updates item in a storage
- Fix dependencies in e2e tests
- Install libpcap-dev before running go tests
- Install missing dependencies for linter
- Fix race conditions in reactor
- Specify alpine 3.15 in Dockerfile
- Release script tries to use non-existing file
- Go link issues
- Data-race issue
- Applied changes according to PR feedback
- Make NewSignItem and MakeSignID exported, revert to precalculate hash for SignItem
- Quorum_sign_data_test.go
- Lint issue
- Check a receiver of ValidatorSet on nil
- Invalid initial height in e2e vote extensions test (#419)
- A block with a height is equal initial-height uses current time instead of genesis last-block-time
- Update block time validation
- Change validateBlockTime function
- Update evidence_test.go
- Go lint issues (#455)
- TestReactorValidatorSetChanges (#468)
- Don't inc proposer prio when processing InitChain response (#470)
- Invalid error msg when verifying val power in genesis doc (#476)
- Revert ResponseCheckTx.info field and pass it to ResultBroadcastTx (#488)
- Fix p2p deadlock (#473)
- Abci Info() returns invalid height at genesis (#474)
- Catchup round number is not correct (#507)
- Commits received during state sync are lost (#513)
- Statesync stops the node when light block request fails (#514)
- ProcessProposal executed twice for a block (#516)
- Proposer-based timestamp broken during backport (#523)
- Improve wal replay mechanism (#510)
- Decrease log verbosity by logging send/recv logs on trace level (#533)
- Ensure process proposal was called on commit processing (#534)
- Ensure process proposal runs on complete proposal (#538)
- Peer notifications should be async to avoid deadlock in PeerUp (#509)
- Improve flaky TestWALRoundsSkipper (#539)
- Flaky TestReactor_Backfill test (#549)
- [**breaking**] Quorum type set to 0 during replay at genesis (#570)
- Seed doesn't respond to pex requests (#574)
- Docker tag is invalid for empty input.tag (#580)
- Docker tag is invalid for empty input.tag (#580) (#585)
- Signature verification (#584)
- Replace tenderdash init single with validator (#599)
- Broken error handling in ValueOp (VSA-2022-100) (#601)
- Broken error handling in ValueOp (VSA-2022-100) (#601)
- Nil voteset panics in rest handler (#609) (#612)
- Update quorum params (#626)

### Docs

- Abci++ typo (#8147)

### Documentation

- Add an overview of the proposer-based timestamps algorithm (#8058)
- PBTS synchrony issues runbook (#8129)
- Go tutorial fixed for 0.35.0 version (#7329) (#7330) (#7331)
- Update go ws code snippets (#7486) (#7487)
- Remove spec section from v0.35 docs (#7899)
- Abcidump documentation
- Same-block execution docs and protobuf cleanup (#454)

### Features

- Abci protocol parser
- Abci protocol parser - packet capture
- Parse CBOR messages
- Add missed fields (CoreChainLockedHeight, ProposerProTxHash and ProposedAppVersion) to RequestFinalizeBlock and PrepareProposal
- Add node's pro-tx-hash into a context (#416)
- Same-block execution (#418)
- Implement import app-state in init-chain request (#472)
- Consensus params updates support (#475)
- Add round to Prepare/ProcessProposal,  FinalizeBlock (#498)
- Add core_chain_lock_update to RequestProcessProposal (#492)
- [**breaking**] Include state id in block signature (#478)
- [**breaking**] Put full block in RequestFinalizeBlock  (#505)
- Upgrade bls library to version 1 (#224)
- Seed connectivity tuning options (max-incoming-connection-time,incoming-connection-window) (#532)
- [**breaking**] Verify next consensus params between nodes (#550)
- Add quorum hash to RequestPrepare/ProcessProposal (#553)
- Derive node key from bip39 mnemonic (#562)
- Conversion of PEM-encoded ED25519 node keys (#564)

### Miscellaneous Tasks

- Stabilize consensus algorithm (#284)
- Temporarily disable ARM build which is broken
- Backport Tendermint 0.35.1 to Tenderdash 0.8 (#309)
- Update CI e2e action workflow (#319)
- Change dockerhub build target
- Inspect context
- Bump golang version
- Remove debug
- Use gha cache from docker
- Revert dev changes
- Remove obsolete cache step
- Update changelog and version to 0.7.1
- If the tenderdash source code is not tracked by git then cloning "develop_0.1" branch as fallback scenario to build a project (#356)
- If the tenderdash source code is not tracked by git then cloning "develop_0.1" branch as fallback scenario to build a project (#355)
- Update changelog and version to 0.8.0-dev.2 (#333)
- Update changelog and version to 0.8.0-dev.3
- Update changelog and version to 0.8.0-dev.4 (#370)
- Don't fail due to missing bodyclose in go 1.18
- Remove printing debug stacktrace for a duplicate vote
- Remove redundant mock cons_sync_reactor.go
- Remove github CI docs-toc.yml workflow
- Refactor e2e initialization
- Fix whitespace and comments
- Add unit tests for TestMakeBlockSignID, TestMakeStateSignID, TestMakeVoteExtensionSignIDs
- Some naming modifications
- Add verification for commit vote extension threshold signatures
- Modify a condition in VoteExtSigns2BytesSlices
- Remove recoverableVoteExtensionIndexes
- Some improvements
- Cleanup during self-review
- Remove duplicate test
- Update go.mod
- Update changelog and version to 0.8.0-dev.5
- Update changelog and version to 0.8.0-dev.5
- Preallocate the list
- Fix unit tests
- Fix unit tests
- Some modification after self-review
- Remove ThresholdVoteExtension as redundant, use VoteExtension instead
- Update order fields initialization
- Update abci++ spec
- Update changelog and version to 0.8.0-dev.6
- Update changelog and version to 0.8.0-dev.7
- Update alpine image version
- Update alpine image version
- Update changelog and version to 0.8.0-dev.8
- Update changelog and version to 0.8.0-dev.9
- Update changelog and version to 0.8.0-dev.10
- Update changelog and version to 0.9.0-dev.1
- Update changelog and version to 0.10.0-dev.1 (#456)
- Revert `validateBlockTime` (#458)
- Update changelog and version to 0.10.0-dev.2 (#489)
- Improve validation of ResponsePrepare/ProcessProposal ExecTxResults (#477)
- Update changelog and version to 0.10.0-dev.3 (#502)
- Update changelog and version to 0.10.0-dev.4 (#503)
- Update changelog and version to 0.10.0-dev.5 (#511)
- Backport to 0.8
- Fix build
- Fix abcidump after backport
- Update changelog and version to 0.8.0
- [**breaking**] Rename genesis.json quorum fields (#515)
- [**breaking**] Remove Snapshot.core_chain_locked_height (#527)
- Update changelog and version to 0.10.0-dev.6 (#526)
- Update changelog and version to 0.11.0-dev.1 (#530)
- Update changelog and version to 0.10.0-dev.7 (#536)
- Update bls library (#535)
- Update changelog and version to 0.10.0-dev.9 (#579)
- Update changelog and version to 0.11.0-dev.2 (#583)
- Update changelog and version to 0.11.0-dev.3 (#586)
- Bump up dashd-go version to v0.23.6 (#587)
- Update changelog and version to 0.10.0-dev.10 (#588)
- Update changelog and version to 0.10.0-dev.11 (#591)
- Update changelog and version to 0.11.0-dev.4 (#593)
- Add quote to CGO variables in Makefile (#597)

### PBTS

- System model made more precise (#8096)

### Refactor

- Replace several functions with an identical body (processStateCh,processDataCh,processVoteCh,processVoteSetBitsCh) on one function processMsgCh (#296)
- [**breaking**] Replace is-masternode config with mode=validator (#308)
- Add MustPubKeyToProto helper function (#311)
- Implementing LLMQ generator (#310)
- Move bls CI code to a separate action and improve ARM build (#314)
- Persistent kvstore abci (#313)
- Improve statesync.backfill (#316)
- Small improvement in test four add four minus one genesis validators (#318)
- Consolidate redundant code (#322)
- Single vote-extension field was modified on multiple ones. support default and threshold-recover types of extensions
- Simplify priv validator initialization code
- Add a centralized way for recovering threshold signatures, add a way of creating sign ids, refactor code to use one way of making sign data and recovering signs
- Standardize the naming of functions, variables
- Add some modifications by RP feedback
- Refactor cbor and apply review feedback
- Move abcidump from scripts/ to cmd/
- Separate default and threshold-recover extensions between 2 independent list, persist threshold vote extensions with a commit
- Revert vote-extension protobuf structures to previous version
- The changes by PR feedback
- DashCoreSignerClient should return correct private key
- Modifications after merge
- Abci app expects tendermint.version.Consensus rather than proposed-app-version in RequestFinalizeBlock and RequestPrepareProposal
- Revert proposed_app_version
- Allow set 0 for 'create-proof-block-range' to ignore proof block app hash
- Start test of proof-block range from 0 height
- Allow set 0 for 'create-proof-block-range' to ignore proof block app hash
- Start test of proof-block range from 0 height
- Enable building docker for develop branch (#443)
- Publish block events after block commit (#459)
- Handshake block-replay mechanism (#460)
- Merge e2e app with kvstore and support same-block execution (#457)
- Change a logic of usage CoreChainLockHeight (#485)
- Remove unused P2P.PexReactor flag field in a config (#490)
- Provide a current block commit with request finalize block request (#501)
- Make all genesis-doc fields (except chain_id) optional (#506)
- Optimize initialize priv-validator (#512)
- Use logger for log printing (#545)
- Blocksync.bpRequester should stop procedure if block was received (#546)
- [**breaking**] Cleanup protobuf definitions and reorganize fields (#552)
- Replace peerID on proTxHash for peer catchup rounds in HeightVoteSet component (#559)
- Sync node and seed implementation (#576)
- Use llmq.Validate function to validate llmq type (#590)
- Introduce p2p proto Envelope as a wrapper for p2p messages (#598)

### Security

- Bump actions/checkout from 2.4.0 to 3 (#8076)
- Bump docker/login-action from 1.13.0 to 1.14.1 (#8075)
- Bump golangci/golangci-lint-action from 2.5.2 to 3.1.0 (#8074)
- Bump google.golang.org/grpc from 1.44.0 to 1.45.0 (#8104)
- Bump github.com/spf13/cobra from 1.3.0 to 1.4.0 (#8109)
- Bump github.com/stretchr/testify from 1.7.0 to 1.7.1 (#8131)
- Bump gaurav-nelson/github-action-markdown-link-check from 1.0.13 to 1.0.14 (#8166)
- Bump docker/build-push-action from 2.9.0 to 2.10.0 (#8167)
- Bump github.com/golangci/golangci-lint from 1.44.2 to 1.45.0 (#8169)
- Bump github.com/golangci/golangci-lint from 1.45.0 to 1.45.2 (#8192)
- Bump github.com/adlio/schema from 1.2.3 to 1.3.0 (#8201)
- Bump github.com/vektra/mockery/v2 from 2.10.0 to 2.10.1 (#8226)
- Bump github.com/vektra/mockery/v2 from 2.10.1 to 2.10.2 (#8246)
- Bump github.com/vektra/mockery/v2 from 2.10.2 to 2.10.4 (#8250)
- Bump github.com/BurntSushi/toml from 1.0.0 to 1.1.0 (#8251)
- Bump github.com/lib/pq from 1.10.4 to 1.10.5 (#8283)
- Bump codecov/codecov-action from 2.1.0 to 3.0.0 (#8306)
- Bump actions/setup-go from 2 to 3 (#8305)
- Bump actions/stale from 4 to 5 (#8304)
- Bump actions/download-artifact from 2 to 3 (#8302)
- Bump actions/upload-artifact from 2 to 3 (#8303)
- Bump github.com/creachadair/tomledit from 0.0.11 to 0.0.13 (#8307)
- Bump github.com/vektra/mockery/v2 from 2.10.4 to 2.10.6 (#8346)
- Bump github.com/spf13/viper from 1.10.1 to 1.11.0 (#8344)
- Bump github.com/creachadair/atomicfile from 0.2.4 to 0.2.5 (#8365)
- Bump github.com/vektra/mockery/v2 from 2.10.6 to 2.11.0 (#8374)
- Bump github.com/creachadair/tomledit from 0.0.16 to 0.0.18 (#8392)
- Bump bufbuild/buf-setup-action from 1.3.1 to 1.4.0 (#8405)
- Bump codecov/codecov-action from 3.0.0 to 3.1.0 (#8406)
- Bump google.golang.org/grpc from 1.45.0 to 1.46.0 (#8408)
- Bump github.com/vektra/mockery/v2 from 2.12.0 to 2.12.1 (#8417)
- Bump github.com/google/go-cmp from 0.5.7 to 0.5.8 (#8422)
- Bump github.com/creachadair/tomledit from 0.0.18 to 0.0.19 (#8440)
- Bump github.com/btcsuite/btcd from 0.22.0-beta to 0.22.1 (#8439)
- Bump docker/setup-buildx-action from 1.6.0 to 1.7.0 (#8451)
- Merge result of tendermint/master with v0.8-dev (#376)

### Test

- Add deadlock detection with go-deadlock (#471)

### Testing

- Logger cleanup (#8153)
- KeepInvalidTxsInCache test is invalid
- Fix validator conn executor test backport
- Update mockery mocks
- Fix test test_abci_cli
- Update oss-fuzz build script to match reality (#8296)
- Convert to Go 1.18 native fuzzing (#8359)
- Remove debug logging statement (#8385)
- Use correct home path in TestRootConfig
- Add cbor test
- Add parse cmd test
- Test parser NewMessageType
- Test parser
- Replace hardcoded input data
- Skip broken PBTS tests (#500)
- Fix Index out of bounds on "runner logs" (#537)
- Update test vectors for BLS
- Refactor genesis doc generation (#573)
- Fix TestMakeHTTPDialerURL (#605)

### Abci

- Synchronize FinalizeBlock with the updated specification (#7983)
- Avoid having untracked requests in the channel (#8382)
- Streamline grpc application construction (#8383)
- Application type should take contexts (#8388)
- Application should return errors errors and nilable response objects (#8396)
- Remove redundant methods in client (#8401)
- Remove unneccessary implementations (#8403)
- Interface should take pointers to arguments (#8404)

### Abci++

- Synchronize PrepareProposal with the newest version of the spec (#8094)
- Remove app_signed_updates (#8128)
- Remove CheckTx call from PrepareProposal flow (#8176)
- Correct max-size check to only operate on added and unmodified (#8242)
- Only include meaningful header fields in data passed-through to application (#8216)
- Sync implementation and spec for vote extensions (#8141)
- Remove intermediate protos (#8414)
- Vote extension cleanup (#8402)

### Autofile

- Reduce minor panic and docs changes (#8122)
- Remove vestigal close mechanism (#8150)

### Backport

- Add basic metrics to the indexer package. (#7250) (#7252)
- V0.7.1 into v0.8-dev (#361)
- Upgrade logging to v0.8
- Update for new logging
- Tendermint v0.36 (#446)
- Catch up on the latest changes from v0.10 (#528)
- Catch up the recent changes from v0.10 to v0.11 (#589)
- V0.10 to v0.11 (#596)
- Use dashd-go 0.24.0 to support LLMQ type 6 (LLMQType_25_67) (#610) (#611)

### Blocksync

- Drop redundant shutdown mechanisms (#8136)
- Remove intermediate channel (#8140)
- Honor contexts supplied to BlockPool (#8447)

### Build

- Bump docker/login-action from 1.13.0 to 1.14.1
- Bump golangci/golangci-lint-action from 2.5.2 to 3.1.0
- Bump google.golang.org/grpc from 1.41.0 to 1.42.0 (#7218)
- Bump github.com/lib/pq from 1.10.3 to 1.10.4
- Bump github.com/tendermint/tm-db from 0.6.4 to 0.6.6 (#7285)
- Bump minimist from 1.2.5 to 1.2.6 in /docs (#8196)
- Bump bufbuild/buf-setup-action from 1.1.0 to 1.3.0 (#8199)
- Bump github.com/spf13/viper from 1.9.0 to 1.10.0 (#7435)
- Bump github.com/adlio/schema from 1.2.2 to 1.2.3 (#7436)
- Bump github.com/spf13/cobra from 1.2.1 to 1.3.0 (#7457)
- Bump github.com/rs/zerolog from 1.26.0 to 1.26.1 (#7467)
- Downgrade tm-db from v0.6.7 to v0.6.6
- Use Go 1.18 to fix issue building curve25519-voi
- Bump bufbuild/buf-setup-action from 1.3.0 to 1.3.1 (#8245)
- Provide base branch to make as variable (#321)
- Implement full release workflow in the release script (#332)
- Use go install instead of go get. (#8299)
- Implement full release workflow in the release script (#332) (#345)
- Implement full release workflow in the release script (#332) (#345)
- Bump async from 2.6.3 to 2.6.4 in /docs (#8357)
- Bump github.com/vektra/mockery/v2 from 2.11.0 to 2.12.0 (#8393)
- Bump docker/build-push-action from 2.9.0 to 3.0.0
- Bump docker/login-action from 1.14.1 to 2.0.0
- Bump docker/setup-buildx-action from 1.6.0 to 2.0.0
- Use golang 1.18
- Upgrade golangci-lint to 1.46
- Bump actions/setup-go from 2 to 3.1.0
- Bump golangci/golangci-lint-action from 3.1.0 to 3.2.0
- Bump actions/setup-go from 3.1.0 to 3.2.0
- Bump github.com/golangci/golangci-lint
- Bump actions/setup-go from 3.2.0 to 3.2.1
- Bump actions/stale from 5 to 6
- Bump actions/setup-go from 3.2.1 to 3.3.1
- Bump bufbuild/buf-setup-action from 1.6.0 to 1.9.0
- Bump docker/setup-buildx-action from 2.0.0 to 2.2.1
- Bump golangci/golangci-lint-action from 3.2.0 to 3.3.0
- Remove unused nightly test runs (#499)
- Save e2e failure logs as artifact (#508)
- Bump golangci/golangci-lint-action from 3.3.0 to 3.3.1 (#504)
- Update go.mod
- Fix missing dependencies in lint and tests
- Fix superlinter yaml issues
- Improve release script for v0.8 (#520)
- Bump actions/setup-go from 3.3.1 to 3.4.0 (#524)
- Bump bufbuild/buf-setup-action from 1.9.0 to 1.10.0 (#525)
- CGO paths to BLS deps are incorrect (#531)
- Improve release script (#522)
- Bump goreleaser/goreleaser-action from 3 to 4 (#544)
- Bump actions/stale from 6 to 7 (#543)
- Bump actions/setup-go from 3.4.0 to 3.5.0 (#542)
- Bump bufbuild/buf-setup-action from 1.10.0 to 1.11.0 (#541)
- Use ubuntu 20.04 in github workflows (#547)
- Enable deadlock detection on -dev docker images  (#540)
- Bump bufbuild/buf-setup-action from 1.11.0 to 1.12.0 (#556)
- Bump docker/build-push-action from 3.1.0 to 3.3.0 (#555)
- Use version 1.2.5 of BLS lib in Docker (#557)
- Bump golangci/golangci-lint-action from 3.3.1 to 3.4.0 (#560)
- Add abcidump to release image (#563)
- Bump bufbuild/buf-setup-action from 1.12.0 to 1.13.1 (#566)
- Bump docker/setup-buildx-action from 2.2.1 to 2.4.0 (#567)
- Improve docker image build caching (#571)
- Refactor e2e docker image build process (#575)
- Bump docker/build-push-action from 3.3.0 to 4.0.0 (#568)
- Bump docker/setup-buildx-action from 2.4.0 to 2.4.1 (#572)
- Bump bufbuild/buf-setup-action from 1.13.1 to 1.14.0 (#577)
- Move e2e-manual.yml  logic to e2e.yml (#578)
- Fix broken github actions and regenerate some code (#615)
- Bump github/super-linter from 4 to 5 (#624)

### Ci

- Move test execution to makefile (#7372) (#7374)
- Update mergify for tenderdash 0.8
- Cleanup build/test targets (backport #7393) (#7395)
- Skip docker image builds during PRs (#7397) (#7398)
- Fix super-linter configuration settings (backport #7708) (#7710)
- Fixes for arm builds

### Cleanup

- Remove commented code (#8123)
- Unused parameters (#8372)
- Pin get-diff-action uses to major version only, not minor/patch (#8368)

### Cli

- Add graceful catches to SIGINT (#8308)
- Simplify resetting commands (#8312)

### Cmd

- Make reset more safe (#8081)
- Cosmetic changes for errors and print statements (#7377) (#7408)
- Add integration test for rollback functionality (backport #7315) (#7369)

### Config

- Add a Deprecation annotation to P2PConfig.Seeds. (#7496) (#7497)
- Default indexer configuration to null (#8222)
- Minor template infrastructure (#8411)

### Confix

- Clean up and document transformations (#8301)
- Remove mempool.version in v0.36 (#8334)
- Convert tx-index.indexer from string to array (#8342)

### Consensus

- Improve wal test cleanup (#8059)
- Fix TestInvalidState race and reporting (#8071)
- Ensure the node terminates on consensus failure (#8111)
- Avoid extra close channel (#8144)
- Avoid persistent kvstore in tests (#8148)
- Avoid race in accessing channel (#8149)
- Skip channel close during shutdown (#8155)
- Change lock handling in reactor and handleMsg for RoundState (forward-port #7994 #7992) (#8139)
- Reduce size of test fixtures and logging rate (#8172)
- Avoid panic during shutdown (#8170)
- Cleanup tempfile explictly (#8184)
- Add leaktest check to replay tests (#8185)
- Update state machine to use the new consensus params (#8181)
- Add some more checks to vote counting (#7253) (#7262)
- Timeout params in toml used as overrides (#8186)
- Additional timing metrics (backport #7849) (#7875)
- Remove string indented function (#8257)
- Avoid panics during handshake (#8266)
- Add nil check to gossip routine (#8288)
- Reduce size of validator set changes test (#8442)

### Crypto

- Remove unused code (#8412)
- Cleanup tmhash package (#8434)

### E2e

- Stabilize validator update form (#7340) (#7351)
- Clarify apphash reporting (#7348) (#7352)
- Generate keys for more stable load (#7344) (#7353)
- App hash test cleanup (0.35 backport) (#7350)
- Fix hashing for app + Fix logic of TestApp_Hash (#8229)

### Eventbus

- Publish without contexts (#8369)

### Events

- Remove service aspects of event switch (#8146)
- Remove unused event code (#8313)

### Evidence

- Manage and initialize state objects more clearly in the pool (#8080)
- Remove source of non-determinism from test (#7266) (#7268)

### Fuzz

- Don't panic on expected errors (#8423)

### Internal/libs/protoio

- Optimize MarshalDelimited by plain byteslice allocations+sync.Pool (#7325) (#7426)

### Internal/proxy

- Add initial set of abci metrics backport (#7342)

### Keymigrate

- Fix decoding of block-hash row keys (#8294)
- Fix conversion of transaction hash keys (#8352)

### Libs/clist

- Remove unused surface area (#8134)

### Libs/events

- Remove unneccessary unsubscription code (#8135)

### Libs/log

- Remove Must constructor (#8120)

### Light

- Remove untracked close channel (#8228)

### Lint

- Remove lll check (#7346) (#7357)
- Bump linter version in ci (#8234)

### Mempool

- Test harness should expose application (#8143)
- Reduce size of test (#8152)

### Migration

- Remove stale seen commits (#8205)

### Node

- Excise node handle within rpc env (#8063)
- Nodes should fetch state on startup (#8062)
- Pass eventbus at construction time (#8084)
- Cleanup evidence db (#8119)
- Always sync with the application at startup (#8159)
- Remove channel and peer update initialization from construction (#8238)
- Reorder service construction (#8262)
- Move handshake out of constructor (#8264)
- Use signals rather than ephemeral contexts (#8376)
- Cleanup setup for indexer and evidence components (#8378)
- Start rpc service after reactors (#8426)

### Node+statesync

- Normalize initialization (#8275)

### P2p

- Update polling interval calculation for PEX requests (#8106)
- Remove unnecessary panic handling in PEX reactor (#8110)
- Adjust max non-persistent peer score (#8137)
- Reduce peer score for dial failures (backport #7265) (#7271)
- Plumb rudamentary service discovery to rectors and update statesync (backport #8030) (#8036)
- Update shim to transfer information about peers (#8047)
- Inject nodeinfo into router (#8261)
- Fix setting in con-tracker (#8370)
- Remove support for multiple transports and endpoints (#8420)
- Use nodeinfo less often (#8427)
- Avoid using p2p.Channel internals (#8444)

### P2p+flowrate

- Rate control refactor (#7828)

### Privval/grpc

- Normalize signature (#8441)

### Proto

- Update proto generation to use buf (#7975)

### Proxy

- Collapse triforcated abci.Client (#8067)

### Pubsub

- Report a non-nil error when shutting down. (#7310)
- [minor] remove unused stub method (#8316)

### Readme

- Add vocdoni (#8117)

### Rfc

- RFC 015 ABCI++ Tx Mutation (#8033)

### Rollback

- Cleanup second node during test (#8175)

### Rpc

- Backport experimental buffer size control parameters from #7230 (tm v0.35.x) (#7276)
- Implement header and header_by_hash queries (backport #7270) (#7367)
- Add more nil checks in the status end point (#8287)
- Avoid leaking threads (#8328)
- Reformat method signatures and use a context (#8377)
- Fix byte string decoding for URL parameters (#8431)

### Scmigrate

- Ensure target key is correctly renamed (#8276)

### Service

- Add NopService and use for PexReactor (#8100)
- Minor cleanup of comments (#8314)

### State

- Avoid panics for marshaling errors (#8125)
- Panic on ResponsePrepareProposal validation error (#8145)
- Propogate error from state store (#8171)
- Avoid premature genericism (#8224)
- Remove unused weighted time (#8315)

### Statesync

- Avoid leaking a thread during tests (#8085)
- Assert app version matches (backport #7856) (#7886)
- Avoid compounding retry logic for fetching consensus parameters (backport #8032) (#8041)
- Merge channel processing (#8240)
- Tweak test performance (#8267)

### Statesync+blocksync

- Move event publications into the sync operations (#8274)

### Types

- Update synchrony params to match checked in proto (#8142)
- Minor cleanup of un or minimally used types (#8154)
- Add TimeoutParams into ConsensusParams structs (#8177)
- Fix path handling in node key tests (#7493) (#7502)

## [0.35.2] - 2022-03-02

### ADR

- Synchronize PBTS ADR with spec (#7764)

### Bug Fixes

- Detect and fix data-race in MockPV (#262)
- Race condition when logging (#271)
- Decrease memory used by debug logs (#280)
- Tendermint stops when validator node id lookup fails (#279)

### Documentation

- Fix some typos in ADR 075. (#7726)
- Drop v0.32 from the doc site configuration (#7741)
- Fix RPC output examples for GET queries (#7799)
- Fix ToC file extension for RFC 004. (#7813)
- Rename RFC 008 (#7841)
- Fix broken markdown links (cherry-pick of #7847) (#7848)
- Fix broken markdown links (#7847)
- Update spec links to point to tendermint/tendermint (#7851)
- Remove unnecessary os.Exit calls at the end of main (#7861)
- Fix misspelled file name (#7863)
- Remove spec section from v0.35 docs (#7899)
- Pin the RPC docs to v0.35 instead of master (#7909)
- Pin the RPC docs to v0.35 instead of master (backport #7909) (#7911)
- Update repo and spec readme's (#7907)
- Redirect master links to the latest release version (#7936)
- Redirect master links to the latest release version (backport #7936) (#7954)
- Fix cosmos theme version. (#7966)
- Point docs/master to the same content as the latest release (backport #7980) (#7998)
- Fix some broken markdown links (#8021)
- Update ADR template (#7789)

### Miscellaneous Tasks

- Update changelog and version to 0.7.0
- Update unit tests after backport fo tendermint v0.35 (#245)
- Backport Tenderdash 0.7 to 0.8 (#246)
- Fix e2e tests and protxhash population (#273)
- Improve logging for debug purposes

### PBTS

- Spec reorganization, summary of changes on README.md (#399)

### RFC

- Add delete gas rfc (#7777)

### Refactor

- Change node's proTxHash on slice from pointer of slice (#263)
- Some minor changes in validate-conn-executor and routerDashDialer (#277)
- Populate proTxHash in address-book (#274)

### Security

- Bump github.com/prometheus/client_golang from 1.12.0 to 1.12.1 (#7732)
- Bump github.com/gorilla/websocket from 1.4.2 to 1.5.0 (#7829)
- Bump github.com/golangci/golangci-lint from 1.44.0 to 1.44.2 (#7854)
- Bump golangci/golangci-lint-action from 2.5.2 to 3.1.0 (#8026)

### Testing

- Reduce usage of the MustDefaultLogger constructor (#7960)

### Abci

- PrepareProposal (#6544)
- Vote Extension 1 (#6646)
- PrepareProposal-VoteExtension integration [2nd try] (#7821)
- Undo socket buffer limit (#7877)
- Make tendermint example+test clients manage a mutex (#7978)
- Remove lock protecting calls to the application interface (#7984)
- Use no-op loggers in the examples (#7996)
- Revert buffer limit change (#7990)

### Abci/client

- Remove vestigially captured context (#7839)
- Remove waitgroup for requests (#7842)
- Remove client-level callback (#7845)
- Make flush operation sync (#7857)
- Remove lingering async client code (#7876)

### Abci/kvstore

- Test cleanup improvements (#7991)

### Adr

- Merge tendermint/spec repository into tendermint/tendermint (#7775)

### Blocksync

- Shutdown cleanup (#7840)

### Build

- Bump github.com/prometheus/client_golang (#7731)
- Bump docker/build-push-action from 2.8.0 to 2.9.0 (#397)
- Bump docker/build-push-action from 2.8.0 to 2.9.0 (#7780)
- Bump github.com/gorilla/websocket from 1.4.2 to 1.5.0 (#7830)
- Bump docker/build-push-action from 2.7.0 to 2.9.0
- Bump github.com/gorilla/websocket from 1.4.2 to 1.5.0
- Bump github.com/rs/zerolog from 1.26.0 to 1.26.1
- Bump actions/github-script from 5 to 6
- Bump docker/login-action from 1.10.0 to 1.12.0
- Bump url-parse from 1.5.4 to 1.5.7 in /docs (#7855)
- Bump github.com/golangci/golangci-lint (#7853)
- Bump docker/login-action from 1.12.0 to 1.13.0
- Bump docker/login-action from 1.12.0 to 1.13.0 (#7890)
- Bump prismjs from 1.26.0 to 1.27.0 in /docs (#8022)
- Bump url-parse from 1.5.7 to 1.5.10 in /docs (#8023)

### Ci

- Fix super-linter configuration settings (#7708)
- Fix super-linter configuration settings (backport #7708) (#7710)

### Clist

- Remove unused waitgroup from clist implementation (#7843)

### Cmd

- Avoid package state in cli constructors (#7719)

### Cmd/debug

- Remove global variables and logging (#7957)

### Conensus

- Put timeouts on reactor tests (#7733)

### Config

- Add event subscription options and defaults (#7930)

### Consensus

- Use buffered channel in TestStateFullRound1 (#7668)
- Remove unused closer construct (#7734)
- Delay start of peer routines (#7753)
- Delay start of peer routines (backport of #7753) (#7760)
- Tie peer threads to peer lifecylce context (#7792)
- Refactor operations in consensus queryMaj23Routine (#7791)
- Refactor operations in consensus queryMaj23Routine (backport #7791) (#7793)
- Start the timeout ticker before replay (#7844)
- Additional timing metrics (#7849)
- Additional timing metrics (backport #7849) (#7875)
- Improve cleanup of wal tests (#7878)
- HasVoteMessage index boundary check (#7720)
- TestReactorValidatorSetChanges test fix (#7985)
- Make orchestration more reliable for invalid precommit test (#8013)
- Validator set changes test cleanup (#8035)

### Context

- Cleaning up context dead ends (#7963)

### E2e

- Plumb logging instance (#7958)
- Change ci network configuration (#7988)

### Evidence

- Refactored the evidence message to process Evidence instead of EvidenceList (#7700)

### Github

- Update e2e workflows (#7803)
- Add Informal code owners (#8042)

### Indexer

- Skip Docker tests when Docker is not available (#7814)

### Libs/cli

- Clean up package (#7806)

### Libs/events

- Remove unused event cache (#7807)

### Libs/service

- Regularize Stop semantics and concurrency primitives (#7809)

### Libs/strings

- Cleanup string helper function package (#7808)

### Light

- Fix absence proof verification by light client (#7639)
- Fix absence proof verification by light client (backport #7639) (#7716)
- Remove legacy timeout scheme (#7776)
- Remove legacy timeout scheme (backport #7776) (#7786)
- Avert a data race (#7888)

### Logging

- Allow logging level override (#7873)

### Math

- Remove panics in safe math ops (#7962)

### Mempool

- Return duplicate tx errors more consistently (#7714)
- Return duplicate tx errors more consistently (backport #7714) (#7718)
- IDs issue fixes (#7763)
- Remove duplicate tx message from reactor logs (#7795)
- Fix benchmark CheckTx for hitting the GetEvictableTxs call (#7796)
- Use checktx sync calls (#7868)

### Mempool+evidence

- Simplify cleanup (#7794)

### Metrics

- Add metric for proposal timestamp difference  (#7709)

### Node

- Allow orderly shutdown if context is canceled and gensis is in the future (#7817)
- Clarify unneccessary logic in seed constructor (#7818)
- Hook up eventlog and eventlog metrics (#7981)

### P2p

- Pass start time to flowrate and cleanup constructors (#7838)
- Make mconn transport test less flaky (#7973)
- Mconn track last message for pongs (#7995)
- Relax pong timeout (#8007)
- Backport changes in ping/pong tolerances (#8009)
- Retry failed connections slightly more aggressively (#8010)
- Retry failed connections slightly more aggressively (backport #8010) (#8012)
- Ignore transport close error during cleanup (#8011)
- Plumb rudamentary service discovery to rectors and update statesync (#8030)
- Plumb rudamentary service discovery to rectors and update statesync (backport #8030) (#8036)
- Re-enable tests previously disabled (#8049)
- Update shim to transfer information about peers (#8047)

### P2p/message

- Changed evidence message to contain evidence, not a list… (#394)

### Params

- Increase default synchrony params (#7704)

### Proto

- Merge the proposer-based timestamps parameters (#393)
- Abci++ changes (#348)

### Proxy

- Fix endblock metric (#7989)

### Pubsub

- Check for termination in UnsubscribeAll (#7820)

### Rfc

- P2p light client (#7672)

### Roadmap

- Update to better reflect v0.36 changes (#7774)

### Rpc

- Add application info to `status` call (#7701)
- Remove unused websocket options (#7712)
- Clean up unused non-default websocket client options (#7713)
- Don't route websocket-only methods on GET requests (#7715)
- Clean up encoding of request and response messages (#7721)
- Simplify and consolidate response construction (#7725)
- Clean up unmarshaling of batch-valued responses (#7728)
- Simplify the handling of JSON-RPC request and response IDs (#7738)
- Fix layout of endpoint list (#7742)
- Fix layout of endpoint list (#7742) (#7744)
- Remove the placeholder RunState type. (#7749)
- Allow GET parameters that support encoding.TextUnmarshaler (#7800)
- Remove unused latency metric (#7810)
- Implement the eventlog defined by ADR 075 (#7825)
- Implement the ADR 075 /events method (#7965)
- Set a minimum long-polling interval for Events (#8050)

### Rpc/client

- Add Events method to the client interface (#7982)
- Rewrite the WaitForOneEvent helper (#7986)
- Add eventstream helper (#7987)

### Service

- Change stop interface (#7816)

### Spec

- Merge spec repo into tendermint repo (#7804)
- Merge spec repo into tendermint repo (#7804)
- Minor updates to spec merge PR (#7835)

### State

- Synchronize the ProcessProposal implementation with the latest version of the spec (#7961)

### Statesync

- Relax timing (#7819)
- Assert app version matches (#7856)
- Assert app version matches (backport #7856) (#7886)
- Avoid compounding retry logic for fetching consensus parameters (#8032)
- Avoid compounding retry logic for fetching consensus parameters (backport #8032) (#8041)

### Sync+p2p

- Remove closer (#7805)

### Types

- Make timely predicate adaptive after 10 rounds (#7739)
- Remove nested evidence field from block (#7765)
- Add string format to 64-bit integer JSON fields (#7787)
- Add default values for the synchrony parameters (#7788)

### Types/events+evidence

- Emit events + metrics on evidence validation (#7802)

## [0.35.1] - 2022-01-26

### ABCI++

- Major refactor of spec's structure. Addressed Josef's comments. Merged ABCI's methods and data structs that didn't change. Added introductory paragraphs
- Found a solution to set the execution mode

### ADR

- Update the proposer-based timestamp spec per discussion with @cason (#7153)

### ADR-74

- Migrate Timeout Parameters to Consensus Parameters (#7503)

### Bug Fixes

- Panic on precommits does not have any +2/3 votes
- Improved error handling  in DashCoreSignerClient
- Abci/example, cmd and test packages were fixed after the upstream backport
- Some fixes to be able to compile the add
- Some fixes made by PR feedback
- Use types.DefaultDashVotingPower rather than internal dashDefaultVotingPower
- Don't disconnect already disconnected validators

### Documentation

- Clarify where doc site config settings must land (#7289)
- Add abci timing metrics to the metrics docs (#7311)
- Go tutorial fixed for 0.35.0 version (#7329) (#7330)
- Go tutorial fixed for 0.35.0 version (#7329) (#7330) (#7331)
- Update go ws code snippets (#7486)
- Update go ws code snippets (#7486) (#7487)
- Fixup the builtin tutorial  (#7488)

### Features

- Improve logging for better elasticsearch compatibility (#220)
- InitChain can set initial core lock height (#222)
- Add empty block on h-1 and h-2 apphash change (#241)
- Inter-validator set communication (#187)
- Add create_proof_block_range config option (#243)

### Miscellaneous Tasks

- Create only 1 proof block by default
- Release script and initial changelog (#250)
- [**breaking**] Bump ABCI version and update release.sh to change TMVersionDefault automatically (#253)
- Eliminate compile errors after backport of tendermint 0.35 (#238)

### PBTS

- New minimal set of changes in consensus algorithm (#369)
- New system model and problem statement (#375)

### RFC-009

- Consensus Parameter Upgrades (#7524)

### RFC006

- Semantic Versioning (#365)

### Refactor

- Apply peer review feedback

### Security

- Bump google.golang.org/grpc from 1.41.0 to 1.42.0 (#7200)
- Bump github.com/lib/pq from 1.10.3 to 1.10.4 (#7261)
- Bump github.com/tendermint/tm-db from 0.6.4 to 0.6.6 (#7287)
- Bump actions/cache from 2.1.6 to 2.1.7 (#7334)
- Bump watchpack from 2.2.0 to 2.3.0 in /docs (#7335)
- Bump github.com/adlio/schema from 1.1.14 to 1.1.15 (#7407)
- Bump github.com/adlio/schema from 1.2.2 to 1.2.3 (#7432)
- Bump github.com/spf13/viper from 1.9.0 to 1.10.0 (#7434)
- Bump github.com/spf13/cobra from 1.2.1 to 1.3.0 (#7456)
- Bump google.golang.org/grpc from 1.42.0 to 1.43.0 (#7455)
- Bump github.com/spf13/viper from 1.10.0 to 1.10.1 (#7470)
- Bump docker/login-action from 1.10.0 to 1.12.0 (#7494)
- Bump github.com/BurntSushi/toml from 0.4.1 to 1.0.0 (#7562)
- Bump docker/build-push-action from 2.7.0 to 2.8.0 (#7679)
- Bump github.com/vektra/mockery/v2 from 2.9.4 to 2.10.0 (#7685)
- Bump github.com/golangci/golangci-lint from 1.43.0 to 1.44.0 (#7692)

### Testing

- Ensure commit stateid in wal is OK
- Add testing.T logger connector (#7447)
- Use scoped logger for all public packages (#7504)
- Pass testing.T to assert and require always, assertion cleanup (#7508)
- Remove background contexts (#7509)
- Remove panics from test fixtures (#7522)
- Pass testing.T around rather than errors for test fixtures (#7518)
- Uniquify prom IDs (#7540)
- Remove in-test logging (#7558)
- Use noop loger with leakteset in more places (#7604)
- Update docker versions to match build version (#7646)
- Update cleanup opertunities (#7647)
- Reduce timeout to 4m from 8m (#7681)

### Abci

- Socket server shutdown response handler (#7547)

### Abci/client

- Use a no-op logger in the test (#7633)
- Simplify client interface (#7607)

### Acbi

- Fix readme link to protocol buffers (#362)

### Adr

- Lib2p implementation plan (#7282)

### Autofile

- Ensure files are not reopened after closing (#7628)
- Avoid shutdown race (#7650)

### Backport

- Add basic metrics to the indexer package. (#7250) (#7252)

### Blocksync

- Standardize construction process (#7531)

### Build

- Bump google.golang.org/grpc from 1.41.0 to 1.42.0 (#7218)
- Bump github.com/lib/pq from 1.10.3 to 1.10.4
- Run e2e tests in parallel
- Bump github.com/lib/pq from 1.10.3 to 1.10.4 (#7260)
- Bump technote-space/get-diff-action from 5.0.1 to 5.0.2
- Bump github.com/tendermint/tm-db from 0.6.4 to 0.6.6 (#7285)
- Update the builder image location. (#364)
- Update location of proto builder image (#7296)
- Declare packages variable in correct makefile (#7402)
- Bump github.com/adlio/schema from 1.1.14 to 1.1.15 (#7406)
- Bump github.com/adlio/schema from 1.1.14 to 1.1.15
- Bump github.com/adlio/schema from 1.1.15 to 1.2.2 (#7423)
- Bump github.com/adlio/schema from 1.1.15 to 1.2.2 (#7422)
- Bump github.com/adlio/schema from 1.1.15 to 1.2.3
- Bump github.com/spf13/viper from 1.9.0 to 1.10.0 (#7435)
- Bump github.com/adlio/schema from 1.2.2 to 1.2.3 (#7436)
- Bump watchpack from 2.3.0 to 2.3.1 in /docs (#7430)
- Bump google.golang.org/grpc from 1.42.0 to 1.43.0 (#7458)
- Bump github.com/spf13/cobra from 1.2.1 to 1.3.0 (#7457)
- Bump github.com/rs/zerolog from 1.26.0 to 1.26.1 (#7467)
- Bump github.com/rs/zerolog from 1.26.0 to 1.26.1 (#7469)
- Bump github.com/spf13/viper from 1.10.0 to 1.10.1 (#7468)
- Bump docker/login-action from 1.10.0 to 1.11.0 (#378)
- Bump github.com/rs/cors from 1.8.0 to 1.8.2
- Bump docker/login-action from 1.11.0 to 1.12.0 (#380)
- Bump github.com/rs/cors from 1.8.0 to 1.8.2 (#7484)
- Bump github.com/rs/cors from 1.8.0 to 1.8.2 (#7485)
- Bump technote-space/get-diff-action from 5 to 6.0.1 (#7535)
- Bump github.com/BurntSushi/toml from 0.4.1 to 1.0.0 (#7560)
- Make sure to test packages with external tests (#7608)
- Make sure to test packages with external tests (backport #7608) (#7635)
- Bump github.com/prometheus/client_golang (#7636)
- Bump github.com/prometheus/client_golang (#7637)
- Bump docker/build-push-action from 2.7.0 to 2.8.0 (#389)
- Bump github.com/prometheus/client_golang (#249)
- Bump github.com/BurntSushi/toml from 0.4.1 to 1.0.0
- Bump vuepress-theme-cosmos from 1.0.182 to 1.0.183 in /docs (#7680)
- Bump github.com/vektra/mockery/v2 from 2.9.4 to 2.10.0 (#7684)
- Bump google.golang.org/grpc from 1.43.0 to 1.44.0 (#7693)
- Bump github.com/golangci/golangci-lint (#7696)
- Bump google.golang.org/grpc from 1.43.0 to 1.44.0 (#7695)

### Ci

- Move test execution to makefile (#7372)
- Move test execution to makefile (#7372) (#7374)
- Cleanup build/test targets (#7393)
- Fix missing dependency (#7396)
- Cleanup build/test targets (backport #7393) (#7395)
- Skip docker image builds during PRs (#7397)
- Skip docker image builds during PRs (#7397) (#7398)
- Tweak e2e configuration (#7400)

### Clist

- Reduce size of test workload for clist implementation (#7682)

### Cmd

- Add integration test and fix bug in rollback command (#7315)
- Cosmetic changes for errors and print statements (#7377)
- Cosmetic changes for errors and print statements (#7377) (#7408)
- Add integration test for rollback functionality (backport #7315) (#7369)

### Config

- Add a Deprecation annotation to P2PConfig.Seeds. (#7496)
- Add a Deprecation annotation to P2PConfig.Seeds. (#7496) (#7497)

### Consensus

- Add some more checks to vote counting (#7253)
- Add some more checks to vote counting (#7253) (#7262)
- Remove reactor options (#7526)
- Use noop logger for WAL test (#7580)
- Explicit test timeout (#7585)
- Test shutdown to avoid hangs (#7603)
- Calculate prevote message delay metric (#7551)
- Check proposal non-nil in prevote message delay metric (#7625)
- Calculate prevote message delay metric (backport #7551) (#7618)
- Check proposal non-nil in prevote message delay metric (#7625) (#7632)
- Use delivertxsync (#7616)
- Fix height advances in test state (#7648)

### Consensus+p2p

- Change how consensus reactor is constructed (#7525)

### Consensus/state

- Avert a data race with state update and tests (#7643)

### Contexts

- Remove all TODO instances (#7466)

### E2e

- Control access to state in Info calls (#7345)
- More clear height test (#7347)
- Stabilize validator update form (#7340)
- Stabilize validator update form (#7340) (#7351)
- Clarify apphash reporting (#7348)
- Clarify apphash reporting (#7348) (#7352)
- Generate keys for more stable load (#7344)
- Generate keys for more stable load (#7344) (#7353)
- App hash test cleanup (0.35 backport) (#7350)
- Limit legacyp2p and statesyncp2p (#7361)
- Use more simple strings for generated transactions (#7513)
- Avoid global test context (#7512)
- Use more simple strings for generated transactions (#7513) (#7514)
- Constrain test parallelism and reporting (#7516)
- Constrain test parallelism and reporting (backport #7516) (#7517)
- Make tx test more stable (#7523)
- Make tx test more stable (backport #7523) (#7527)

### Errors

- Formating cleanup (#7507)

### Eventbus

- Plumb contexts (#7337)

### Evidence

- Remove source of non-determinism from test (#7266)
- Remove source of non-determinism from test (#7266) (#7268)
- Reactor constructor (#7533)

### Internal/libs

- Delete unused functionality (#7569)

### Internal/libs/protoio

- Optimize MarshalDelimited by plain byteslice allocations+sync.Pool (#7325)
- Optimize MarshalDelimited by plain byteslice allocations+sync.Pool (#7325) (#7426)

### Internal/proxy

- Add initial set of abci metrics backport (#7342)

### Jsontypes

- Improve tests and error diagnostics (#7669)

### Libs/os

- Remove arbitrary os.Exit (#7284)
- Remove trap signal (#7515)

### Libs/rand

- Remove custom seed function (#7473)

### Libs/service

- Pass logger explicitly (#7288)

### Light

- Remove global context from tests (#7505)
- Avoid panic for integer underflow (#7589)
- Remove test panic (#7588)
- Convert validation panics to errors (#7597)
- Fix provider error plumbing (#7610)
- Return light client status on rpc /status  (#7536)

### Lint

- Remove lll check (#7346)
- Remove lll check (#7346) (#7357)

### Log

- Dissallow nil loggers (#7445)
- Remove support for traces (#7542)
- Avoid use of legacy test logging (#7583)

### Logging

- Remove reamining instances of SetLogger interface (#7572)

### Mempool

- Avoid arbitrary background contexts (#7409)
- Refactor mempool constructor (#7530)
- Reactor concurrency test tweaks (#7651)

### Node

- Minor package cleanups (#7444)
- New concrete type for seed node implementation (#7521)
- Move seed node implementation to its own file (#7566)
- Collapse initialization internals (#7567)

### Node+autofile

- Avoid leaks detected during WAL shutdown (#7599)

### Node+consensus

- Handshaker initialization (#7283)

### Node+privval

- Refactor privval construction (#7574)

### Node+rpc

- Rpc environment should own it's creation (#7573)

### P2p

- Reduce peer score for dial failures (#7265)
- Reduce peer score for dial failures (backport #7265) (#7271)
- Remove unused trust package (#7359)
- Implement interface for p2p.Channel without channels (#7378)
- Remove unneeded close channels from p2p layer (#7392)
- Migrate to use new interface for channel errors (#7403)
- Refactor channel Send/out (#7414)
- Use recieve for channel iteration (#7425)
- Always advertise self, to enable mutual address discovery (#7620)
- Always advertise self, to enable mutual address discovery (#7594)

### P2p/upnp

- Remove unused functionality (#7379)

### Pex

- Improve goroutine lifecycle (#7343)
- Regularize reactor constructor (#7532)
- Avert a data race on map access in the reactor (#7614)
- Do not send nil envelopes to the reactor (#7622)
- Improve handling of closed channels (#7623)

### Privval

- Remove panics in privval implementation (#7475)
- Improve test hygine (#7511)
- Improve client shutdown to prevent resource leak (#7544)
- Synchronize leak check with shutdown (#7629)
- Do not use old proposal timestamp (#7621)
- Avoid re-signing vote when RHS and signbytes are equal (#7592)

### Proto

- Update the mechanism for generating protos from spec repo (#7269)
- Abci++ changes (#348)
- Rebuild the proto files from the spec repository (#7291)

### Protoio

- Fix incorrect test assertion (#7606)

### Pubsub

- Move indexing out of the primary subscription path (#7231)
- Report a non-nil error when shutting down. (#7310)
- Make the queue unwritable after shutdown. (#7316)
- Use concrete queries instead of an interface (#7686)

### Reactors

- Skip log on some routine cancels (#7556)

### Rfc

- Deterministic proto bytes serialization (#7427)
- Don't panic (#7472)

### Rpc

- Fix inappropriate http request log (#7244)
- Backport experimental buffer size control parameters from #7230 (tm v0.35.x) (#7276)
- Implement header and header_by_hash queries (#7270)
- Implement header and header_by_hash queries (backport #7270) (#7367)
- Remove positional parameter encoding from clients (#7545)
- Collapse Caller and HTTPClient interfaces. (#7548)
- Simplify the JSON-RPC client Caller interface (#7549)
- Replace anonymous arguments with structured types (#7552)
- Refactor the HTTP POST handler (#7555)
- Replace custom context-like argument with context.Context (#7559)
- Remove cache control settings from the HTTP server (#7568)
- Fix mock test cases (#7571)
- Rework how responses are written back via HTTP (#7575)
- Simplify panic recovery in the server middleware (#7578)
- Consolidate RPC route map construction (#7582)
- Clean up the RPCFunc constructor signature (#7586)
- Check RPC service functions more carefully (#7587)
- Update fuzz criteria to match the implementation (#7595)
- Remove dependency of URL (GET) requests on tmjson (#7590)
- Simplify the encoding of interface-typed arguments in JSON (#7600)
- Paginate mempool /unconfirmed_txs endpoint (#7612)
- Use encoding/json rather than tmjson (#7670)
- Check error code for broadcast_tx_commit (#7683)
- Check error code for broadcast_tx_commit (#7683) (#7688)

### Service

- Remove stop method and use contexts (#7292)
- Remove quit method (#7293)
- Cleanup base implementation and some caller implementations (#7301)
- Plumb contexts to all (most) threads (#7363)
- Remove exported logger from base implemenation (#7381)
- Cleanup close channel in reactors (#7399)
- Cleanup mempool and peer update shutdown (#7401)
- Avoid debug logs before error (#7564)

### State

- Pass connected context (#7410)

### Statesync

- Assert app version matches (#7463)
- Reactor and channel construction (#7529)
- Use specific testing.T logger for tests (#7543)
- Clarify test cleanup (#7565)
- SyncAny test buffering (#7570)
- More orderly dispatcher shutdown (#7601)

### Sync

- Remove special mutexes (#7438)

### Tools

- Remove tm-signer-harness (#7370)

### Tools/tm-signer-harness

- Switch to not use hardcoded bytes for configs in test (#7362)

### Types

- Fix path handling in node key tests (#7493)
- Fix path handling in node key tests (#7493) (#7502)
- Remove panic from block methods (#7501)
- Tests should not panic (#7506)
- Rename and extend the EventData interface (#7687)

## [0.35.0] - 2021-11-04

### Bug Fixes

- Change CI testnet config from ci.toml on dashcore.toml
- Update the title of pipeline task
- Ensure seed at least once connects to another seed (#200)

### Documentation

- Set up Dependabot on new backport branches. (#7227)
- Update bounty links (#7203)
- Add description about how to keep validators public keys at full node
- Add information how to sue preset for network generation
- Change a type of code block
- Add upgrading info about node service (#7241)
- Add upgrading info about node service (#7241) (#7242)

### Features

- Add two more CI pipeline tasks to run e2e rotate.toml
- Reset full-node pub-keys
- Manual backport the upstream commit b69ac23fd20bdc00dea00c7c8a69fa66f2e675a9
- Update CHANGELOG_PENDING.md

### Refactor

- Minor formatting improvements

### Security

- Bump actions/checkout from 2.3.5 to 2.4.0 (#7199)
- Bump github.com/golangci/golangci-lint from 1.42.1 to 1.43.0 (#7219)

### Testing

- Clean up databases in tests (#6304)
- Improve cleanup for data and disk use (#6311)
- Close db in randConsensusNetWithPeers, just as it is in randConsensusNet

### Build

- Bump github.com/rs/zerolog from 1.25.0 to 1.26.0 (#7192)
- Github workflows: fix dependabot and code coverage (#191)
- Bump github.com/adlio/schema from 1.1.13 to 1.1.14
- Bump github.com/adlio/schema from 1.1.13 to 1.1.14 (#7217)
- Bump github.com/golangci/golangci-lint (#7224)
- Bump github.com/rs/zerolog from 1.25.0 to 1.26.0 (#7222)

### Ci

- Update dependabot configuration (#7204)
- Backport lint configuration changes (#7226)

### Consensus

- Remove stale WAL benchmark (#7194)

### E2e

- Add option to dump and analyze core dumps

### Fuzz

- Remove fuzz cases for deleted code (#7187)

### Lint

- Cleanup branch lint errors (#7238)

### Node

- Cleanup construction (#7191)

### Pex

- Allow disabled pex reactor (#7198)
- Allow disabled pex reactor (backport #7198) (#7201)
- Avoid starting reactor twice (#7239)

### Pubsub

- Use a dynamic queue for buffered subscriptions (#7177)
- Remove uninformative publisher benchmarks. (#7195)

## [0.35.0-rc4] - 2021-10-29

### Bug Fixes

- Accessing validator state safetly
- Safe state access in TestValidProposalChainLocks
- Safe state access in TestReactorInvalidBlockChainLock
- Safe state access in TestReactorInvalidBlockChainLock
- Seeds should not hang when disconnected from all nodes

### Documentation

- Add roadmap to repo (#7107)
- Add reactor sections (#6510)
- Add reactor sections (backport #6510) (#7151)
- Fix broken links and layout (#7154)
- Fix broken links and layout (#7154) (#7163)

### Security

- Bump actions/checkout from 2.3.4 to 2.3.5 (#7139)
- Bump prismjs from 1.23.0 to 1.25.0 in /docs (#7168)
- Bump postcss from 7.0.35 to 7.0.39 in /docs (#7167)
- Bump ws from 6.2.1 to 6.2.2 in /docs (#7165)
- Bump path-parse from 1.0.6 to 1.0.7 in /docs (#7164)
- Bump url-parse from 1.5.1 to 1.5.3 in /docs (#7166)

### Testing

- Regenerate  remote_client mock
- Get rid of workarounds for issues fixed in 0.6.1

### Abci

- Fix readme link (#7173)

### Blocksync

- Remove v0 folder structure (#7128)

### Buf

- Modify buf.yml, add buf generate (#5653)
- Modify buf.yml, add buf generate (#5653)

### Build

- Bump rtCamp/action-slack-notify from 2.1.1 to 2.2.0
- Fix proto-lint step in Makefile
- Fix proto-lint step in Makefile

### Config

- WriteConfigFile should return error (#7169)
- Expose ability to write config to arbitrary paths (#7174)
- Backport file writing changes (#7182)

### E2e

- Always enable blocksync (#7144)
- Avoid unset defaults in generated tests (#7145)
- Evidence test refactor (#7146)

### Flowrate

- Cleanup unused files (#7158)

### Light

- Fix panic when empty commit is received from server

### Mempool

- Remove panic when recheck-tx was not sent to ABCI application (#7134)
- Remove panic when recheck-tx was not sent to ABCI application (#7134) (#7142)
- Port reactor tests from legacy implementation (#7162)
- Consoldate implementations (#7171)

### Node,blocksync,config

- Remove support for running nodes with blocksync disabled (#7159)

### P2p

- Refactor channel description (#7130)
- Channel shim cleanup (#7129)
- Flatten channel descriptor (#7132)
- Simplify open channel interface (#7133)
- Remove final shims from p2p package (#7136)
- Use correct transport configuration (#7152)
- Add message type into the send/recv bytes metrics (#7155)
- Transport should be captive resposibility of router (#7160)
- Add message type into the send/recv bytes metrics (backport #7155) (#7161)

### Pex

- Remove legacy proto messages (#7147)

### Pubsub

- Simplify and improve server concurrency handling (#7070)
- Use distinct client IDs for test subscriptions. (#7178)
- Use distinct client IDs for test subscriptions. (#7178) (#7179)

### State

- Add height assertion to rollback function (#7143)
- Add height assertion to rollback function (#7143) (#7148)

### Tools

- Clone proto files from spec (#6976)

## [0.6.0] - 2021-10-14

### Documentation

- StateID verification algorithm

### Miscellaneous Tasks

- Bump version to 0.6.0 (#185)

### Refactor

- Assignment copies lock value (#7108)

### Testing

- StateID verify with blocks N and N+1
- Cleanup rpc/client and node test fixtures (#7112)
- Install abci-cli when running make tests_integrations (#6834)

### Abci

- Change client to use multi-reader mutexes (backport #6306) (#6873)

### Build

- Replace github.com/go-kit/kit/log with github.com/go-kit/log
- Fix build-docker to include the full context. (#7114)
- Fix build-docker to include the full context. (#7114) (#7116)

### Changelog

- Add 0.34.14 updates (#7117)

### Ci

- Use run-multiple.sh for e2e pr tests (#7111)

### Cleanup

- Remove not needed binary test/app/grpc_client

### Cli

- Allow node operator to rollback last state (#7033)
- Allow node operator to rollback last state (backport #7033) (#7081)

### Config

- Add example on external_address (backport #6621) (#6624)

### E2e

- Improve network connectivity (#7077)
- Abci protocol should be consistent across networks (#7078)
- Abci protocol should be consistent across networks (#7078) (#7086)
- Light nodes should use builtin abci app (#7095)
- Light nodes should use builtin abci app (#7095) (#7097)
- Disable app tests for light client (#6672)
- Avoid starting nodes from the future (#6835) (#6838)
- Cleanup node start function (#6842) (#6848)

### Internal/consensus

- Update error log (#6863) (#6867)

### Internal/proxy

- Add initial set of abci metrics (#7115)

### Light

- Update links in package docs. (#7099)
- Update links in package docs. (#7099) (#7101)
- Fix early erroring (#6905)

### Lint

- Fix collection of stale errors (#7090)

### Node

- Always close database engine (#7113)

### P2p

- Cleanup transport interface (#7071)
- Cleanup unused arguments (#7079)
- Rename pexV2 to pex (#7088)
- Fix priority queue bytes pending calculation (#7120)

### Pex

- Update pex messages (#352)

### Rpc

- Add chunked rpc interface (backport #6445) (#6717)
- Move evidence tests to shared fixtures (#7119)
- Remove the deprecated gRPC interface to the RPC service (#7121)
- Fix typo in broadcast commit (#7124)

### Statesync

- Improve stateprovider handling in the syncer (backport) (#6881)

## [0.35.0-rc3] - 2021-10-06

### Documentation

- Adr-d001: clarify terms based on peer  review
- Create separate releases doc (#7040)
- Adr-d001 apply peer review comments

### Features

- Use proto.Copy function to copy a message

### Testing

- Add some StateID AppHash and Height assertions

### Blocksync/v2

- Remove unsupported reactor (#7046)

### Build

- Bump github.com/adlio/schema from 1.1.13 to 1.1.14 (#7069)

### Ci

- Mergify support for 0.35 backports (#7050)
- 0.35.x nightly should run from master and checkout the release branch (#7067)
- Fix p2p configuration for e2e tests (#7066)

### Consensus

- Wait until peerUpdates channel is closed to close remaining peers (#7058)
- Wait until peerUpdates channel is closed to close remaining peers (#7058) (#7060)

### E2e

- Automatically prune old app snapshots (#7034)
- Automatically prune old app snapshots (#7034) (#7063)

### Mempool,rpc

- Add removetx rpc method (#7047)
- Add removetx rpc method (#7047) (#7065)

### P2p

- Delete legacy stack initial pass (#7035)
- Remove wdrr queue (#7064)

### Scripts

- Fix authors script to take a ref (#7051)

## [0.36.0-dev] - 2021-10-04

### .github

- Remove tessr and bez from codeowners (#7028)

### Bug Fixes

- Fix MD after the lint
- To avoid potential race conditions the validator-set-update is needed to copy rather than using the pointer to the field at PersistentKVStoreApplication, 'cause it leads to a race condition
- Update a comment block

### Documentation

- ADR: Inter Validator Set Messaging
- Adr-d001: apllied feedback, added additional info
- Adr-d001 clarified abci protocol changes
- Adr-d001 describe 3 scenarios and minor restructure

### Features

- Fix coping of PubKey pointer

### Blocksync

- Fix shutdown deadlock issue (#7030)

### Build

- Update all deps to most recent version

### Consensus

- Avoid unbuffered channel in state test (#7025)

### E2e

- Use network size in load generator (#7019)
- Generator ensure p2p modes (#7021)

### Statesync

- Ensure test network properly configured (#7026)
- Remove deadlock on init fail (#7029)
- Improve rare p2p race condition (#7042)

## [0.35.0-rc2] - 2021-09-28

### Bug Fixes

- Amd64 build and arm build ci
- State sync locks when trying to retrieve AppHash
- Set correct LastStateID when updating block
- StateID - update tests (WIP, some still red)
- Ractor should validate StateID correctly + other fixes
- StateID in light client implementation
- Tests sometimes fail on connection attempt
- App hash size validation + remove unused code
- Invalid generation of  tmproto.StateID request id
- State sync locks when trying to retrieve AppHash
- Correctly handle state ID of initial block
- Don't use state to verify blocks from mempool
- Incorrect state id for first block
- AppHashSize is inconsistent
- Support initial height != 1
- E2e: workaround for "chain stalled at unknown height"
- Update dashcore network config, add validator01 to validator_update.0 and add all available validators to 1010 height
- Cleanup e2e Readme.md
- Remove height 1008 from dashcore
- Race condition in p2p_switch and pex_reactor (#7015)
- Race condition in p2p_switch and pex_reactor (#7015)

### Documentation

- Add documentation of unsafe_flush_mempool to openapi (#6947)
- Fix openapi yaml lint (#6948)
- State ID
- State-id.md typos and grammar
- Remove invalid info about initial state id
- Add some code comments

### Features

- Info field with arbitrary data to ResultBroadcastTx
- Add ProposedBlockGTimeWindow in a config

### Fix

- Benchmark tests slow down light client tests

### Refactor

- E2e docker: build bls in separate layer
- Golangci-lint + minor test improvements
- Minor formatting updates
- E2e docker: build bls in separate layer
- Add ErrInvalidVoteSignature
- S/GetStateID()/StateID()/
- Code style changes after peer review
- Move stateid to separate file
- Remove unused message CanonicalStateVote
- Use types instead of pb StateID in SignVote and Evidence
- Inverse behaviour of resetting fullnode pubkeys from FULLNODE_PUBKEY_RESET to FULLNODE_PUBKEY_KEEP env
- Add runner/rotate task to simplify running rotate network

### Security

- Bump github.com/rs/zerolog from 1.24.0 to 1.25.0 (#6923)

### Testing

- Add StateID unit tests
- Check if wrong state ID fails VoteAdd()
- Fix: TestStateBadProposal didn't copy slices correctly
- TestHandshakePanicsIfAppReturnsWrongAppHash fixed
- Change apphash for every message
- Workaround for e2e tests starting too fast
- Consensus tests use random initial height
- Non-nil genesis apphash in genesis tests
- Add tests for initial height != 1 to consensus
- Fix: replay_test.go fails due to invalid height processing

### Abci

- Flush socket requests and responses immediately. (#6997)

### Add

- Update e2e doc

### Build

- Bump codecov/codecov-action from 2.0.3 to 2.1.0 (#6938)
- Bump github.com/vektra/mockery/v2 from 2.9.0 to 2.9.3 (#6951)
- Bump github.com/vektra/mockery/v2 from 2.9.3 to 2.9.4 (#6956)
- Bump github.com/spf13/viper from 1.8.1 to 1.9.0 (#6961)
- E2e docker app can be run with dlv debugger
- Improve e2e docker container debugging
- Bump github.com/go-kit/kit from 0.11.0 to 0.12.0 (#6988)
- Bump google.golang.org/grpc from 1.40.0 to 1.41.0 (#7003)

### Changelog

- Add entry for interanlizations (#6989)

### Ci

- Drop codecov bot (#6917)
- Tweak code coverage settings (#6920)
- Disable codecov patch status check (#6930)
- Skip coverage for non-go changes (#6927)
- Skip coverage tasks for test infrastructure (#6934)
- Reduce number of groups for 0.34 e2e runs (#6968)
- Use smart merges (#6993)
- Use cheaper codecov data collection (#7009)

### Cleanup

- Reduce and normalize import path aliasing. (#6975)

### Config/docs

- Update and deprecated (#6879)

### Crypto/armor

- Remove unused package (#6963)

### E2e

- Introduce canonical ordering of manifests (#6918)
- Load generation and logging changes (#6912)
- Increase retain height to at least twice evidence age (#6924)
- Test multiple broadcast tx methods (#6925)
- Compile tests (#6926)
- Improve p2p mode selection (#6929)
- Reduce load volume (#6932)
- Slow load processes with longer evidence timeouts (#6936)
- Reduce load pressure (#6939)
- Tweak semantics of waitForHeight (#6943)
- Skip broadcastTxCommit check (#6949)
- Allow load generator to succed for short tests (#6952)
- Cleanup on all errors if preserve not specified (#6950)
- Run multiple should use preserve (#6972)
- Improve manifest sorting algorithim (#6979)
- Only check validator sets after statesync (#6980)
- Always preserve failed networks (#6981)
- Load should be proportional to network (#6983)
- Avoid non-determinism in app hash check (#6985)
- Tighten timing for load generation (#6990)
- Skip validation of status apphash (#6991)
- Do not inject evidence through light proxy (#6992)
- Add limit and sort to generator (#6998)
- Reduce number of statesyncs in test networks (#6999)
- Improve chances of statesyncing success (#7001)
- Allow running of single node using the e2e app (#6982)
- Reduce log noise (#7004)
- Avoid seed nodes when statesyncing (#7006)
- Add generator tests (#7008)
- Reduce number of stateless nodes in test networks (#7010)
- Use smaller transactions (#7016)

### Inspect

- Remove duplicated construction path (#6966)

### Light

- Update initialization description (#320)

### Proto

- Add tendermint go changes (#349)
- Regenerate code (#6977)

### Proxy

- Move proxy package to internal (#6953)

### Readme

- Update discord links (#6965)

### Rfc

- Database storage engine (#6897)
- E2e improvements (#6941)
- Add performance taxonomy rfc (#6921)
- Fix a few typos and formatting glitches p2p roadmap (#6960)
- Event system (#6957)

### Rpc

- Fix hash encoding in JSON parameters (#6813)
- Strip down the base RPC client interface. (#6971)
- Implement BroadcastTxCommit without event subscriptions (#6984)

### State

- Move package to internal (#6964)

### Statesync

- Shut down node when statesync fails (#6944)
- Clean up reactor/syncer lifecylce (#6995)
- Add logging while waiting for peers (#7007)

### Statesync/rpc

- Metrics for the statesync and the rpc SyncInfo (#6795)

### Store

- Move pacakge to internal (#6978)

## [0.35.0-rc1] - 2021-09-08

### Documentation

- Add package godoc for indexer (#6839)
- Remove return code in normal case from go built-in example (#6841)
- Fix a typo in the indexing section (#6909)

### Features

- [**breaking**] Proposed app version (#148)

### Miscellaneous Tasks

- Bump tenderdash version to 0.6.0-dev.1

### Testing

- Install abci-cli when running make tests_integrations (#6834)

### Abci

- Clarify what abci stands for (#336)
- Clarify connection use in-process (#337)
- Change client to use multi-reader mutexes (backport #6306) (#6873)

### Blocksync

- Complete transition from Blockchain to BlockSync (#6847)

### Build

- Bump github.com/golangci/golangci-lint (#6837)
- Bump docker/build-push-action from 2.6.1 to 2.7.0 (#6845)
- Bump codecov/codecov-action from 2.0.2 to 2.0.3 (#6860)
- Bump github.com/rs/zerolog from 1.23.0 to 1.24.0 (#6874)
- Bump github.com/lib/pq from 1.10.2 to 1.10.3 (#6890)
- Bump docker/setup-buildx-action from 1.5.0 to 1.6.0 (#6903)
- Bump github.com/golangci/golangci-lint (#6907)

### Changelog

- Update to reflect 0.34.12 release (#6833)
- Linkify the 0.34.11 release notes (#6836)

### Cleanup

- Fix order of linters in the golangci-lint config (#6910)

### Cmd

- Remove deprecated snakes (#6854)

### Contributing

- Remove release_notes.md reference (#6846)

### Core

- Text cleanup (#332)

### E2e

- Avoid starting nodes from the future (#6835)
- Avoid starting nodes from the future (#6835) (#6838)
- Cleanup node start function (#6842)
- Cleanup node start function (#6842) (#6848)
- More consistent node selection during tests (#6857)
- Add weighted random configuration selector (#6869)
- More reliable method for selecting node to inject evidence (#6880)
- Change restart mechanism (#6883)
- Weight protocol dimensions (#6884)
- Skip light clients when waiting for height (#6891)
- Wait for all nodes rather than just one (#6892)
- Skip assertions for stateless nodes (#6894)
- Clean up generation of evidence (#6904)

### Inspect

- Add inspect mode for debugging crashed tendermint node (#6785)

### Internal/consensus

- Update error log (#6863)
- Update error log (#6863) (#6867)

### Light

- Fix early erroring (#6905)

### Lint

- Change deprecated linter (#6861)

### Network

- Update terraform config (#6901)

### Networks

- Update to latest DigitalOcean modules (#6902)

### P2p

- Change default to use new stack (#6862)

### Proto

- Move proto files under the correct directory related to their package name (#344)

### Psql

- Add documentation and simplify constructor API (#6856)

### Pubsub

- Improve handling of closed blocking subsciptions. (#6852)

### Rfc

- P2p next steps (#6866)
- Fix link style (#6870)

### Statesync

- Improve stateprovider handling in the syncer (backport) (#6881)
- Implement p2p state provider (#6807)

### Time

- Make median time library type private (#6853)

### Types

- Move mempool error for consistency (#6875)

### Upgrading

- Add information into the UPGRADING.md for users of the codebase wishing to upgrade (#6898)

## [0.34.12] - 2021-08-17

### Documentation

- Fix typo (#6789)
- Fix a typo in the genesis_chunked description (#6792)
- Upgrade documentation for custom mempools (#6794)
- Fix typos in /tx_search and /tx. (#6823)

### Security

- Bump google.golang.org/grpc from 1.39.0 to 1.39.1 (#6801)
- Bump google.golang.org/grpc from 1.39.1 to 1.40.0 (#6819)

### Testing

- Add mechanism to reproduce found fuzz errors (#6768)

### Abci

- Add changelog entry for mempool_error field (#6770)

### Adr

- Node initialization (#6562)

### Blockchain

- Rename to blocksync service (#6755)

### Blockstore

- Fix problem with seen commit (#6782)

### Build

- Bump styfle/cancel-workflow-action from 0.9.0 to 0.9.1 (#6786)
- Bump technote-space/get-diff-action from 4 to 5 (#6788)
- Bump github.com/BurntSushi/toml from 0.3.1 to 0.4.1 (#6796)

### Bytes

- Clean up and simplify encoding of HexBytes (#6810)

### Changelog

- Prepare for v0.34.12 (#6831)

### Changelog_pending

- Add missing item (#6829)
- Add missing entry (#6830)

### Cleanup

- Remove redundant error plumbing (#6778)

### Cli/indexer

- Reindex events (#6676)

### Clist

- Add simple property tests (#6791)

### Commands

- Add key migration cli (#6790)

### Contributing

- Update release instructions to use backport branches (#6827)

### Evidence

- Add section explaining evidence (#324)

### Light

- Wait for tendermint node to start before running example test (#6744)
- Replace homegrown mock with mockery (#6735)

### Mempool/v1

- Test reactor does not panic on broadcast (#6772)

### Node

- Minimize hardcoded service initialization (#6798)

### P2p

- Add test for pqueue dequeue full error (#6760)

### Pubsub

- Unsubscribe locking handling (#6816)

### Rpc

- Add documentation for genesis chunked api (#6776)
- Avoid panics in unsafe rpc calls with new p2p stack (#6817)
- Support new p2p infrastructure (#6820)
- Log update (#6825)
- Log update (backport #6825) (#6826)
- Update peer format in specification in NetInfo operation (#331)

### State/privval

- Vote timestamp fix (#6748)
- Vote timestamp fix (backport #6748) (#6783)

### Statesync

- New messages for gossiping consensus params (#328)

### Tools

- Add mockery to tools.go and remove mockery version strings (#6787)

### Version

- Bump for 0.34.12 (#6832)

## [0.5.10] - 2021-07-26

### Testing

- Add test to reproduce found fuzz errors (#6757)

### Build

- Bump golangci/golangci-lint-action from 2.3.0 to 2.5.2
- Bump codecov/codecov-action from 1.5.2 to 2.0.1 (#6739)
- Bump codecov/codecov-action from 2.0.1 to 2.0.2 (#6764)

### E2e

- Avoid systematic key-type variation (#6736)
- Drop single node hybrid configurations (#6737)
- Remove cartesian testing of ipv6 (#6734)
- Run tests in fewer groups (#6742)
- Prevent adding light clients as persistent peers (#6743)
- Longer test harness timeouts (#6728)
- Allow for both v0 and v1 mempool implementations (#6752)

### Fastsync/event

- Emit fastsync status event when switching consensus/fastsync (#6619)

### Internal

- Update blockchain reactor godoc (#6749)

### Light

- Run examples as integration tests (#6745)
- Improve error handling and allow providers to be added (#6733)

### Mempool

- Return mempool errors to the abci client (#6740)

### P2p

- Add coverage for mConnConnection.TrySendMessage (#6754)
- Avoid blocking on the dequeCh (#6765)

### Statesync/event

- Emit statesync start/end event  (#6700)

## [0.5.8] - 2021-07-20

### Blockchain

- Error on v2 selection (#6730)

### Clist

- Add a few basic clist tests (#6727)

### Libs/clist

- Revert clear and detach changes while debugging (#6731)

### Mempool

- Add TTL configuration to mempool (#6715)

## [0.5.7] - 2021-07-16

### Bug Fixes

- Maverick compile issues (#104)
- Private validator key still automatically creating (#120)
- Getting pro tx hash from full node
- Incorrectly assume amd64 arch during docker build
- Image isn't pushed after build

### Documentation

- Rename tenderdash and update target repo
- Update events (#6658)
- Add sentence about windows support (#6655)
- Add docs file for the peer exchange (#6665)
- Update github issue and pr templates (#131)
- Fix broken links (#6719)

### Features

- Improve initialisation (#117)
- Add arm64 arch for builds

### Miscellaneous Tasks

- Target production dockerhub org
- Use official docker action

### RPC

- Mark grpc as deprecated (#6725)

### Security

- Bump github.com/spf13/viper from 1.8.0 to 1.8.1 (#6622)
- Bump github.com/rs/cors from 1.7.0 to 1.8.0 (#6635)
- Bump github.com/go-kit/kit from 0.10.0 to 0.11.0 (#6651)
- Bump github.com/spf13/cobra from 1.2.0 to 1.2.1 (#6650)

### Testing

- Fix non-deterministic backfill test (#6648)

### Abci

- Fix gitignore abci-cli (#6668)
- Remove counter app (#6684)

### Build

- Bump docker/login-action from 1.9.0 to 1.10.0 (#6614)
- Bump docker/setup-buildx-action from 1.3.0 to 1.4.0 (#6629)
- Bump docker/setup-buildx-action from 1.4.0 to 1.4.1 (#6632)
- Bump google.golang.org/grpc from 1.38.0 to 1.39.0 (#6633)
- Bump github.com/spf13/cobra from 1.1.3 to 1.2.0 (#6640)
- Bump docker/build-push-action from 2.5.0 to 2.6.1 (#6639)
- Bump docker/setup-buildx-action from 1.4.1 to 1.5.0 (#6649)
- Bump gaurav-nelson/github-action-markdown-link-check (#6679)
- Bump github.com/golangci/golangci-lint (#6686)
- Bump gaurav-nelson/github-action-markdown-link-check (#313)
- Bump github.com/google/uuid from 1.2.0 to 1.3.0 (#6708)
- Bump actions/stale from 3.0.19 to 4 (#319)
- Bump actions/stale from 3.0.19 to 4 (#6726)

### Changelog

- Have a single friendly bug bounty reminder (#6600)
- Update and regularize changelog entries (#6594)

### Ci

- Make ci consistent and push to docker hub
- Trigger docker build on release
- Disable arm build
- Always push to dockerhub
- Test enabling cache
- Test arm build
- Manually trigger build
- Disable arm64 builds
- Set release to workflow dispatch (manual) trigger
- Enable arm64 in CI

### Cmd/tendermint/commands

- Replace $HOME/.some/test/dir with t.TempDir (#6623)

### Config

- Add example on external_address (#6621)
- Add example on external_address (backport #6621) (#6624)

### Crypto

- Use a different library for ed25519/sr25519 (#6526)

### Deps

- Remove pkg errors (#6666)
- Run go mod tidy (#6677)

### E2e

- Allow variable tx size  (#6659)
- Disable app tests for light client (#6672)
- Remove colorized output from docker-compose (#6670)
- Extend timeouts in test harness (#6694)
- Ensure evidence validator set matches nodes validator set (#6712)
- Tweak sleep for pertubations (#6723)

### Evidence

- Update ADR 59 and add comments to the use of common height (#6628)

### Fastsync

- Update the metrics during fast-sync (#6590)

### Fastsync/rpc

- Add TotalSyncedTime & RemainingTime to SyncInfo in /status RPC (#6620)

### Internal/blockchain/v0

- Prevent all possible race for blockchainCh.Out (#6637)

### Libs/CList

- Automatically detach the prev/next elements in Remove function (#6626)

### Light

- Correctly handle contexts (backport -> v0.34.x) (#6685)
- Correctly handle contexts (#6687)
- Add case to catch cancelled contexts within the detector (backport #6701) (#6720)

### Mempool

- Move errors to be public (#6613)

### P2p

- Address audit issues with the peer manager (#6603)
- Make NodeID and NetAddress public (#6583)
- Reduce buffering on channels (#6609)
- Do not redial peers with different chain id (#6630)
- Track peer channels to avoid sending across a channel a peer doesn't have (#6601)
- Remove annoying error log (#6688)

### Pkg

- Expose p2p functions (#6627)

### Privval

- Missing privval type check in SetPrivValidator (#6645)

### Psql

- Close opened rows in tests (#6669)

### Pubsub

- Refactor Event Subscription (#6634)

### Release

- Update changelog and version (#6599)

### Router/statesync

- Add helpful log messages (#6724)

### Rpc

- Use shorter path names for tests (#6602)
- Add totalGasUSed to block_results response (#308)
- Add max peer block height into /status rpc call (#6610)
- Add `TotalGasUsed` to `block_results` response (#6615)
- Re-index missing events (#6535)
- Add chunked rpc interface (backport #6445) (#6717)

### State/indexer

- Close row after query (#6664)

### State/privval

- No GetPubKey retry beyond the proposal/voting window (#6578)

### Statesync

- Make fetching chunks more robust (#6587)
- Keep peer despite lightblock query fail (#6692)
- Remove outgoingCalls race condition in dispatcher (#6699)
- Use initial height as a floor to backfilling (#6709)
- Increase dispatcher timeout (#6714)
- Dispatcher test uses internal channel for timing (#6713)

### Tooling

- Use go version 1.16 as minimum version (#6642)

### Tools

- Remove k8s (#6625)
- Move tools.go to subdir (#6689)

### Types

- Move NodeInfo from p2p (#6618)

## [0.34.11] - 2021-06-18

### Testing

- Add current fuzzing to oss-fuzz-build script (#6576)
- Fix wrong compile fuzzer command (#6579)
- Fix wrong path for some p2p fuzzing packages (#6580)

### Build

- Bump github.com/rs/zerolog from 1.22.0 to 1.23.0 (#6575)
- Bump github.com/spf13/viper from 1.7.1 to 1.8.0 (#6586)

### Config

- Add root dir to priv validator (#6585)

### Consensus

- Skip all messages during sync (#6577)

### E2e

- Fix looping problem while waiting (#6568)

### Fuzz

- Initial support for fuzzing (#6558)

### Libs/log

- Text logging format changes (#6589)

### Libs/time

- Move types/time into libs (#6595)

### Linter

- Linter checks non-ASCII identifiers (#6574)

### P2p

- Increase queue size to 16MB (#6588)
- Avoid retry delay in error case (#6591)

### Release

- Prepare changelog for v0.34.11 (#6597)

### Rpc

- Fix RPC client doesn't handle url's without ports (#6507)
- Add subscription id to events (#6386)

### State

- Move pruneBlocks from consensus/state to state/execution (#6541)

### State/types

- Refactor makeBlock, makeBlocks and makeTxs (#6567)

### Statesync

- Tune backfill process (#6565)
- Increase chunk priority and robustness (#6582)

## [0.4.2] - 2021-06-10

### Blockchain/v0

- Fix data race in blockchain channel (#6518)

### Build

- Bump github.com/btcsuite/btcd (#6560)
- Bump codecov/codecov-action from 1.5.0 to 1.5.2 (#6559)

### Indexer

- Use INSERT ... ON CONFLICT in the psql eventsink insert functions (#6556)

### Node

- Fix genesis on start up (#6563)

## [0.4.1] - 2021-06-09

### .github

- Make core team codeowners (#6384)
- Make core team codeowners (#6383)

### Backport

- #6494 (#6506)

### Bug Fixes

- Benchmark single operation in parallel benchmark not b.N (#6422)

### Documentation

- Adr-65 adjustments (#6401)
- Adr cleanup (#6489)
- Hide security page (second attempt) (#6511)
- Rename tendermint-core to system (#6515)
- Logger updates (#6545)

### RFC

- ReverseSync - fetching historical data (#224)

### Security

- Bump github.com/confio/ics23/go from 0.6.3 to 0.6.6 (#6374)
- Bump github.com/grpc-ecosystem/go-grpc-middleware from 1.2.2 to 1.3.0 (#6387)
- Bump google.golang.org/grpc from 1.37.0 to 1.37.1 (#6461)
- Bump google.golang.org/grpc from 1.37.1 to 1.38.0 (#6483)
- Bump github.com/lib/pq from 1.10.1 to 1.10.2 (#6505)

### Testing

- Create common functions for easily producing tm data structures (#6435)
- HeaderHash test vector (#6531)
- Add evidence hash testvectors (#6536)

### WIP

- Add implementation of mock/fake http-server
- Rename package name from fakeserver to mockcoreserver
- Change the method names of call structure, Fix adding headers
- Add mock of JRPCServer implementation on top of HTTServer mock

### Build

- Bump codecov/codecov-action from v1.3.2 to v1.4.0 (#6365)
- Bump codecov/codecov-action from v1.4.0 to v1.4.1 (#6379)
- Bump docker/setup-buildx-action from v1.1.2 to v1.2.0 (#6391)
- Bump docker/setup-buildx-action from v1.2.0 to v1.3.0 (#6413)
- Bump codecov/codecov-action from v1.4.1 to v1.5.0 (#6417)
- Bump github.com/cosmos/iavl from 0.15.3 to 0.16.0 (#6421)
- Bump JamesIves/github-pages-deploy-action (#6448)
- Bump docker/build-push-action from 2 to 2.4.0 (#6454)
- Bump actions/stale from 3 to 3.0.18 (#6455)
- Bump actions/checkout from 2 to 2.3.4 (#6456)
- Bump docker/login-action from 1 to 1.9.0 (#6460)
- Bump actions/stale from 3.0.18 to 3.0.19 (#6477)
- Bump actions/stale from 3 to 3.0.18 (#300)
- Bump watchpack from 2.1.1 to 2.2.0 in /docs (#6482)
- Bump actions/stale from 3.0.18 to 3.0.19 (#302)
- Bump browserslist from 4.16.4 to 4.16.6 in /docs (#6487)
- Bump docker/build-push-action from 2.4.0 to 2.5.0 (#6496)
- Bump dns-packet from 1.3.1 to 1.3.4 in /docs (#6500)
- Bump actions/cache from 2.1.5 to 2.1.6 (#6504)
- Bump rtCamp/action-slack-notify from 2.1.3 to 2.2.0 (#6543)
- Bump github.com/prometheus/client_golang (#6552)

### Changelog

- Update for 0.34.10 (#6358)

### Config

- Create `BootstrapPeers` p2p config parameter (#6372)
- Add private peer id /net_info expose information in default config (#6490)
- Seperate priv validator config into seperate section (#6462)

### Config/indexer

- Custom event indexing (#6411)

### Consensus

- Add test vector for hasvote (#6469)

### Core

- Update a few sections  (#284)

### Crypto

- Add sr25519 as a validator key (#6376)

### Crypto/merkle

- Pre-allocate data slice in innherHash (#6443)
- Optimize merkle tree hashing (#6513)

### Db

- Migration script for key format change (#6355)

### Dep

- Remove IAVL dependency (#6550)

### E2e

- Split out nightly tests (#6395)
- Prevent non-viable testnets (#6486)

### Events

- Add block_id to NewBlockEvent (#6478)

### Evidence

- Fix bug with hashes (#6375)
- Fix bug with hashes (backport #6375) (#6381)
- Separate abci specific validation (#6473)

### Github

- Fix linter configuration errors and occluded errors (#6400)

### Improvement

- Update TxInfo (#6529)

### Libs

- Remove most of libs/rand (#6364)
- Internalize some packages (#6366)

### Libs/clist

- Fix flaky tests (#6453)

### Libs/log

- Use fmt.Fprintf directly with *bytes.Buffer to avoid unnecessary allocations (#6503)

### Libs/os

- Avoid CopyFile truncating destination before checking if regular file (#6428)
- Avoid CopyFile truncating destination before checking if regular file (backport: #6428) (#6436)

### Light

- Ensure trust level is strictly less than 1 (#6447)
- Spec alignment on verify skipping (#6474)

### Lint

- Fix lint errors (#301)

### Logger

- Refactor Tendermint logger by using zerolog (#6534)

### Mempool

- Remove vestigal mempool wal (#6396)
- Benchmark improvements (#6418)
- Add duplicate transaction and parallel checktx benchmarks (#6419)
- V1 implementation (#6466)

### Metrics

- Change blocksize to a histogram (#6549)

### Node

- Use db provider instead of mem db (#6362)
- Cleanup pex initialization (#6467)
- Change package interface (#6540)

### Node/state

- Graceful shutdown in the consensus state (#6370)

### Node/tests

- Clean up use of genesis doc and surrounding tests (#6554)

### P2p

- Fix network update test (#6361)
- Update state sync messages for reverse sync (#285)
- Improve PEX reactor (#6305)
- Support private peer IDs in new p2p stack (#6409)
- Wire pex v2 reactor to router (#6407)
- Add channel descriptors to open channel (#6440)
- Revert change to routePeer (#6475)
- Limit rate of dialing new peers (#6485)
- Renames for reactors and routing layer internal moves (#6547)

### P2p/conn

- Check for channel id overflow before processing receive msg (#6522)
- Check for channel id overflow before processing receive msg (backport #6522) (#6528)

### P2p/pex

- Cleanup to pex internals and peerManager interface (#6476)
- Reuse hash.Hasher per addrbook for speed (#6509)

### Pex

- Fix send requests too often test (#6437)

### Rpc

- Define spec for RPC (#276)
- Remove global environment (#6426)
- Clean up client global state in tests (#6438)
- Add chunked rpc interface (#6445)
- Clarify timestamps (#304)
- Add chunked genesis endpoint (#299)
- Decouple test fixtures from node implementation (#6533)

### State

- Keep a cache of block verification results (#6402)

### State/indexer

- Reconstruct indexer, move txindex into the indexer package (#6382)

### Statesync

- Improve e2e test outcomes (#6378)
- Improve e2e test outcomes (backport #6378) (#6380)
- Sort snapshots by commonness (#6385)
- Fix unreliable test (#6390)
- Ranking test fix (#6415)

### Tools

- Use os home dir to instead of the hardcoded PATH (#6498)

### Types

- Refactor EventAttribute (#6408)
- Fix verify commit light / trusting bug (#6414)
- Revert breaking change (#6538)

### Version

- Revert version through ldflag only (#6494)

### Ws

- Parse remote addrs with trailing dash (#6537)

## [0.34.10] - 2021-04-14

### Documentation

- Bump vuepress-theme-cosmos (#6344)
- Remove RFC section and s/RFC001/ADR066 (#6345)

### RPC

- Don't cap page size in unsafe mode (#6329)

### Security

- Bump google.golang.org/grpc from 1.36.1 to 1.37.0 (#6330)

### Testing

- Produce structured reporting from benchmarks (#6343)

### Adr

- ADR 065: Custom Event Indexing (#6307)

### Build

- Bump google.golang.org/grpc from 1.36.1 to 1.37.0 (bp #6330) (#6335)
- Bump styfle/cancel-workflow-action from 0.8.0 to 0.9.0 (#6341)
- Bump actions/cache from v2.1.4 to v2.1.5 (#6350)

### Changelog

- Update to reflect 0.34.9 (#6334)
- Update for 0.34.10 (#6357)

### E2e

- Tx load to use broadcast sync instead of commit (#6347)
- Tx load to use broadcast sync instead of commit (backport #6347) (#6352)
- Relax timeouts (#6356)

### Light

- Handle too high errors correctly (#6346)
- Handle too high errors correctly (backport #6346) (#6351)

### P2p

- Make peer scoring test more resilient (#6322)
- Fix using custom channels (#6339)
- Minor cleanup + update router options (#6353)

### Security

- Update policy after latest security release (#6336)

## [0.34.9] - 2021-04-08

### .github

- Remove myself from CODEOWNERS (#6248)

### Bug Fixes

- Make p2p evidence_pending test not timing dependent (#6252)
- Avoid race with a deeper copy (#6285)
- Jsonrpc url parsing and dial function (#6264)
- Jsonrpc url parsing and dial function (#6264) (#6288)
- Theoretical leak in clisit.Init (#6302)
- Test fixture peer manager in mempool reactor tests (#6308)

### Documentation

- Fix sample code (#6186)
- Fix sample code #6186

### P2P

- Evidence Reactor Test Refactor (#6238)

### Security

- Bump vuepress-theme-cosmos from 1.0.180 to 1.0.181 in /docs (#6266)
- Bump github.com/minio/highwayhash from 1.0.1 to 1.0.2 (#6280)

### Testing

- Fix PEX reactor test (#6188)
- Fix rpc, secret_connection and pex tests (#6190)
- Refactor mempool reactor to use new p2ptest infrastructure (#6250)
- Clean up databases in tests (#6304)
- Improve cleanup for data and disk use (#6311)

### Abci

- Note on concurrency (#258)
- Change client to use multi-reader mutexes (#6306)
- Reorder sidebar (#282)

### Blockstore

- Save only the last seen commit (#6212)

### Build

- Bump google.golang.org/grpc from 1.35.0 to 1.36.0 (#6180)
- Bump JamesIves/github-pages-deploy-action from 4.0.0 to 4.1.0 (#6215)
- Bump rtCamp/action-slack-notify from ae4223259071871559b6e9d08b24a63d71b3f0c0 to 2.1.3 (#6234)
- Bump codecov/codecov-action from v1.2.1 to v1.2.2 (#6231)
- Bump codecov/codecov-action from v1.2.2 to v1.3.1 (#6247)
- Bump github.com/golang/protobuf from 1.4.3 to 1.5.1 (#6254)
- Bump github.com/prometheus/client_golang (#6258)
- Bump google.golang.org/grpc from 1.36.0 to 1.36.1 (#6281)
- Bump github.com/golang/protobuf from 1.5.1 to 1.5.2 (#6299)
- Bump github.com/Workiva/go-datastructures (#6298)
- Bump golangci/golangci-lint-action from v2.5.1 to v2.5.2 (#6317)
- Bump codecov/codecov-action from v1.3.1 to v1.3.2 (#6319)
- Bump JamesIves/github-pages-deploy-action (#6316)
- Bump docker/setup-buildx-action from v1 to v1.1.2 (#6324)

### Changelog

- Update for 0.34.8 (#6183)
- Prepare changelog for 0.34.9 release (#6333)

### Ci

- Add janitor (#6292)

### Consensus

- Reduce shared state in tests (#6313)

### Crypto

- Ed25519 & sr25519 batch verification (#6120)

### E2e

- Adjust timeouts to be dynamic to size of network (#6202)
- Add benchmarking functionality (#6210)
- Integrate light clients (#6196)
- Add benchmarking functionality (bp #6210) (#6216)
- Fix light client generator (#6236)
- Integrate light clients (bp #6196)
- Fix perturbation of seed nodes (#6272)
- Add evidence generation and testing (#6276)

### Genesis

- Explain fields in genesis file (#270)

### Github

- Add @tychoish to code owners (#6273)

### Indexer

- Remove info log (#6194)
- Remove info log (#6194)

### Light

- Improve provider handling (#6053)

### Light/evidence

- Handle FLA backport (#6331)

### Linter

- Fix nolintlint warnings (#6257)

### Localnet

- Fix localnet by excluding self from persistent peers list (#6209)

### Logging

- Shorten precommit log message (#6270)
- Shorten precommit log message (#6270) (#6274)

### Logs

- Cleanup (#6198)
- Cleanup (#6198)

### Mempool

- Don't return an error on checktx with the same tx (#6199)

### Mempool/rpc

- Log grooming (#6201)
- Log grooming (bp #6201) (#6203)

### Node

- Implement tendermint modes (#6241)
- Remove mode defaults. Make node mode explicit (#6282)

### Note

- Add nondeterministic note to events (#6220)
- Add nondeterministic note to events (#6220) (#6225)

### P2p

- Links (#268)
- Revised router message scheduling (#6126)
- Metrics (#6278)
- Simple peer scoring (#6277)
- Rate-limit incoming connections by IP (#6286)
- Fix "Unknown Channel" bug on CustomReactors (#6297)
- Connect max inbound peers configuration to new router (#6296)
- Filter peers by IP address and ID (#6300)
- Improve router test stability (#6310)
- Extend e2e tests for new p2p framework (#6323)

### Privval

- Return errors on loadFilePV (#6185)
- Add ctx to privval interface (#6240)

### Readme

- Cleanup (#262)

### Rpc

- Index block events to support block event queries (#6226)
- Index block events to support block event queries (bp #6226) (#6261)

### Rpc/jsonrpc

- Unmarshal RPCRequest correctly (#6191)
- Unmarshal RPCRequest correctly (bp #6191) (#6193)

### Rpc/jsonrpc/server

- Return an error in WriteRPCResponseHTTP(Error) (#6204)
- Return an error in WriteRPCResponseHTTP(Error) (bp #6204) (#6230)

### Spec

- Merge rust-spec (#252)

### State

- Cleanup block indexing logs and null (#6263)
- Fix block event indexing reserved key check (#6314)
- Fix block event indexing reserved key check (#6314) (#6315)

## [0.34.8] - 2021-02-25

### .github

- Jepsen workflow - initial version (#6123)
- [jepsen] fix inputs and remove TTY from docker (#6134)
- [jepsen] use working-directory instead of 'cd' (#6135)
- [jepsen] use "bash -c" to execute lein run cmd (#6136)
- [jepsen] cd inside the container, not outside (#6137)
- [jepsen] fix directory name (#6138)
- [jepsen] source .bashrc (#6139)
- [jepsen] add more docs (#6141)
- [jepsen] archive results (#6164)

### Documentation

- How to add tm version to RPC (#6151)
- Add preallocated list of security vulnerability names (#6167)

### Testing

- Fix TestByzantinePrevoteEquivocation (#6132)

### Abci

- Fix ReCheckTx for Socket Client (bp #6124) (#6125)

### Build

- Bump golangci/golangci-lint-action from v2.4.0 to v2.5.1 (#6175)

### Changelog

- Fix changelog pending version numbering (#6149)
- Update with changes from 0.34.7 (and failed 0.34.5, 0.34.6) (#6150)
- Update for 0.34.8 (#6181)

### Cmd

- Ignore missing wal in debug kill command (#6160)

### Consensus

- More log grooming (#6140)
- Log private validator address and not struct (#6144)
- More log grooming (bp #6140) (#6143)

### Goreleaser

- Reintroduce arm64 build instructions

### Libs/log

- [JSON format] include timestamp (#6174)
- [JSON format] include timestamp (bp #6174) (#6179)

### Light

- Improve timeout functionality (#6145)

### Logging

- Print string instead of callback (#6177)
- Print string instead of callback (#6178)

### P2p

- Enable scheme-less parsing of IPv6 strings (#6158)

### Rpc

- Standardize error codes (#6019)
- Change default sorting to desc for `/tx_search` results (#6168)

### Rpc/client/http

- Do not drop events even if the `out` channel is full (#6163)
- Drop endpoint arg from New and add WSOptions (#6176)

### State

- Save in batches within the state store (#6067)

## [0.34.7] - 2021-02-18

### .goreleaser

- Remove arm64 build instructions and bump changelog again (#6131)

## [0.34.6] - 2021-02-18

### Changelog

- Bump to v0.34.6

## [0.34.5] - 2021-02-18

### ABCI

- Fix ReCheckTx for Socket Client (#6124)

### CHANGELOG_PENDING

- Update changelog for changes to American spelling (#6100)

### Documentation

- Fix proto file names (#6112)

### Security

- Update 0.34.3 changelog with details on security vuln (bp #6108) (#6110)

### Adr

- Batch verification (#6008)

### Backports

- Mergify (#6107)

### Build

- Bump golangci/golangci-lint-action from v2.3.0 to v2.4.0 (#6111)

### Changelog

- Update to reflect v0.34.4 release (#6105)
- Update 0.34.3 changelog with details on security vuln (#6108)
- Update for 0.34.5 (#6129)

### Consensus

- P2p refactor (#5969)
- Groom Logs (#5917)
- Remove privValidator from log call (#6128)

### Node

- Feature flag for legacy p2p support (#6056)

### Proto

- Modify height int64 to uint64 (#253)

### Tooling

- Remove tools/Makefile (#6102)
- Remove tools/Makefile (bp #6102) (#6106)

## [0.34.4] - 2021-02-11

### .github

- Fix fuzz-nightly job (#5965)
- Archive crashers and fix set-crashers-count step (#5992)
- Rename crashers output (fuzz-nightly-test) (#5993)
- Clean up PR template (#6050)
- Use job ID (not step ID) inside if condition (#6060)
- Remove erik as reviewer from dependapot (#6076)
- Rename crashers output (fuzz-nightly-test) (#5993)
- Archive crashers and fix set-crashers-count step (#5992)
- Fix fuzz-nightly job (#5965)
- Use job ID (not step ID) inside if condition (#6060)
- Remove erik as reviewer from dependapot (#6076)

### .github/workflows

- Enable manual dispatch for some workflows (#5929)
- Try different e2e nightly test set (#6036)
- Separate e2e workflows for 0.34.x and master (#6041)
- Fix whitespace in e2e config file (#6043)
- Cleanup yaml for e2e nightlies (#6049)
- Try different e2e nightly test set (#6036)
- Separate e2e workflows for 0.34.x and master (#6041)
- Fix whitespace in e2e config file (#6043)
- Cleanup yaml for e2e nightlies (#6049)

### .golangci

- Set locale to US for misspell linter (#6038)

### ADR-062

- Update with new P2P core implementation (#6051)

### CODEOWNERS

- Remove erikgrinaker (#6057)
- Remove erikgrinaker (#6057)

### CONTRIBUTING.md

- Update testing section (#5979)

### Documentation

- Update package-lock.json (#5928)
- Package-lock.json fix (#5948)
- Change v0.33 version (#5950)
- Bump package-lock.json of v0.34.x (#5952)
- Dont login when in PR (#5961)
- Release Linux/ARM64 image (#5925)
- Log level docs (#5945)
- Fix typo in state sync example (#5989)
- External address (#6035)
- Reword configuration (#6039)
- Change v0.33 version (#5950)
- Release Linux/ARM64 image (#5925)
- Dont login when in PR (#5961)
- Fix typo in state sync example (#5989)

### Makefile

- Always pull image in proto-gen-docker. (#5953)
- Always pull image in proto-gen-docker. (#5953)

### Security

- Bump watchpack from 2.1.0 to 2.1.1 in /docs (#6063)
- Bump github.com/tendermint/tm-db from 0.6.3 to 0.6.4 (#6073)
- Bump github.com/tendermint/tm-db from 0.6.3 to 0.6.4 (#6073)
- Bump watchpack from 2.1.0 to 2.1.1 in /docs (#6063)

### Testing

- Fix TestPEXReactorRunning data race (#5955)
- Move fuzz tests into this repo (#5918)
- Fix `make test` (#5966)
- Close transports to avoid goroutine leak failures (#5982)
- Don't use foo-bar.net in TestHTTPClientMakeHTTPDialer (#5997)
- Fix TestSwitchAcceptRoutine flake by ignoring error type (#6000)
- Disable TestPEXReactorSeedModeFlushStop due to flake (#5996)
- Fix test data race in p2p.MemoryTransport with logger (#5995)
- Fix TestSwitchAcceptRoutine by ignoring spurious error (#6001)
- Fix TestRouter to take into account PeerManager reconnects (#6002)
- Fix flaky router broadcast test (#6006)
- Enable pprof server to help debugging failures (#6003)
- Increase sign/propose tolerances (#6033)
- Increase validator tolerances (#6037)
- Don't use foo-bar.net in TestHTTPClientMakeHTTPDialer (#5997) (#6047)
- Enable pprof server to help debugging failures (#6003)
- Increase sign/propose tolerances (#6033)
- Increase validator tolerances (#6037)
- Move fuzz tests into this repo (#5918)
- Fix `make test` (#5966)

### Blockchain/v2

- Internalize behavior package (#6094)

### Build

- Bump actions/cache from v2.1.3 to v2.1.4 (#6055)
- Bump JamesIves/github-pages-deploy-action (#6062)
- Bump actions/cache from v2.1.3 to v2.1.4 (#6055)
- Bump github.com/spf13/cobra from 1.1.1 to 1.1.2 (#6075)
- Bump github.com/spf13/cobra from 1.1.2 to 1.1.3 (#6098)

### Changelog

- Update changelog for v0.34.3 (#5927)
- Update for v0.34.4 (#6096)
- Improve with suggestions from @melekes (#6097)

### Consensus

- Groom Logs (#5917)

### E2e

- Releases nightly (#5906)
- Add control over the log level of nodes (#5958)
- Releases nightly (#5906)
- Disconnect maverick (#6099)

### Evidence

- Terminate broadcastEvidenceRoutine when peer is stopped (#6068)

### Goreleaser

- Downcase archive and binary names (#6029)
- Downcase archive and binary names (#6029)

### Libs/log

- Format []byte as hexidecimal string (uppercased) (#5960)
- Format []byte as hexidecimal string (uppercased) (#5960)

### Light

- Fix panic with RPC calls to commit and validator when height is nil (#6026)
- Fix panic with RPC calls to commit and validator when height is nil (#6040)
- Remove max retry attempts from client and add to provider (#6054)
- Remove witnesses in order of decreasing index (#6065)
- Create provider options struct (#6064)

### Light/provider/http

- Fix Validators (#6022)
- Fix Validators (#6024)

### Makefile

- Remove call to tools (#6104)

### Maverick

- Reduce some duplication (#6052)
- Reduce some duplication (#6052)

### Mempool

- P2p refactor (#5919)
- Fix reactor tests (#5967)
- Fix TestReactorNoBroadcastToSender (#5984)
- Fix mempool tests timeout (#5988)

### P2p

- Revise shim log levels (#5940)
- Improve PeerManager prototype (#5936)
- Make PeerManager.DialNext() and EvictNext() block (#5947)
- Improve peerStore prototype (#5954)
- Simplify PeerManager upgrade logic (#5962)
- Add PeerManager.Advertise() (#5957)
- Add prototype PEX reactor for new stack (#5971)
- Resolve PEX addresses in PEX reactor (#5980)
- Use stopCtx when dialing peers in Router (#5983)
- Clean up new Transport infrastructure (#6017)
- Tighten up and test Transport API (#6020)
- Add tests and fix bugs for `NodeAddress` and `NodeID` (#6021)
- Tighten up and test PeerManager (#6034)
- Tighten up Router and add tests (#6044)

### Params

- Remove block timeiota (#248)
- Remove blockTimeIota (#5987)

### Proto

- Docker deployment (#5931)
- Seperate native and proto types (#5994)
- Add files (#246)
- Docker deployment (#5931)

### Proto/p2p

- Rename PEX messages and fields (#5974)

### Spec

- Remove reactor section (#242)

### Store

- Fix deadlock in pruning (#6007)
- Use a batch instead of individual writes in SaveBlock (#6018)

### Sync

- Move closer to separate file (#6015)

### Types

- Cleanup protobuf.go (#6023)

## [0.34.3] - 2021-01-19

### .github/codeowners

- Add alexanderbez (#5913)
- Add alexanderbez (#5913)

### Security

- Bump github.com/stretchr/testify from 1.6.1 to 1.7.0 (#5897)
- Bump google.golang.org/grpc from 1.34.0 to 1.35.0 (#5902)
- Bump vuepress-theme-cosmos from 1.0.179 to 1.0.180 in /docs (#5915)
- Bump github.com/stretchr/testify from 1.6.1 to 1.7.0 (#5897)
- Bump google.golang.org/grpc from 1.34.0 to 1.35.0 (#5902)
- Bump vuepress-theme-cosmos from 1.0.179 to 1.0.180 in /docs (#5915)

### Changelog

- Update changelogs to reflect changes released in 0.34.2
- Update for 0.34.3 (#5926)

### Config

- Fix mispellings (#5914)
- Fix mispellings (#5914)

### Light

- Fix light store deadlock (#5901)

### Mod

- Go mod tidy

### P2p

- Add prototype peer lifecycle manager (#5882)

### Proto

- Bump gogoproto (1.3.2) (#5886)
- Bump gogoproto (1.3.2) (#5886)

### Readme

- Add security mailing list (#5916)
- Add security mailing list (#5916)

## [0.34.2] - 2021-01-12

### Documentation

- Fix broken redirect links (#5881)

### Testing

- Improve WaitGroup handling in Byzantine tests (#5861)
- Tolerate up to 2/3 missed signatures for a validator (#5878)
- Disable abci/grpc and blockchain/v2 due to flake (#5854)

### Abci

- Rewrite to proto interface (#237)

### Abci/grpc

- Fix invalid mutex handling in StopForError() (#5849)

### Blockchain/v0

- Stop tickers on poolRoutine exit (#5860)

### Blockchain/v2

- Fix missing mutex unlock (#5862)

### Build

- Bump gaurav-nelson/github-action-markdown-link-check (#239)
- Bump gaurav-nelson/github-action-markdown-link-check (#5884)

### Changelog

- Update with changes released in 0.34.1 (#5875)
- Prepare 0.34.2 release (#5894)

### Evidence

- P2p refactor (#5747)
- Buffer evidence from consensus (#5890)
- Buffer evidence from consensus (#5890)

### Layout

- Add section titles (#240)

### Libs/os

- EnsureDir now returns IO errors and checks file type (#5852)

### Os

- Simplify EnsureDir() (#5871)

### P2p

- Add Router prototype (#5831)

### Privval

- Add grpc (#5725)
- Query validator key (#5876)

### Reactors

- Remove bcv1 (#241)

### State

- Prune states using an iterator (#5864)

### Store

- Use db iterators for pruning and range-based queries (#5848)

### Tools/tm-signer-harness

- Fix listener leak in newTestHarnessListener() (#5850)

## [0.34.1] - 2021-01-06

### ABCI

- Update readme to fix broken link to proto (#5847)

### Testing

- Disable abci/grpc and blockchain/v2 due to flake (#5854)
- Add conceptual overview (#5857)
- Improve WaitGroup handling in Byzantine tests (#5861)

### Abci/grpc

- Fix invalid mutex handling in StopForError() (#5849)

### Blockchain/v0

- Stop tickers on poolRoutine exit (#5860)

### Blockchain/v2

- Fix missing mutex unlock (#5862)

### Build

- Bump codecov/codecov-action from v1.1.1 to v1.2.0 (#5863)
- Bump codecov/codecov-action from v1.2.0 to v1.2.1

### Changelog

- Update changelog for v0.34.1 (#5872)

### Libs/os

- EnsureDir now returns IO errors and checks file type (#5852)

### Os

- Simplify EnsureDir() (#5871)

### P2p

- Rename ID to NodeID
- Add NodeID.Validate(), replaces validateID()
- Replace PeerID with NodeID
- Rename NodeInfo.DefaultNodeID to NodeID
- Rename PubKeyToID to NodeIDFromPubKey
- Fix IPv6 address handling in new transport API (#5853)
- Fix MConnection inbound traffic statistics and rate limiting (#5868)
- Fix MConnection inbound traffic statistics and rate limiting (#5868) (#5870)

### Statesync

- Do not recover panic on peer updates (#5869)

### Store

- Order-preserving varint key encoding (#5771)

### Tools/tm-signer-harness

- Fix listener leak in newTestHarnessListener() (#5850)

## [0.34.1-rc1] - 2020-12-23

### CHANGELOG

- Prepare 0.34.1-rc1 (#5832)

### Documentation

- Use hyphens instead of snake case (#5802)
- Specify master for tutorials (#5822)
- Specify 0.34 (#5823)

### Makefile

- Use git 2.20-compatible branch detection (#5778)

### Security

- Bump github.com/prometheus/client_golang from 1.8.0 to 1.9.0 (#5807)
- Bump github.com/cosmos/iavl from 0.15.2 to 0.15.3 (#5814)

### Abci

- Use protoio for length delimitation (#5818)

### Blockchain/v2

- Send status request when new peer joins (#5774)

### Build

- Bump vuepress-theme-cosmos from 1.0.178 to 1.0.179 in /docs (#5780)
- Bump gaurav-nelson/github-action-markdown-link-check from 1.0.8 to 1.0.9 (#5779)
- Bump gaurav-nelson/github-action-markdown-link-check (#5787)
- Bump gaurav-nelson/github-action-markdown-link-check (#5793)
- Bump gaurav-nelson/github-action-markdown-link-check (#233)
- Bump github.com/cosmos/iavl from 0.15.0 to 0.15.2
- Bump codecov/codecov-action from v1.0.15 to v1.1.1 (#5825)

### Ci

- Make timeout-minutes 8 for golangci (#5821)
- Run `goreleaser build` (#5824)

### Cmd

- Hyphen case cli and config (#5777)
- Hyphen-case cli  v0.34.1 (#5786)

### Config

- Increase MaxPacketMsgPayloadSize to 1400

### Consensus

- Change log level to error when adding vote
- Deprecate time iota ms (#5792)

### Localnet

- Fix node starting issue with --proxy-app flag (#5803)
- Expose 6060 (pprof) and 9090 (prometheus) on node0
- Use 27000 port for prometheus (#5811)

### Mempool

- Introduce KeepInvalidTxsInCache config option (#5813)
- Disable MaxBatchBytes (#5800)
- Introduce KeepInvalidTxsInCache config option (#5813)
- Disable MaxBatchBytes (#5800)

### P2p

- Implement new Transport interface (#5791)
- Remove `NodeInfo` interface and rename `DefaultNodeInfo` struct (#5799)
- Do not format raw msg bytes
- Update frame size (#235)
- Fix data race in MakeSwitch test helper (#5810)
- Add MemoryTransport, an in-memory transport for testing (#5827)

### Readme

- Add links to job post (#5785)
- Update discord link (#5795)

## [0.34.1-dev1] - 2020-12-10

### .goreleaser

- Add windows, remove arm (32 bit) (#5692)

### CONTRIBUTING

- Update to match the release flow used for 0.34.0 (#5697)

### Documentation

- Add nodes section  (#5604)
- Add version dropdown and v0.34 docs(#5762)
- Fix link (#5763)

### README

- Update link to Tendermint blog (#5713)

### Security

- Bump vuepress-theme-cosmos from 1.0.176 to 1.0.177 in /docs (#5746)
- Bump vuepress-theme-cosmos from 1.0.177 to 1.0.178 in /docs (#5754)

### Testing

- Switched node keys back to edwards (#4)
- Enable v1 and v2 blockchains (#5702)
- Fix TestByzantinePrevoteEquivocation flake (#5710)
- Fix TestByzantinePrevoteEquivocation flake (#5710)
- Fix integration tests and rename binary

### UX

- Version configuration (#5740)

### Abci

- Add abci_version to requestInfo (#223)
- Modify Client interface and socket client (#5673)

### Adr

- Privval gRPC (#5712)

### Blockchain/v0

- Relax termination conditions and increase sync timeout (#5741)

### Blockchain/v1

- Handle peers without blocks (#5701)
- Fix deadlock (#5711)
- Remove in favor of v2 (#5728)
- Omit incoming message bytes from log

### Build

- Bump github.com/cosmos/iavl from 0.15.0-rc5 to 0.15.0 (#5708)
- Bump vuepress-theme-cosmos from 1.0.175 to 1.0.176 in /docs (#5727)
- Refactor BLS library/bindings integration  (#9)
- BLS scripts - Improve build.sh, Fix install.sh (#10)
- Dashify some files (#11)
- Fix docker image and docker.yml workflow (#12)
- Fix coverage.yml, bump go version, install BLS, drop an invalid character (#19)
- Fix test.yml, bump go version, install BLS, fix job names (#18)
- Bump google.golang.org/grpc from 1.33.2 to 1.34.0 (#5737)
- Bump gaurav-nelson/github-action-markdown-link-check (#22)
- Bump codecov/codecov-action from v1.0.13 to v1.0.15 (#23)
- Bump golangci/golangci-lint-action from v2.2.1 to v2.3.0 (#24)
- Bump rtCamp/action-slack-notify from e9db0ef to 2.1.1 (#25)
- Bump google.golang.org/grpc from 1.33.2 to 1.34.0 (#26)
- Bump vuepress-theme-cosmos from 1.0.173 to 1.0.177 in /docs (#27)
- Bump watchpack from 2.0.1 to 2.1.0 in /docs (#5768)
- Bump rtCamp/action-slack-notify from ecc1353ce30ef086ce3fc3d1ea9ac2e32e150402 to 2.1.2 (#5767)

### Changelog

- Add entry back (#5738)

### Ci

- Remove circle (#5714)
- Build for 32 bit, libs: fix overflow (#5700)
- Build for 32 bit, libs: fix overflow (#5700)
- Install BLS library in lint.yml and bump its go version (#15)
- E2e fixes - docker image, e2e.yml BLS library, default KeyType (#21)

### Cmd

- Modify `gen_node_key` to print key to STDOUT (#5772)

### Codecov

- Validate codecov.yml (#5699)

### Consensus

- Fix flaky tests (#5734)

### Contributing

- Simplify our minor release process (#5749)

### Crypto

- Fix infinite recursion in Secp256k1 string formatting (#5707)
- Fix infinite recursion in Secp256k1 string formatting (#5707) (#5709)

### Crypto|p2p|state|types

- Rename Pub/PrivKey's TypeIdentifier() and Type()

### Dep

- Bump ed25519consensus version (#5760)

### Evidence

- Omit bytes field (#5745)
- Omit bytes field (#5745)

### Goreleaser

- Lowercase binary name (#5765)

### Libs/bits

- Validate BitArray in FromProto (#5720)

### Light

- Minor fixes / standardising errors (#5716)

### Lint

- Run gofmt and goimports  (#13)

### Linter

- Fix some bls-related linter issues (#14)

### Node

- Improve test coverage on proposal block (#5748)

### P2p

- State sync reactor refactor (#5671)

### P2p/pex

- Fix flaky tests (#5733)

### Reactors

- Omit incoming message bytes from reactor logs (#5743)
- Omit incoming message bytes from reactor logs (#5743)

### Readme

- Remover circleci badge (#5729)

### Version

- Add abci version to handshake (#5706)

## [0.34.0] - 2020-11-19

### .github

- Move mergify config
- Move codecov.yml into .github
- Move codecov config into .github

### .gitignore

- Sort (#5690)

### .goreleaser

- Don't build linux/arm
- Build for windows

### .vscode

- Remove directory (#5626)

### CHANGELOG

- Update to reflect v0.34.0-rc6 (#5622)
- Add breaking Version name change (#5628)

### Core

- Move validation & data structures together (#176)

### Documentation

- Update url for kms repo (#5510)
- Footer cleanup (#5457)
- Remove DEV_SESSIONS list (#5579)
- Add ADR on P2P refactor scope (#5592)
- Bump vuepress-theme-cosmos (#5614)
- Make blockchain not viewable (#211)
- Add missing ADRs to README, update status of ADR 034 (#5663)
- Add P2P architecture ADR (#5637)
- Warn developers about calling blocking funcs in Receive (#5679)

### RFC

- Adopt zip 215 (#144)

### Security

- Bump github.com/golang/protobuf from 1.4.2 to 1.4.3 (#5506)
- Bump github.com/spf13/cobra from 1.0.0 to 1.1.0 (#5505)
- Bump github.com/prometheus/client_golang from 1.7.1 to 1.8.0 (#5515)
- Bump github.com/spf13/cobra from 1.1.0 to 1.1.1 (#5526)
- Bump google.golang.org/grpc from 1.33.1 to 1.33.2 (#5635)
- Bump github.com/golang/protobuf from 1.4.2 to 1.4.3 (#5506)
- Bump github.com/spf13/cobra from 1.0.0 to 1.1.0 (#5505)
- Bump github.com/prometheus/client_golang from 1.7.1 to 1.8.0 (#5515)
- Bump github.com/spf13/cobra from 1.1.0 to 1.1.1 (#5526)
- Bump google.golang.org/grpc from 1.33.1 to 1.33.2 (#5635)

### Testing

- Clean up E2E test volumes using a container (#5509)
- Tweak E2E tests for nightly runs (#5512)
- Enable ABCI gRPC client in E2E testnets (#5521)
- Enable blockchain v2 in E2E testnet generator (#5533)
- Enable restart/kill perturbations in E2E tests (#5537)
- Add end-to-end testing framework (#5435)
- Add basic end-to-end test cases (#5450)
- Add GitHub action for end-to-end tests (#5452)
- Remove P2P tests (#5453)
- Add E2E test for node peering (#5465)
- Add random testnet generator (#5479)
- Clean up E2E test volumes using a container (#5509)
- Tweak E2E tests for nightly runs (#5512)
- Enable ABCI gRPC client in E2E testnets (#5521)
- Enable blockchain v2 in E2E testnet generator (#5533)
- Enable restart/kill perturbations in E2E tests (#5537)
- Run remaining E2E testnets on run-multiple.sh failure (#5557)
- Tag E2E Docker resources and autoremove them (#5558)
- Add evidence e2e tests (#5488)
- Run remaining E2E testnets on run-multiple.sh failure (#5557)
- Tag E2E Docker resources and autoremove them (#5558)
- Add evidence e2e tests (#5488)
- Fix handling of start height in generated E2E testnets (#5563)
- Disable E2E misbehaviors due to bugs (#5569)
- Fix handling of start height in generated E2E testnets (#5563)
- Disable E2E misbehaviors due to bugs (#5569)
- Fix various E2E test issues (#5576)
- Fix various E2E test issues (#5576)
- Fix secp failures (#5649)
- Fix secp failures (#5649)

### Abci

- Lastcommitinfo.round extra sentence (#221)

### Abci/grpc

- Return async responses in order (#5520)
- Return async responses in order (#5520) (#5531)
- Fix ordering of sync/async callback combinations (#5556)
- Fix ordering of sync/async callback combinations (#5556)

### Block

- Fix max commit sig size (#5567)
- Fix max commit sig size (#5567)

### Blockchain/v1

- Add noBlockResponse handling  (#5401)

### Blockchain/v2

- Fix "panic: duplicate block enqueued by processor" (#5499)
- Fix panic: processed height X+1 but expected height X (#5530)
- Fix "panic: duplicate block enqueued by processor" (#5499)
- Fix panic: processed height X+1 but expected height X (#5530)
- Make the removal of an already removed peer a noop (#5553)
- Make the removal of an already removed peer a noop (#5553)
- Remove peers from the processor  (#5607)
- Remove peers from the processor  (#5607)

### Buf

- Modify buf.yml, add buf generate (#5653)

### Build

- Bump codecov/codecov-action from v1.0.13 to v1.0.14 (#5525)
- Bump gaurav-nelson/github-action-markdown-link-check from 1.0.7 to 1.0.8 (#5543)
- Bump gaurav-nelson/github-action-markdown-link-check from 1.0.7 to 1.0.8 (#188)
- Bump google.golang.org/grpc from 1.32.0 to 1.33.1 (#5544)
- Bump actions/cache from v2.1.1 to v2.1.2 (#5487)
- Bump golangci/golangci-lint-action from v2.2.0 to v2.2.1 (#5486)
- Bump technote-space/get-diff-action from v3 to v4 (#5485)
- Bump golangci/golangci-lint-action from v2.2.1 to v2.3.0 (#5571)
- Bump codecov/codecov-action from v1.0.13 to v1.0.14 (#5582)
- Bump watchpack from 2.0.0 to 2.0.1 in /docs (#5605)
- Bump actions/cache from v2.1.2 to v2.1.3 (#5633)
- Bump github.com/tendermint/tm-db from 0.6.2 to 0.6.3
- Bump rtCamp/action-slack-notify from e9db0ef to 2.1.1
- Bump google.golang.org/grpc from 1.32.0 to 1.33.1 (#5544)
- Bump github.com/tendermint/tm-db from 0.6.2 to 0.6.3
- Bump codecov/codecov-action from v1.0.14 to v1.0.15 (#5676)

### Changelog

- Squash changelog from 0.34 RCs into one (#5691)
- Squash changelog from 0.34 RCs into one (#5687)

### Ci

- Docker remove circleci and add github action (#5551)
- Add goreleaser (#5527)
- Tests (#5577)
- Add goreleaser (#5527)
- Tests (#5577)
- Use gh pages (#5609)
- Remove `add-path` (#5674)
- Remove `add-path` (#5674)

### Ci/e2e

- Avoid running job when no go files are touched (#5471)

### Circleci

- Remove Gitian reproducible_builds job (#5462)

### Cli

- Light home dir should default to where the full node default is (#5392)

### Cmd

- Add support for --key (#5612)

### Consensus

- Open target WAL as read/write during autorepair (#5536)
- Open target WAL as read/write during autorepair (#5536) (#5547)

### Contributing

- Include instructions for a release candidate (#5498)

### Crypto

- Add in secp256k1 support (#5500)
- Add in secp256k1 support (#5500)
- Adopt zip215 ed25519 verification (#5632)

### E2e

- Use ed25519 for secretConn (remote signer) (#5678)
- Use ed25519 for secretConn (remote signer) (#5678)

### Encoding

- Add secp, ref zip215, tables (#212)

### Evidence

- Don't gossip consensus evidence too soon (#5528)
- Don't send committed evidence and ignore inbound evidence that is already committed (#5574)
- Don't gossip consensus evidence too soon (#5528)
- Don't send committed evidence and ignore inbound evidence that is already committed (#5574)
- Structs can independently form abci evidence (#5610)
- Structs can independently form abci evidence (#5610)
- Update data structures to reflect added support of abci evidence (#213)

### Github

- Rename e2e jobs (#5502)
- Add nightly E2E testnet action (#5480)
- Add nightly E2E testnet action (#5480)
- Rename e2e jobs (#5502)
- Only notify nightly E2E failures once (#5559)
- Only notify nightly E2E failures once (#5559)
- Issue template for proposals (#190)

### Go.mod

- Upgrade iavl and deps (#5657)
- Upgrade iavl and deps (#5657)

### Libs/os

- Add test case for TrapSignal (#5646)
- Remove unused aliases, add test cases (#5654)

### Light

- Cross-check the very first header (#5429)
- Model-based tests (#5461)
- Run detector for sequentially validating light client (#5538)
- Run detector for sequentially validating light client (#5538) (#5601)
- Make fraction parts uint64, ensuring that it is always positive (#5655)
- Make fraction parts uint64, ensuring that it is always positive (#5655)
- Ensure required header fields are present for verification (#5677)

### Light/rpc

- Fix ABCIQuery (#5375)

### P2p

- Remove p2p.FuzzedConnection and its config settings (#5598)
- Remove unused MakePoWTarget() (#5684)

### Privval

- Make response values non nullable (#5583)
- Make response values non nullable (#5583)
- Increase read/write timeout to 5s and calculate ping interval based on it (#5638)
- Reset pingTimer to avoid sending unnecessary pings (#5642)
- Increase read/write timeout to 5s and calculate ping interva… (#5666)
- Reset pingTimer to avoid sending unnecessary pings (#5642) (#5668)
- Duplicate SecretConnection from p2p package (#5672)

### Proto

- Buf for everything (#5650)

### Relase_notes

- Add release notes for v0.34.0

### Rpc

- Fix content-type header (#5661)
- Fix content-type header

### Scripts

- Move build.sh into scripts
- Make linkifier default to 'pull' rather than 'issue' (#5689)

### Spec

- Update light client verification to match supervisor (#171)

### Statesync

- Check all necessary heights when adding snapshot to pool (#5516)
- Check all necessary heights when adding snapshot to pool (#5516) (#5518)

### Types

- Rename json parts to part_set_header (#5523)
- Move `MakeBlock` to block.go (#5573)

### Upgrading

- Update 0.34 instructions with updates since RC4 (#5685)
- Update 0.34 instructions with updates since RC4 (#5686)

## [0.34.0-rc5] - 2020-10-13

### Documentation

- Minor tweaks (#5404)
- Update state sync config with discovery_time (#5405)
- Add explanation of p2p configuration options (#5397)
- Specify TM version in go tutorials (#5427)
- Revise ADR 56, documenting short term decision around amnesia evidence  (#5440)
- Fix links to adr 56 (#5464)
- Docs-staging → master (#5468)
- Make /master the default (#5474)

### Testing

- Add end-to-end testing framework (#5435)
- Add basic end-to-end test cases (#5450)
- Add GitHub action for end-to-end tests (#5452)
- Remove P2P tests (#5453)
- Add E2E test for node peering (#5465)
- Add random testnet generator (#5479)

### Abci

- Remove setOption (#5447)

### Block

- Use commit sig size instead of vote size (#5490)

### Blockchain

- Remove duplication of validate basic (#5418)

### Blockchain/v1

- Add noBlockResponse handling  (#5401)

### Build

- Bump watchpack from 1.7.4 to 2.0.0 in /docs (#5470)
- Bump actions/cache from v2.1.1 to v2.1.2 (#5487)
- Bump golangci/golangci-lint-action from v2.2.0 to v2.2.1 (#5486)
- Bump technote-space/get-diff-action from v3 to v4 (#5485)

### Changelog

- Add missing date to v0.33.5 release, fix indentation (#5454)
- Add missing date to v0.33.5 release, fix indentation (#5454) (#5455)
- Prepare changelog for RC5 (#5494)

### Ci

- Docker remvoe circleci and add github action (#5420)

### Ci/e2e

- Avoid running job when no go files are touched (#5471)

### Circleci

- Remove Gitian reproducible_builds job (#5462)

### Cli

- Light home dir should default to where the full node default is (#5392)

### Codecov

- Disable annotations (#5413)

### Config

- Set statesync.rpc_servers when generating config file (#5433)
- Set statesync.rpc_servers when generating config file (#5433) (#5438)

### Consensus

- Check block parts don't exceed maximum block bytes (#5431)
- Check block parts don't exceed maximum block bytes (#5436)

### Evidence

- Update data structures (#165)
- Use bytes instead of quantity to limit size (#5449)
- Use bytes instead of quantity to limit size (#5449)(#5476)

### Light

- Expand on errors and docs (#5443)
- Cross-check the very first header (#5429)

### Light/rpc

- Fix ABCIQuery (#5375)

### Mempool

- Fix nil pointer dereference (#5412)
- Fix nil pointer dereference (#5412)
- Length prefix txs when getting them from mempool (#5483)

### Privval

- Allow passing options to NewSignerDialerEndpoint (#5434)
- Allow passing options to NewSignerDialerEndpoint (#5434) (#5437)
- Fix ping message encoding (#5441)
- Fix ping message encoding (#5442)

### Rpc/core

- More docs and a test for /blockchain endpoint (#5417)

### Spec

- Protobuf changes (#156)

### State

- More test cases for block validation (#5415)

### Tx

- Reduce function to one parameter (#5493)

## [0.34.0-rc4] - 2020-09-24

### CHANGELOG

- Update for 0.34.0-rc4 (#5400)

### CODEOWNERS

- Specify more precise codeowners (#5333)

### Documentation

- Cleanup (#5252)
- Dont display duplicate  (#5271)
- Rename swagger to openapi (#5263)
- Fix go tutorials (#5267)
- Versioned (#5241)
- Remove duplicate secure p2p (#5279)
- Remove interview transcript (#5282)
- Add block retention to upgrading.md (#5284)
- Updates to various sections (#5285)
- Add algolia docsearch configs (#5309)
- Add sections to abci (#150)
- Add doc on state sync configuration (#5304)
- Move subscription to tendermint-core (#5323)
- Add missing metrics (#5325)
- Add more description to initial_height (#5350)
- Make rfc section disppear (#5353)
- Document max entries for `/blockchain` RPC (#5356)
- Fix incorrect time_iota_ms configuration (#5385)

### README

- Clean up README (#5391)

### Security

- Bump vuepress-theme-cosmos from 1.0.169 to 1.0.172 in /docs (#5286)
- Bump google.golang.org/grpc from 1.31.0 to 1.31.1 (#5290)

### UPGRADING

- Polish upgrading instructions for 0.34 (#5398)

### Abci

- Update evidence (#5324)
- Fix socket client error for state sync responses (#5395)

### Adr

- Add API stability ADR (#5341)

### Blockchain

- Fix fast sync halt with initial height > 1 (#5249)
- Verify +2/3 (#5278)

### Blockstore

- Fix race conditions when loading data (#5382)

### Build

- Bump golangci/golangci-lint-action from v2.1.0 to v2.2.0 (#5245)
- Bump actions/cache from v1 to v2.1.0 (#5244)
- Bump codecov/codecov-action from v1.0.7 to v1.0.12 (#5247)
- Bump technote-space/get-diff-action from v1 to v3 (#5246)
- Bump gaurav-nelson/github-action-markdown-link-check from 0.6.0 to 1.0.5 (#5248)
- Bump codecov/codecov-action from v1.0.12 to v1.0.13 (#5258)
- Bump gaurav-nelson/github-action-markdown-link-check from 1.0.5 to 1.0.6 (#5265)
- Bump gaurav-nelson/github-action-markdown-link-check from 1.0.6 to 1.0.7 (#5269)
- Bump actions/cache from v2.1.0 to v2.1.1 (#5268)
- Bump gaurav-nelson/github-action-markdown-link-check from 0.6.0 to 1.0.7 (#149)
- Bump github.com/tendermint/tm-db from 0.6.1 to 0.6.2 (#5296)
- Bump google.golang.org/grpc from 1.31.1 to 1.32.0 (#5346)
- Bump github.com/minio/highwayhash from 1.0.0 to 1.0.1 (#5370)
- Bump vuepress-theme-cosmos from 1.0.172 to 1.0.173 in /docs (#5390)

### Changelog

- Add v0.33.8 from release (#5242)
- Minor tweaks (#5389)

### Ci

- Fix net pipeline (#5272)
- Delay codecov notification  (#5275)
- Add markdown linter (#146)
- Add dependabot config (#148)
- Fix net run (#5343)

### Config

- Trust period consistency (#5297)
- Rename prof_laddr to pprof_laddr and move it to rpc (#5315)
- Set time_iota_ms to timeout_commit in test genesis (#5386)
- Add state sync discovery_time setting (#5399)

### Consensus

- Double-sign risk reduction (ADR-51) (#5147)
- Fix wrong proposer schedule for `InitChain` validators (#5329)

### Crypto

- Remove secp256k1 (#5280)
- Remove proto privatekey (#5301)
- Reword readme (#5349)

### Evidence

- Modularise evidence by moving verification function into evidence package (#5234)
- Remove ConflictingHeaders type (#5317)
- Remove lunatic  (#5318)
- Remove amnesia & POLC (#5319)
- Introduction of LightClientAttackEvidence and refactor of evidence lifecycle (#5361)

### Header

- Check block protocol (#5340)

### Libs/bits

- Inline defer and change order of mutexes (#5187)

### Light

- Update ADR 47 with light traces (#5250)
- Implement light block (#5298)
- Move dropout handling and invalid data to the provider (#5308)

### Lint

- Add markdown linter (#5254)
- Add errchecks (#5316)
- Enable errcheck (#5336)

### Makefile

- Add options for other DBs (#5357)

### Markdownlint

- Ignore .github directory (#5351)

### Mempool

- Return an error when WAL fails (#5292)
- Batch txs per peer in broadcastTxRoutine (#5321)

### Mempool/reactor

- Fix reactor broadcast test (#5362)

### Metrics

- Switch from gauge to histogram (#5326)

### Mocks

- Update with 2.2.1 (#5294)

### Node

- Fix genesis state propagation to state sync (#5302)

### P2p

- Reduce log severity (#5338)

### Privval

- Add chainID to requests (#5239)

### Rfc

- Add end-to-end testing RFC (#5337)

### Rpc

- Add private & unconditional to /dial_peer (#5293)
- Fix openapi spec syntax error (#5358)
- Fix test data races (#5363)
- Revert JSON-RPC/WebSocket response batching (#5378)

### Rpc/client

- Take context as first param (#5347)

### Rpc/jsonrpc/server

- Ws server optimizations (#5312)

### Spec

- Update abci events (#151)
- Extract light-client to its own directory (#152)
- Remove evidences (#153)
- Light client attack detector (#164)

### Spec/reactors/mempool

- Batch txs per peer (#155)

### State

- Define interface for state store (#5348)

### Statesync

- Fix valset off-by-one causing consensus failures (#5311)
- Broadcast snapshot request to all peers on startup (#5320)
- Fix the validator set heights (again) (#5330)

### Swagger

- Update (#5257)

### Types

- Comment on need for length prefixing (#5283)

### Upgrading

- State store change (#5364)

### Ux

- Use docker to format proto files (#5384)

## [0.34.0-rc3] - 2020-08-13

### RFC-002

- Non-zero genesis (#119)

### Security

- [Security] Bump prismjs from 1.20.0 to 1.21.0 in /docs

### Testing

- Protobuf vectors for reactors (#5221)

### Abci

- Fix abci evidence types (#5174)
- Add ResponseInitChain.app_hash, check and record it (#5227)
- Add ResponseInitChain.app_hash (#140)

### Build

- Bump google.golang.org/grpc from 1.30.0 to 1.31.0
- Bump github.com/spf13/viper from 1.7.0 to 1.7.1

### Changelog

- Add v0.33.7 release (#5203)
- Add v0.32.13 release (#5204)
- Update for 0.34.0-rc3 (#5240)

### Ci

- Freeze golangci action version (#5196)

### Consensus

- Don't check InitChain app hash vs genesis app hash, replace it (#5237)

### Contributing

- Add steps for adding and removing rc branches (#5223)

### Crypto

- Consistent api across keys (#5214)
- API modifications (#5236)

### Db

- Add support for badgerdb (#5233)

### Evidence

- Remove phantom validator evidence (#5181)
- Don't stop evidence verification if an evidence fails (#5189)
- Fix usage of time field in abci evidence (#5201)
- Change evidence time to block time (#5219)
- Remove validator index verification (#5225)

### Genesis

- Add support for arbitrary initial height (#5191)

### Libs/rand

- Fix "out-of-memory" error on unexpected argument (#5215)

### Merkle

- Return hashes for empty merkle trees (#5193)

### Node

- Don't attempt fast sync when InitChain sets self as only validator (#5211)

### Rpc/client/http

- Log error (#5182)

### Spec

- Revert event hashing (#132)

### State

- Don't save genesis state in database when loaded (#5231)

## [0.34.0-rc2] - 2020-07-30

### .github/issue_template

- Update `/dump_consensus_state` request. (#5060)

### ADR

- Add missing numbers as blank templates (#5154)

### ADR-057

- RPC (#4857)

### CHANGELOG_PENDING

- Fix the upcoming release number (#5103)

### Documentation

- Update .vuepress/config.js (#5043)
- Add warning for chainid (#5072)
- Added further documentation to the subscribing to events page (#5110)
- Tweak light client documentation (#5121)
- Modify needed proto files for guides (#5123)
- EventAttribute#Index is not deterministic (#5132)
- Event hashing ADR 058 (#5134)
- Simplify choosing an ADR number (#5156)
- Add more details on Vote struct from /consensus_state (#5164)
- Document ConsensusParams (#5165)
- Document canonical field (#5166)

### README

- Update chat link with Discord instead of Riot (#5071)

### Security

- [Security] Bump lodash from 4.17.15 to 4.17.19 in /docs

### Testing

- Use github.sha in binary cache key (#5062)
- Deflake TestAddAndRemoveListenerConcurrency and TestSyncer_SyncAny (#5101)

### Abci

- Tweak node sync estimate (#115)

### Abci/example/kvstore

- Decrease val power by 1 upon equivocation (#5056)

### Abci/types

- Add comment for TotalVotingPower (#5081)

### Behaviour

- Add simple doc.go (#5055)

### Blockchain

- Test vectors for proto encoding (#5073)
- Rename to core (#123)
- Remove duplicate evidence sections (#124)

### Build

- Bump github.com/prometheus/client_golang
- Bump vuepress-theme-cosmos from 1.0.168 to 1.0.169 in /docs

### Changelog

- Update 0.33.6 (#5075)
- Note breaking change in the 0.33.6 release (#5077)
- Reorgranize (#5065)
- Move entries from pending  (#5172)
- Bump to 0.34.0-rc2 (#5176)

### Ci

- Try to fix codecov (#5095)
- Only run tests when go files are touched (#5097)
- Version linter fix  (#5128)

### Consensus

- Do not allow signatures for a wrong block in commits
- Msg testvectors (#5076)
- Added byzantine test, modified previous test (#5150)
- Only call privValidator.GetPubKey once per block (#5143)

### Deps

- Bump tm-db to 0.6.0 (#5058)

### Evidence

- Fix data race in Pool.updateValToLastHeight() (#5100)
- Check lunatic vote matches header (#5093)
- New evidence event subscription (#5108)
- Minor correction to potential amnesia ev validate basic (#5151)

### Jsonrpc

- Change log to debug (#5131)

### Libs

- Wrap mutexes for build flag with godeadlock (#5126)

### Light

- Fix rpc calls: /block_results & /validators (#5104)
- Use bisection (not VerifyCommitTrusting) when verifying a head… (#5119)
- Return if target header is invalid (#5124)

### Lint

- Errcheck  (#5091)

### Linter

- (1/2) enable errcheck (#5064)

### Mempool

- Make it clear overwriting of pre/postCheck filters is intent… (#5054)
- Use oneof (#5063)
- Add RemoveTxByKey function (#5066)

### P2p

- Remove data race bug in netaddr stringer (#5048)
- Ensure peers can't change IP of known nodes (#5136)

### Privval

- If remote signer errors, don't retry (#5140)

### Proto

- Increase lint level to basic and fix lint warnings (#5096)
- Improve enums (#5099)
- Reorganize Protobuf schemas (#5102)
- Minor cleanups (#5105)
- Change type + a cleanup (#5107)
- Add a comment for Validator#Address (#5144)

### Proto/tendermint/abci

- Fix Request oneof numbers (#5116)

### Proxy

- Improve ABCI app connection handling (#5078)

### Rpc

- Move docs from doc.go to swagger.yaml (#5044)
- /broadcast_evidence nil evidence check (#5109)
- Make gasWanted/Used snake_case (#5137)

### Rpc/jsonrpc/server

- Merge WriteRPCResponseHTTP and WriteRPCResponseAr (#5141)

### Spec/abci

- Expand on Validator#Address (#118)

### Spec/consensus

- Canonical vs subjective commit

### State

- Revert event hashing (#5159)

### Types

- Simplify safeMul (#5061)
- Verify commit fully
- Validatebasic on from proto (#5152)
- Check if nil or empty valset (#5167)

### Version

- Bump version numbers (#5173)

## [0.34.0-dev1] - 2020-06-24

### .github

- Move checklist from PR description into an auto-comment (#4745)
- Fix whitespace for autocomment (#4747)
- Fix whitespace for auto-comment (#4750)

### ADR-053

- Strengthen and simplify the state sync ABCI interface (#4610)

### Bug Fixes

- Fix spelling of comment (#4566)

### CHANGELOG

- Update to reflect 0.33.5 (#4915)
- Add 0.32.12 changelog entry (#4918)

### CONTRIBUTING

- Update minor release process (#4909)

### Documentation

- Validator setup & Key info (#4604)
- Add adr-55 for proto repo design (#4623)
- Amend adr-54 with changes in the sdk (#4684)
- Create adr 56: prove amnesia attack
- Mention unbonding period in MaxAgeNumBlocks/MaxAgeDuration
- State we don't support non constant time crypto
- Move tcp-window.png to imgs/
- Document open file limit in production guide (#4945)
- Update amnesia adr (#4994)

### Makefile

- Parse TENDERMINT_BUILD_OPTIONS (#4738)

### README

- Specify supported versions (#4660)

### RFC-001

- Configurable block retention (#84)

### Security

- [Security] Bump websocket-extensions from 0.1.3 to 0.1.4 in /docs (#4976)

### Testing

- Fix p2p test build breakage caused by Debian testing
- Revert Go 1.13→1.14 bump
- Use random socket names to avoid collisions (#4885)
- Mitigate test data race (#4886)

### UPGRADING.md

- Write about the LastResultsHash change (#5000)

### Abci

- Add basic description of ABCI Commit.ResponseHeight (#85)
- Add MaxAgeNumBlocks/MaxAgeDuration to EvidenceParams (#87)
- Update MaxAgeNumBlocks & MaxAgeDuration docs (#88)
- Fix protobuf lint issues
- Regenerate proto files
- Remove protoreplace script
- Remove python examples
- Proto files follow same path  (#5039)
- Add AppVersion to ConsensusParams (#106)

### Abci/server

- Print panic & stack trace to STDERR if logger is not set

### Adr-053

- Update after state sync merge (#4768)

### All

- Name reactors when they are initialized (#4608)

### Blockchain

- Enable v2 to be set (#4597)
- Change validator set sorting method (#91)
- Proto migration  (#4969)

### Blockchain/v2

- Allow setting nil switch, for CustomReactors()
- Don't broadcast base if height is 0
- Fix excessive CPU usage due to spinning on closed channels (#4761)
- Respect fast_sync option (#4772)
- Integrate with state sync
- Correctly set block store base in status responses (#4971)

### Blockchain[v1]

- Increased timeout times for peer tests (#4871)

### Blockstore

- Allow initial SaveBlock() at any height

### Build

- Bump github.com/Workiva/go-datastructures (#4545)
- Bump google.golang.org/grpc from 1.27.1 to 1.28.0 (#4551)
- Bump github.com/tendermint/tm-db from 0.4.1 to 0.5.0 (#4554)
- Bump github.com/golang/protobuf from 1.3.4 to 1.3.5 (#4563)
- Bump github.com/prometheus/client_golang (#4574)
- Bump github.com/gorilla/websocket from 1.4.1 to 1.4.2 (#4584)
- Bump github.com/spf13/cobra from 0.0.6 to 0.0.7 (#4612)
- Bump github.com/tendermint/tm-db from 0.5.0 to 0.5.1 (#4613)
- Bump google.golang.org/grpc from 1.28.0 to 1.28.1 (#4653)
- Bump github.com/spf13/viper from 1.6.2 to 1.6.3 (#4664)
- Bump @vuepress/plugin-google-analytics in /docs (#4692)
- Bump google.golang.org/grpc from 1.28.1 to 1.29.0
- Bump google.golang.org/grpc from 1.29.0 to 1.29.1 (#4735)
- Manually bump github.com/prometheus/client_golang from 1.5.1 to 1.6.0 (#4758)
- Bump github.com/golang/protobuf from 1.4.0 to 1.4.1 (#4794)
- Bump vuepress-theme-cosmos from 1.0.163 to 1.0.164 in /docs (#4815)
- Bump github.com/spf13/viper from 1.6.3 to 1.7.0 (#4814)
- Bump github.com/golang/protobuf from 1.4.1 to 1.4.2 (#4849)
- Bump vuepress-theme-cosmos from 1.0.164 to 1.0.165 in /docs
- Bump github.com/stretchr/testify from 1.5.1 to 1.6.0
- Bump vuepress-theme-cosmos from 1.0.165 to 1.0.166 in /docs (#4920)
- Bump github.com/stretchr/testify from 1.6.0 to 1.6.1
- Bump github.com/prometheus/client_golang from 1.6.0 to 1.7.0 (#5027)
- Bump google.golang.org/grpc from 1.29.1 to 1.30.0

### Changelog

- Add entries from secruity releases

### Ci

- Transition some ci to github actions
- Only run when applicable (#4752)
- Check git diff on each job (#4770)
- Checkout code before git diff check (#4779)
- Add paths 
- Bump the timeout for test_coverage (#4864)
- Migrate localnet to github actions (#4878)
- Add timeouts (#4912)
- Migrate test_cover (#4869)
- Fix spacing of if statement (#5015)

### Cli

- Add command to generate shell completion scripts (#4665)

### Codeowners

- Add code owners (#82)

### Config

- Allow fastsync.version = v2 (#4639)

### Consensus

- Add comment as to why use mocks during replay (#4785)
- Fix TestSimulateValidatorsChange
- Fix and rename TestStateLockPOLRelock (#4796)
- Bring back log.Error statement (#4899)
- Increase ensureTimeout (#4891)
- Fix startnextheightcorrectly test (#4938)
- Attempt to repair the WAL file on data corruption (#4682)
- Change logging and handling of height mismatch (#4954)
- Stricter on LastCommitRound check (#4970)
- Proto migration (#4984)

### Cov

- Ignore autogen file (#5033)

### Crypto

- Remove SimpleHashFromMap() and SimpleProofsFromMap()
- Remove key suffixes (#4941)
- Removal of multisig (#4988)

### Crypto/merkle

- Remove simple prefix (#4989)

### Dep

- Bump protobuf, cobra, btcutil & std lib deps (#4676)

### Deps

- Bump deps that bot cant (#4555)
- Run go mod tidy (#4587)

### Encoding

- Remove codecs (#4996)

### Evidence

- Both MaxAgeDuration and MaxAgeNumBlocks need to be surpassed (#4667)
- Handling evidence from light client(s) (#4532)
- Remove unused param (#4726)
- Remove pubkey from duplicate vote evidence
- Add doc.go
- Protect valToLastHeight w/ mtx
- Check evidence is pending before validating evidence
- Refactor evidence mocks throughout packages (#4787)
- Cap evidence to an absolute number (#4780)
- Create proof of lock change and implement it in evidence store (#4746)
- Prevent proposer from proposing duplicate pieces of evidence (#4839)
- Remove header from phantom evidence (#4892)
- Retrieve header at height of evidence for validation (#4870)
- Json tags for DuplicateVoteEvidence (#4959)
- Migrate reactor to proto (#4949)
- Adr56 form amnesia evidence (#4821)
- Improve amnesia evidence handling (#5003)
- Replace mock evidence with mocked duplicate vote evidence (#5036)

### Format

- Add format cmd & goimport repo (#4586)

### Indexer

- Allow indexing an event at runtime (#4466)
- Remove index filtering (#5006)

### Ints

- Stricter numbers (#4939)

### Json

- Add Amino-compatible encoder/decoder (#4955)

### Keys

- Change to []bytes  (#4950)

### Libs

- Remove bech32
- Remove kv (#4874)

### Libs/kv

- Remove unused type KI64Pair (#4542)

### Light

- Rename lite2 to light & remove lite (#4946)
- Implement validate basic (#4916)
- Migrate to proto (#4964)
- Added more tests for pruning, initialization and bisection (#4978)

### Lint

- Add review dog (#4652)
- Enable nolintlinter, disable on tests
- Various fixes

### Linting

- Remove unused variable

### Lite

- Fix HTTP provider error handling

### Lite2

- Add benchmarking tests (#4514)
- Cache headers in bisection (#4562)
- Use bisection for some of backward verification (#4575)
- Make maxClockDrift an option (#4616)
- Prevent falsely returned double voting error (#4620)
- Default to http scheme in provider.New (#4649)
- Verify ConsensusHash in rpc client
- Fix pivot height during bisection (#4850)
- Correctly return the results of the "latest" block (#4931)
- Allow bigger requests to LC proxy (#4930)
- Check header w/ witnesses only when doing bisection (#4929)
- Compare header with witnesses in parallel (#4935)

### Lite2/http

- Fix provider test by increasing the block retention value (#4890)

### Lite2/rpc

- Verify block results and validators (#4703)

### Mempool

- Reserve IDs in InitPeer instead of AddPeer
- Move mock into mempool directory
- Allow ReapX and CheckTx functions to run in parallel
- Do not launch broadcastTxRoutine if Broadcast is off

### Mergify

- Use PR title and body for squash merge commit (#4669)

### P2p

- PEX message abuse should ban as well as disconnect (#4621)
- Limit the number of incoming connections
- Set RecvMessageCapacity to maxMsgSize in all reactors
- Return err on `signChallenge` (#4795)
- Return masked IP (not the actual IP) in addrbook#groupKey
- TestTransportMultiplexAcceptNonBlocking and TestTransportMultiplexConnFilterTimeout (#4868)
- Remove nil guard (#4901)
- Expose SaveAs on NodeKey (#4981)
- Proto leftover (#4995)

### P2p/conn

- Add a test for MakeSecretConnection (#4829)
- Migrate to Protobuf (#4990)

### P2p/pex

- Fix DATA RACE
- Migrate to Protobuf (#4973)

### P2p/test

- Wait for listener to get ready (#4881)
- Fix Switch test race condition (#4893)

### Pex

- Use highwayhash for pex bucket

### Privval

- Return error on getpubkey (#4534)
- Remove deprecated `OldFilePV`
- Retry GetPubKey/SignVote/SignProposal N times before
- Migrate to protobuf (#4985)

### Proto

- Use docker to generate stubs (#4615)
- Bring over proto types & msgs (#4718)
- Regenerate proto (#4730)
- Remove test files
- Add proto files for ibc unblock (#4853)
- Add more to/from (#4956)
- Change to use gogofaster (#4957)
- Remove amino proto tests (#4982)
- Move keys to oneof (#4983)
- Leftover amino (#4986)
- Move all proto dirs to /proto (#5012)
- Folder structure adhere to buf (#5025)

### Reactors/pex

- Specify hash function (#94)
- Masked IP is used as group key (#96)

### Readme

- Add badge for git tests (#4732)
- Add source graph badge (#4980)

### Removal

- Remove build folder (#4565)

### Rpc

- Fix panic when `Subscribe` is called (#4570)
- Add codespace to ResultBroadcastTx (#4611)
- Handle panics during panic handling
- Use a struct to wrap all the global objects
- Refactor lib folder (#4836)
- Increase waitForEventTimeout to 8 seconds (#4917)
- Add BlockByHash to Client (#4923)
- Replace Amino with new JSON encoder (#4968)
- Support EXISTS operator in /tx_search query (#4979)
- Add /check_tx endpoint (#5017)

### Rpc/client

- Split out client packages (#4628)

### Rpc/core

- Do not lock ConsensusState mutex
- Return an error if `page=0` (#4947)

### Rpc/test

- Fix test race in TestAppCalls (#4894)
- Wait for mempool CheckTx callback (#4908)
- Wait for subscription in TestTxEventsSentWithBroadcastTxAsync (#4907)

### Spec

- Add ProofTrialPeriod to EvidenceParam (#99)
- Modify Header.LastResultsHash (#97)
- Link to abci server implementations (#100)
- Update evidence in blockchain.md (#108)

### State

- Export InitStateVersion
- Proto migration (#4951)
- Proto migration (#4972)

### Statesync

- Use Protobuf instead of Amino for p2p traffic (#4943)

### Store

- Proto migration (#4974)

### Swagger

- Remove duplicate blockID 
- Define version (#4952)

### Template

- Add labels to pr template

### Toml

- Make sections standout (#4993)

### Tools

- Remove need to install buf (#4605)
- Update gogoproto get cmd (#5007)

### Tools/build

- Delete stale tools (#4558)

### Types

- Implement Header#ValidateBasic (#4638)
- Return an error if voting power overflows
- Sort validators by voting power
- Simplify VerifyCommitTrusting
- Remove extra validation in VerifyCommit
- Assert specific error in TestValSetUpdateOverflowRelated
- Remove unnecessary sort call (#4876)
- Create ValidateBasic() funcs for validator and validator set (#4905)
- Remove VerifyFutureCommit (#4961)
- Migrate params to protobuf (#4962)
- Remove duplicated validation in VerifyCommit (#4991)
- Add tests for blockmeta (#5013)
- Remove pubkey options (#5016)
- More test cases for TestValidatorSet_VerifyCommit (#5018)
- Rename partsheader to partsetheader (#5029)
- Fix evidence timestamp calculation (#5032)
- Add AppVersion to ConsensusParams (#5031)
- Reject blocks w/ ConflictingHeadersEvidence (#5041)

### Types/test

- Remove slow test cases in TestValSetUpdatePriorityOrderTests (#4903)

### Upgrading

- Add note on rpc/client subpackages (#4636)

## [0.33.1-dev3] - 2020-03-09

### .github

- Add markdown link checker (#4513)

### CONTRIBUTING

- Include instructions for installing protobuf

### Documentation

- Adr-046 add bisection algorithm details (#4496)
- `tendermint node --help` dumps all supported flags (#4511)
- Write about debug kill and dump (#4516)
- Fix links (#4531)

### Testing

- Simplified txsearch cancellation test (#4500)

### Adr

- Crypto encoding for proto (#4481)

### Adr-047

- Evidence handling (#4429)

### Build

- Bump github.com/golang/protobuf from 1.3.3 to 1.3.4 (#4485)
- Bump github.com/prometheus/client_golang (#4525)

### Circleci

- Fix reproducible builds test (#4497)

### Cmd

- Show useful error when tm not initialised (#4512)
- Fix debug kill and change debug dump archive filename format (#4517)

### Deps

- Bump github.com/Workiva/go-datastructures (#4519)

### Example/kvstore

- Return ABCI query height (#4509)

### Lite

- Add helper functions for initiating the light client (#4486)

### Lite2

- Fix tendermint lite sub command (#4505)
- Remove auto update (#4535)
- Indicate success/failure of Update (#4536)
- Replace primary when providing invalid header (#4523)

### Mergify

- Remove unnecessary conditions (#4501)
- Use strict merges (#4502)

### Readme

- Add discord to readme (#4533)

### Rpc

- Stop txSearch result processing if context is done (#4418)
- Keep the original subscription "id" field when new RPCs come in (#4493)
- Remove BlockStoreRPC in favor of BlockStore (#4510)
- Create buffered subscriptions on /subscribe (#4521)

### Swagger

- Update swagger port (#4498)

### Tool

- Add Mergify (#4490)

## [0.33.1-dev2] - 2020-02-27

### Deps

- Bump github.com/tendermint/tm-db from 0.4.0 to 0.4.1 (#4476)

### Github

- Edit templates for use in issues and pull requests (#4483)

### Lite2

- Cross-check first header and update tests (#4471)
- Remove expiration checks on functions that don't require them (#4477)
- Prune-headers (#4478)
- Return height as 2nd return param in TrustedValidatorSet (#4479)
- Actually run example tests + clock drift (#4487)

## [0.33.1-dev1] - 2020-02-26

### ADR-053

- Update with implementation plan after prototype (#4427)

### Documentation

- Fix spec links (#4384)
- Update Light Client Protocol page (#4405)

### Adr

- Light client implementation (#4397)

### Autofile

- Resolve relative paths (#4390)

### Blockchain

- Add v2 reactor (#4361)

### Build

- Bump github.com/stretchr/testify from 1.5.0 to 1.5.1 (#4441)
- Bump github.com/spf13/cobra from 0.0.3 to 0.0.6 (#4440)

### Circleci

- Run P2P IPv4 and IPv6 tests in parallel (#4459)

### Consensus

- Reduce log severity for ErrVoteNonDeterministicSignature (#4431)

### Dep

- Bump gokit dep (#4424)
- Maunally bump dep (#4436)

### Deps

- Bump github.com/stretchr/testify from 1.4.0 to 1.5.0 (#4435)

### Evidence

- Add time to evidence params (#69)

### Lite

- Modified bisection to loop (#4400)

### Lite2

- Manage witness dropout (#4380)
- Improve string output of all existing providers (#4387)
- Modified sequence method to match bisection (#4403)
- Disconnect from bad nodes (#4388)
- Divide verify functions (#4412)
- Return already verified headers and verify earlier headers (#4428)
- Don't save intermediate headers (#4452)
- Store current validator set (#4472)

### Make

- Remove sentry setup cmds (#4383)

### Makefile

- Place phony markers after targets (#4408)

### P2p

- Use curve25519.X25519() instead of ScalarMult() (#4449)

### Proto

- Add buf and protogen script (#4369)
- Minor linting to proto files (#4386)

### Readme

- Fix link to original paper (#4391)

### Release

- Minor release 0.33.1 (#4401)

### Rpc

- Fix issue with multiple subscriptions (#4406)
- Fix tx_search pagination with ordered results (#4437)
- Fix txsearch tests (#4438)
- Fix TxSearch test nits (#4446)

### Types

- VerifyCommitX return when +2/3 sigs are verified (#4445)

## [0.33.1-dev0] - 2020-02-07

### Documentation

- Fix incorrect link (#4377)

### Lite2

- Return if there are no headers in RemoveNoLongerTrustedHeaders (#4378)

## [0.33.0-dev2] - 2020-02-07

### Documentation

- Update links to rpc (#4348)
- Update npm dependencies (#4364)
- Update guides proto paths (#4365)
- Update specs to remove cmn (#77)

### Security

- Cross-check new header with all witnesses (#4373)

### Abci

- Fix broken spec link (#4366)

### Adr

- ADR-051: Double Signing Risk Reduction (#4262)

### Build

- Bump google.golang.org/grpc from 1.26.0 to 1.27.0 (#4355)

### Deps

- Bump github.com/golang/protobuf from 1.3.2 to 1.3.3 (#4359)
- Bump google.golang.org/grpc from 1.27.0 to 1.27.1 (#4372)

### Lite2

- Add Start, TrustedValidatorSet funcs (#4337)
- Rename alternative providers to witnesses (#4344)
- Refactor cleanup() (#4343)
- Batch save & delete operations in DB store (#4345)
- Panic if witness is on another chain (#4356)
- Make witnesses mandatory (#4358)
- Replace primary provider with alternative when unavailable (#4354)
- Fetch missing headers (#4362)
- Validate TrustOptions, add NewClientFromTrustedStore (#4374)

### Node

- Use GRPCMaxOpenConnections when creating the gRPC server (#4349)

### P2p

- Merlin based malleability fixes (#72)

### Rpc

- Add sort_order option to tx_search (#4342)

## [0.33.0-dev1] - 2020-01-23

### .golangci

- Disable new linters (#4024)

### CHANGELOG

- Update release/v0.32.8 details (#4162)

### Documentation

- Fix consensus spec formatting (#3804)
- "Writing a built-in Tendermint Core application in Go" guide (#3608)
- Add guides to docs (#3830)
- Add a footer to guides (#3835)
- "Writing a Tendermint Core application in Kotlin (gRPC)" guide (#3838)
- "Writing a Tendermint Core application in Java (gRPC)" guide (#3887)
- Fix some typos and changelog entries (#3915)
- Switch the data in `/unconfirmed_txs` and `num_unconfirmed_txs` (#3933)
- Add dev sessions from YouTube (#3929)
- Move dev sessions into docs (#3934)
- Specify a fix for badger err on Windows (#3974)
- Remove traces of develop branch (#4022)
- Any path can be absolute or relative (#4035)
- Add previous dev sessions (#4040)
- Add ABCI Overview (2/2) dev session (#4044)
- Update fork-accountability.md (#4068)
- Add assumption to getting started with abci-cli (#4098)
- Fix build instructions (#4123)
- Add GA for docs.tendermint.com (#4149)
- Replace dead original whitepaper link (#4155)
- Update wording (#4174)
- Mention that Evidence votes are now sorted
- Fix broken links (#4186)
- Fix broken links in consensus/readme.md (#4200)
- Update ADR 43 with links to PRs (#4207)
- Add flag documentation (#4219)
- Fix broken rpc link (#4221)
- Fix broken ecosystem link (#4222)
- Add notes on architecture intro (#4175)
- Remove "0 means latest" from swagger docs (#4236)
- Update app-architecture.md (#4259)
- Link fixes in readme (#4268)
- Add link for installing Tendermint (#4307)
- Update theme version (#4315)
- Minor doc fixes (#4335)

### Security

- Refactor Remote signers (#3370)

### Testing

- Branch for fix of ci (#4266)
- Bind test servers to 127.0.0.1 (#4322)

### Vagrantfile

- Update Go version

### [Docs]

- Minor doc touchups (#4171)

### Abci

- Remove TotalTxs and NumTxs from Header (#3783)

### Abci/client

- Fix DATA RACE in gRPC client (#3798)

### Abci/kvstore

- Return `LastBlockHeight` and `LastBlockAppHash` in `Info` (#4233)

### Abci/server

- Recover from app panics in socket server (#3809)

### Adr

- ADR-052: Tendermint Mode (#4302)

### Adr#50

- Improve trusted peering (#4072)

### Blockchain

- Reorg reactor (#3561)

### Build

- Bump github.com/tendermint/tm-db from 0.1.1 to 0.2.0 (#4001)
- Bump github.com/gogo/protobuf from 1.3.0 to 1.3.1 (#4055)
- Bump github.com/spf13/viper from 1.4.0 to 1.5.0 (#4102)
- Bump github.com/spf13/viper from 1.5.0 to 1.6.1 (#4224)
- Bump github.com/pkg/errors from 0.9.0 to 0.9.1 (#4310)

### Changelog

- Add v0.31.9 and v0.31.8 updates (#4034)
- Fix typo (#4106)
- Explain breaking changes better
- GotVoteFromUnwantedRoundError -> ErrGotVoteFromUnwantedRound
- Add 0.32.9 changelog to master (#4305)

### Cli

- Add `--cs.create_empty_blocks_interval` flag (#4205)
- Add `--db_backend` and `--db_dir` flags to tendermint node cmd (#4235)
- Add optional `--genesis_hash` flag to check genesis hash upon startup (#4238)
- Debug sub-command (#4227)

### Cmd/debug

- Execute p.Signal only when p is not nil (#4271)

### Cmd/lite

- Switch to new lite2 package (#4300)

### Config

- Move max_msg_bytes into mempool section (#3869)
- Add rocksdb as a db backend option (#4239)

### Consensus

- Reduce "Error attempting to add vote" message severity (Er… (#3871)

### Consensus/types

- Fix BenchmarkRoundStateDeepCopy panics (#4244)

### Crypto

- Add sr25519 signature scheme (#4190)
- Fix sr25519 from raw import (#4272)

### Crypto/amino

- Add function to modify key codec (#4112)

### Cs

- Check for SkipTimeoutCommit or wait timeout in handleTxsAvailable (#3928)
- Don't panic when block is not found in store (#4163)
- Clarify where 24 comes from in maxMsgSizeBytes (wal.go)
- Set missing_validators(_power) metrics to 0 for 1st block (#4194)
- Check if cs.privValidator is nil (#4295)

### Dep

- Update tm-db to 0.4.0 (#4289)

### Deps

- Update gogo/protobuf version from v1.2.1 to v1.3.0 (#3947)
- Bump github.com/magiconair/properties from 1.8.0 to 1.8.1 (#3937)
- Bump github.com/rs/cors from 1.6.0 to 1.7.0 (#3939)
- Bump github.com/fortytw2/leaktest from 1.2.0 to 1.3.0 (#3943)
- Bump github.com/libp2p/go-buffer-pool from 0.0.1 to 0.0.2 (#3948)
- Bump google.golang.org/grpc from 1.22.0 to 1.23.0 (#3942)
- Bump github.com/gorilla/websocket from 1.2.0 to 1.4.1 (#3945)
- Bump viper to 1.4.0 and logfmt to 0.4.0 (#3950)
- Bump github.com/stretchr/testify from 1.3.0 to 1.4.0 (#3951)
- Bump github.com/go-kit/kit from 0.6.0 to 0.9.0 (#3952)
- Bump google.golang.org/grpc from 1.23.0 to 1.23.1 (#3982)
- Bump google.golang.org/grpc from 1.23.1 to 1.24.0 (#4021)
- Bump google.golang.org/grpc from 1.25.0 to 1.25.1 (#4127)
- Bump google.golang.org/grpc from 1.25.1 to 1.26.0 (#4264)
- Bump github.com/go-logfmt/logfmt from 0.4.0 to 0.5.0 (#4282)
- Bump github.com/pkg/errors from 0.8.1 to 0.9.0 (#4301)
- Bump github.com/spf13/viper from 1.6.1 to 1.6.2 (#4318)

### Evidence

- Enforce ordering in DuplicateVoteEvidence (#4151)
- Introduce time.Duration to evidence params (#4254)

### Gitian

- Update reproducible builds to build with Go 1.12.8 (#3902)

### Libs

- Remove db from tendermint in favor of tendermint/tm-cmn (#3811)

### Libs/common

- Refactor libs/common 01 (#4230)
- Refactor libs/common 2 (#4231)
- Refactor libs common 3 (#4232)
- Refactor libs/common 4 (#4237)
- Refactor libs/common 5 (#4240)

### Libs/pubsub

- Relax tx querying (#4070)

### Libs/pubsub/query

- Add EXISTS operator (#4077)

### Lint

- Golint issue fixes (#4258)

### Linters

- Enable scopelint (#3963)
- Modify code to pass maligned and interfacer (#3959)
- Enable stylecheck (#4153)

### Lite

- Follow up from #3989 (#4209)

### Lite2

- Light client with weak subjectivity (#3989)
- Move AutoClient into Client (#4326)
- Improve auto update (#4334)

### Make

- Add back tools cmd (#4281)

### Makefile

- Minor cleanup (#3994)

### Mempool

- Make max_msg_bytes configurable (#3826)
- Make `max_tx_bytes` configurable instead of `max_msg_bytes` (#3877)
- Fix memory loading error on 32-bit machines (#3969)
- Moved TxInfo parameter into Mempool.CheckTx() (#4083)

### Metrics

- Only increase last_signed_height if commitSig for block (#4283)

### Networks/remote

- Turn on GO111MODULE and use git clone instead of go get (#4203)

### P2p

- Fix error logging for connection stop (#3824)
- Do not write 'Couldn't connect to any seeds' if there are no seeds (#3834)
- Only allow ed25519 pubkeys when connecting
- Log as debug msg when address dialing is already connected (#4082)
- Make SecretConnection non-malleable (#3668)
- Add `unconditional_peer_ids` and `persistent_peers_max_dial_period` (#4176)
- Extract maxBackoffDurationForPeer func and remove 1 test  (#4218)

### P2p/conn

- Add Bufferpool (#3664)
- Simplify secret connection handshake malleability fix with merlin (#4185)

### Privval

- Remove misplaced debug statement (#4103)
- Add `SignerDialerEndpointRetryWaitInterval` option (#4115)

### Prometheus/metrics

- Three new metrics for consensus (#4263)

### Rpc

- Make max_body_bytes and max_header_bytes configurable (#3818)
- /broadcast_evidence (#3481)
- Return err if page is incorrect (less than 0 or greater than tot… (#3825)
- Protect subscription access from race condition (#3910)
- Allow using a custom http client in rpc client (#3779)
- Remove godoc comments in favor of swagger docs (#4126)
- /block_results fix docs + write test + restructure response (#3615)
- Remove duplication of data in `ResultBlock ` (#3856)
- Add pagination to /validators (#3993)
- Update swagger docs to openapi 3.0 (#4223)
- Added proposer in consensus_state (#4250)
- Pass `outCapacity` to `eventBus#Subscribe` when subscribing using a l… (#4279)
- Add method block_by_hash (#4257)
- Modify New* functions to return error (#4274)
- Check nil blockmeta (#4320)
- PR#4320 follow up (#4323)

### Rpc/client

- Add basic authentication (#4291)

### Rpc/lib

- Fix RPC client, which was previously resolving https protocol to http (#4131)

### Rpc/swagger

- Add numtxs to blockmeta (#4139)

### Scripts

- Remove install scripts (#4242)

### Spec

- Update spec with tendermint updates (#62)

### Spec/consensus/signing

- Add more details about nil and amnesia (#54)

### State

- Txindex/kv: fsync data to disk immediately after receiving it  (#4104)
- Txindex/kv: return an error if there's one (#4095)

### State/store

- Remove extra `if` statement (#3774)

### Store

- Register block amino, not just crypto (#3894)

### Tm-bench

- Add deprecation warning (#3992)

### Tools.mk

- Use tags instead of revisions where possible
- Install protoc

### Tools/tm-bench

- Remove tm-bench in favor of tm-load-test (#4169)

### Txindexer

- Refactor Tx Search Aggregation (#3851)

### Types

- Move MakeVote / MakeBlock functions (#3819)
- Add test for block commits with votes for the wrong blockID (#3936)
- Prevent temporary power overflows on validator updates  (#4165)
- Change number_txs to num_txs json tag in BlockMeta
- Remove dots from errors in SignedHeader#ValidateBasic
- Change `Commit` to consist of just signatures (#4146)
- Prevent spurious validator power overflow warnings when changing the validator set (#4183)

## [0.32.1] - 2019-07-15

### Documentation

- Update to contributing.md (#3760)
- Add readme image (#3763)
- Remove confusing statement from contributing.md (#3764)
- Quick link fixes throughout docs and repo (#3776)
- Replace priv_validator.json with priv_validator_key.json (#3786)

### Testing

- Add consensus_params to testnet config generation (#3781)

### Abci

- Refactor CheckTx to notify of recheck (#3744)
- Minor cleanups in the socket client (#3758)
- Fix documentation regarding CheckTx type update (#3789)

### Adr

- [43] blockchain riri-org (#3753)

### Behaviour

- Return correct reason in MessageOutOfOrder (#3772)

### Config

- Make possible to set absolute paths for TLS cert and key (#3765)

### Libs

- Remove commented and unneeded code (#3757)
- Minor cleanup (#3794)

### Libs/common

- Remove heap.go (#3780)
- Remove unused functions (#3784)

### Libs/fail

- Clean up `fail.go` (#3785)

### Node

- Allow registration of custom reactors while creating node (#3771)

### P2p

- Dial addrs which came from seed instead of calling ensurePeers (#3762)
- Extract ID validation into a separate func (#3754)

### Tm-monitor

- Update build-docker Makefile target (#3790)
- Add Context to RPC handlers (#3792)

## [0.32.0] - 2019-06-25

### Documentation

- (rpc/broadcast_tx_*) write expectations for a client (#3749)
- Update JS section of abci-cli.md (#3747)

## [0.32.0-dev2] - 2019-06-22

### Abci

- Refactor ABCI CheckTx and DeliverTx signatures (#3735)

### Abci/examples

- Switch from hex to base64 pubkey in kvstore (#3641)

### Cs

- Exit if SwitchToConsensus fails (#3706)

### Node

- Run whole func in goroutine, not just logger.Error fn (#3743)

### Rpc/lib

- Write a test for TLS server (#3703)

### State

- Add more tests for block validation (#3674)

## [0.32.0-dev1] - 2019-06-21

### Documentation

- Missing 'b' in python command (#3728)
- Fix some language issues and deprecated link (#3733)

### Node

- Fix a bug where `nil` is recorded as node's address (#3740)

### P2p

- Refactor Switch#OnStop (#3729)

### Types

- Do not ignore errors returned by PublishWithEvents (#3722)

## [0.32.0-dev0] - 2019-06-12

### Documentation

- Update /block_results RPC docs (#3708)

### Abci

- Refactor tagging events using list of lists (#3643)

### Libs/db

- Fix the BoltDB Batch.Delete
- Fix the BoltDB Get and Iterator

### P2p

- Per channel metrics (#3666) (#3677)
- Remove NewNetAddressStringWithOptionalID (#3711)
- Peerbehaviour follow up (#3653) (#3663)

### Rpc

- Use Wrap instead of Errorf error (#3686)

## [0.31.7] - 2019-06-04

### Documentation

- Update RPC docs for /subscribe & /unsubscribe (#3705)

### Libs/db

- Remove deprecated `LevelDBBackend` const (#3632)

## [0.31.6] - 2019-05-30

### ADR-037

- DeliverBlock (#3420)

### Documentation

- Fix typo in clist readme (#3574)
- Update contributing.md (#3503)
- Fix minor typo (#3681)

### Abci/types

- Update comment (#3612)

### Cli

- Add option to not clear address book with unsafe reset (#3606)

### Crypto

- Proof of Concept for iterative version of SimpleHashFromByteSlices (#2611) (#3530)

### Cs

- Fix nondeterministic tests (#3582)

### Cs/replay

- Check appHash for each block (#3579)
- ExecCommitBlock should not read from state.lastValidators (#3067)

### Libs/common

- Remove deprecated PanicXXX functions (#3595)

### Libs/db

- Bbolt (etcd's fork of bolt) (#3610)
- Close boltDBIterator (#3627)
- Fix boltdb batching
- Conditional compilation (#3628)
- Boltdb: use slice instead of sync.Map (#3633)

### Mempool

- Move interface into mempool package (#3524)
- Remove only valid (Code==0) txs on Update (#3625)

### Node

- Refactor node.NewNode (#3456)

### P2p

- (seed mode) limit the number of attempts to connect to a peer (#3573)
- Session should terminate on nonce wrapping (#3531) (#3609)
- Make persistent prop independent of conn direction (#3593)
- PeerBehaviour implementation (#3539)  (#3552)
- Peer state init too late and pex message too soon (#3634)

### P2p/pex

- Consult seeds in crawlPeersRoutine (#3647)

### Pex

- Dial seeds when address book needs more addresses (#3603)
- Various follow-ups (#3605)

### Privval

- Increase timeout to mitigate non-deterministic test failure (#3580)

### Rpc

- Add support for batched requests/responses (#3534)
- /dial_peers: only mark peers as persistent if flag is on (#3620)

### Types

- CommitVotes struct as last step towards #1648 (#3298)

## [0.31.5] - 2019-04-16

### Adr

- PeerBehaviour updates (#3558)

### Blockchain

- Dismiss request channel delay (#3459)

### Common

- CMap: slight optimization in Keys() and Values(). (#3567)

### Gitignore

- Add .vendor-new (#3566)

### State

- Use last height changed if validator set is empty (#3560)

## [0.31.4] - 2019-04-12

### Documentation

- Fix block.Header.Time description (#3529)
- Abci#Commit: better explain the possible deadlock (#3536)

### Adr

- Peer Behaviour (#3539)

### P2p

- Seed mode refactoring (#3011)
- Do not log err if peer is private (#3474)

### Rpc

- Fix response time grow over time (#3537)

## [0.31.2] - 2019-04-01

### Blockchain

- Comment out logger in test code that causes a race condition (#3500)

### Libs

- Remove useless code in group (#3504)

## [0.31.1] - 2019-03-28

### Documentation

- Fix broken links (#3482) (#3488)
- Fix broken links (#3482) (#3488)

### Blockchain

- Update the maxHeight when a peer is removed (#3350)

### Changelog

- Add summary & fix link & add external contributor (#3490)

### Crypto

- Delete unused code (#3426)

### Mempool

- Fix broadcastTxRoutine leak (#3478)
- Add a safety check, write tests for mempoolIDs (#3487)

### P2p

- Refactor GetSelectionWithBias for addressbook (#3475)

### Rpc

- Client disable compression (#3430)
- Support tls rpc (#3469)

### Rpc/client

- Include NetworkClient interface into Client interface (#3473)

## [0.31.0] - 2019-03-19

### Types

- Refactor PB2TM.ConsensusParams to take BlockTimeIota as an arg (#3442)

## [0.31.0-rc0] - 2019-03-14

### Changelog

- More review fixes/release/v0.31.0 (#3427)

### Cmd

- Make sure to have 'testnet' create the data directory for nonvals (#3409)

### Cs

- Comment out log.Error to avoid TestReactorValidatorSetChanges timing out (#3401)

### Grpcdb

- Close Iterator/ReverseIterator after use (#3424)

### Localnet

- Fix $LOG variable (#3423)

### Types

- Remove check for priority order of existing validators (#3407)

## [0.30.2] - 2019-03-11

### Documentation

- Fix typo (#3373)
- Fix the reverse of meaning in spec (#3387)

### Circleci

- Removed complexity from docs deployment job  (#3396)

### Deps

- Update gogo/protobuf from 1.1.1 to 1.2.1 and golang/protobuf from 1.1.0 to 1.3.0 (#3357)

### Libs/db

- Add cleveldb.Stats() (#3379)
- Close batch (#3397)
- Close batch (#3397)

### P2p

- Do not panic when filter times out (#3384)

### Types

- Followup after validator set changes (#3301)

## [0.31.0-dev0] - 2019-02-28

### Cs

- Update wal comments (#3334)

### P2p

- Fix comment in secret connection (#3348)

### Privval

- Improve Remote Signer implementation (#3351)

## [0.30.1] - 2019-02-20

### Documentation

- Fix rpc Tx() method docs (#3331)

### Consensus

- Flush wal on stop (#3297)

### Cs

- Reset triggered timeout precommit (#3310)
- Sync WAL more frequently (#3300)

### Cs/wal

- Refuse to encode msg that is bigger than maxMsgSizeBytes (#3303)

### P2p

- Check secret conn id matches dialed id (#3321)

### Rpc/net_info

- Change RemoteIP type from net.IP to String (#3309)

### Types

- Validator set update tests (#3284)

## [0.29.2-rc1] - 2019-02-08

### Secp256k1

- Change build tags (#3277)

## [0.29.2-rc0] - 2019-02-08

### Documentation

- Fix links (#3220)

### R4R

- Config TestRoot modification for LCD test (#3177)

### WAL

- Better errors and new fail point (#3246)

### Addrbook_test

- Preallocate memory for bookSizes (#3268)

### Adr

- Style fixes (#3206)

### Cmn

- GetFreePort (#3255)

### Mempool

- Correct args order in the log msg (#3221)

### P2p

- Fix infinite loop in addrbook (#3232)

### P2p/conn

- Don't hold stopMtx while waiting (#3254)

### Pubsub

- Comments
- Fixes after Ethan's review (#3212)

### Types

- Comments on user vs internal events

## [0.29.1-rc0] - 2019-01-24

### Documentation

- Explain how someone can run his/her own ABCI app on localnet (#3195)
- Update pubsub ADR (#3131)
- Fix lite client formatting (#3198)

### P2p

- File descriptor leaks (#3150)

## [0.29.0-rc0] - 2019-01-22

### Mempool

- Enforce maxMsgSize limit in CheckTx (#3168)

## [0.29.0-beta0] - 2019-01-18

### Documentation

- Fix broken link (#3142)
- Fix RPC links (#3141)

### Json2wal

- Increase reader's buffer size (#3147)

## [0.28.0] - 2019-01-16

### Documentation

- Update link for rpc docs (#3129)

### Makefile

- Fix build-docker-localnode target (#3122)

### Privval

- Fixes from review (#3126)

## [0.28.0-beta1] - 2019-01-13

### Documentation

- Fix p2p readme links (#3109)

### Rpc

- Include peer's remote IP in `/net_info` (#3052)

## [0.28.0-dev0] - 2019-01-10

### R4R

- Split immutable and mutable parts of priv_validator.json (#2870)

### Cs

- Prettify logging of ignored votes (#3086)

## [0.27.4] - 2018-12-21

### Documentation

- Add rpc link to docs navbar and re-org sidebar (#3041)

### Circleci

- Update go version (#3051)

### Mempool

- Move tx to back, not front (#3036)
- Move tx to back, not front (#3036)

## [0.27.3] - 2018-12-16

### Crypto

- Revert to mainline Go crypto lib (#3027)

## [0.27.1] - 2018-12-16

### Documentation

- Relative links in docs/spec/readme.md, js-amino lib (#2977)
- Fixes from 'first time' review (#2999)
- Enable full-text search (#3004)
- Add edit on Github links (#3014)
- Update DOCS_README (#3019)
- Networks/docker-compose: small fixes (#3017)

### Circleci

- Add a job to automatically update docs (#3005)

### Mempool

- Add a comment and missing changelog entry (#2996)
- NotifyTxsAvailable if there're txs left, but recheck=false (#2991)

### P2p

- Set MConnection#created during init (#2990)

## [0.27.0-rc0] - 2018-12-05

### Documentation

- Add client#Start/Stop to examples in RPC docs (#2939)

### P2p

- Panic on transport error (#2968)
- Fix peer count mismatch #2332 (#2969)

## [0.27.0-dev1] - 2018-11-29

### Documentation

- Update ecosystem.json: add Rust ABCI (#2945)

### Types

- ValidatorSet.Update preserves Accum (#2941)

## [0.27.0-dev0] - 2018-11-28

### Documentation

- Small improvements (#2933)
- Fix js-abci example (#2935)
- Add client.Start() to RPC WS examples (#2936)

### R4R

- Swap start/end in ReverseIterator (#2913)

### Types

- NewValidatorSet doesn't panic on empty valz list (#2938)

## [0.26.4] - 2018-11-27

### Documentation

- Prepend cp to /usr/local with sudo (#2885)

### Mempool

- Add txs from Update to cache

### Node

- Refactor privValidator ext client code & tests (#2895)

### Rpc

- Fix tx.height range queries (#2899)

### Types

- Emit tags from BeginBlock/EndBlock (#2747)

## [0.26.3] - 2018-11-17

### R4R

- Add timeouts to http servers (#2780)

### P2p

- Log 'Send failed' on Debug (#2857)
- NewMultiplexTransport takes an MConnConfig (#2869)

### P2p/conn

- FlushStop. Use in pex. Closes #2092 (#2802)

## [0.26.2-rc0] - 2018-11-15

### Documentation

- Update config: ref #2800 & #2837

### Optimize

- Using parameters in func (#2845)

### Abci

- LocalClient improvements & bugfixes & pubsub Unsubscribe issues (#2748)

### Arm

- Add install script, fix Makefile (#2824)

## [0.26.1] - 2018-11-12

### P2p

- AddressBook requires addresses to have IDs; Do not close conn immediately after sending pex addrs in seed mode (#2797)
- Re-check after sleeps (#2664)

## [0.26.1-rc1] - 2018-11-07

### Mempool

- Print postCheck error (#2762)

### P2p

- Peer-id -> peer_id (#2771)

## [0.26.1-rc0] - 2018-11-06

### Mempool

- ErrPreCheck and more log info (#2724)

## [0.26.0] - 2018-11-03

### ADR-016

- Add versions to Block and State (#2644)
- Add protocol Version to NodeInfo (#2654)
- Update ABCI Info method for versions (#2662)

### Adr-016

- Update int64->uint64; add version to ConsensusParams (#2667)

### Crypto

- Use stdlib crypto/rand. ref #2099 (#2669)

### P2p

- Restore OriginalAddr (#2668)

### Privval

- Add IPCPV and fix SocketPV (#2568)

### Tm-monitor

- Update health after we added / removed node (#2694)

### Types

- Remove Version from CanonicalXxx (#2666)
- Dont use SimpleHashFromMap for header. closes #1841 (#2670)
- First field in Canonical structs is Type (#2675)

## [0.26.0-dev0] - 2018-10-15

### Documentation

- Consensus params and general merkle (#2524)

### Testing

- Test itr.Value in checkValuePanics (#2580)

### Abci

- Codespace (#2557)

### Adr-029

- Update CheckBlock

### Bit_array

- Simplify subtraction

### Circle

- Save p2p logs as artifacts (#2566)

### Clist

- Speedup Next by removing defers (#2511)

### Config

- Add ValidateBasic (#2485)
- Refactor ValidateBasic (#2503)

### Consensus

- Wait timeout precommit before starting new round (#2493)
- Add ADR for first stage consensus refactor (#2462)
- Wait for proposal or timeout before prevote (#2540)

### Crypto

- Add a way to go from pubkey to route (#2574)

### Crypto/amino

- Address anton's comment on PubkeyAminoRoute (#2592)

### Crypto/merkle

- Remove byter in favor of plain byte slices (#2595)

### Crypto/random

- Use chacha20, add forward secrecy (#2562)

### Distribution

- Lock binary dependencies to specific commits (#2550)

### Ed25519

- Use golang/x/crypto fork (#2558)

### Libs

- Handle SIGHUP explicitly inside autofile (#2480)
- Call Flush()  before rename #2428 (#2439)
- Fix event concurrency flaw (#2519)
- Refactor & document events code (#2576)
- Let prefixIterator implements Iterator correctly (#2581)
- Test deadlock from listener removal inside callback (#2588)

### Lite

- Add synchronization in lite verify (#2396)

### Metrics

- Add additional metrics to p2p and consensus (#2425)

### Node

- Respond always to OS interrupts (#2479)

### P2p

- NodeInfo is an interface; General cleanup (#2556)

### Privval

- Switch to amino encoding in SignBytes (#2459)
- Set deadline in readMsg (#2548)

### Rpc/core

- Ints are strings in responses, closes #1896

### Rpc/libs/doc

- Formatting for godoc, closes #2420

### State

- Require block.Time of the fist block to be genesis time (#2594)

### Tools

- Refactor tm-bench (#2570)

### Types

- Remove pubkey from validator hash (#2512)
- Cap evidence in block validation (#2560)

## [0.25.0] - 2018-09-23

### Documentation

- Improve docs on AppHash (#2363)
- Update link to rpc (#2361)
- Update README (#2393)
- Update secure-p2p doc to match the spec + current implementation
- Add missing changelog entry and comment (#2451)
- Add assets/instructions for local docs build (#2453)

### Security

- Implement PeerTransport

### Adr-021

- Note about tag spacers (#2362)

### Common

- Delete unused functions (#2452)

### Mempool

- Filter new txs if they have insufficient gas (#2385)

### P2p

- Integrate new Transport
- Add RPCAddress to NodeInfoOther.String() (#2442)

### Proxy

- Remove Handshaker from proxy pkg (#2437)

### Rpc

- Transform /status result.node_info.other into map (#2417)
- Add /consensus_params endpoint  (#2415)

### Spec

- Add missing field to NodeInfoOther (#2426)

### Tools/tm-bench

- Bounds check for txSize and improving test cases (#2410)

### Version

- Types

## [0.24.0] - 2018-09-07

### Documentation

- Fix encoding JSON
- Bring blockchain.md up-to-date
- Specify consensus params in state.md
- Fix note about ChainID size
- Remove tags from result for now
- Move app-dev/abci-spec.md to spec/abci/abci.md
- Update spec
- Refactor ABCI docs
- Fixes and more from #2249
- Add abci spec to config.js

### Types/time

- Add note about stripping monotonic part

### Version

- Add and bump abci version

## [0.24.0-rc0] - 2018-09-05

### Documentation

- Fix indentation for genesis.validators
- Remove json tags, dont use HexBytes
- Update vote, signature, time

### Tmtime

- Canonical, some comments (#2312)

## [0.23.1] - 2018-08-31

### Documentation

- Fix links & other imrpvoements
- Note max outbound peers excludes persistent
- Fix img links, closes #2214 (#2282)
- Deprecate RTD (#2280)

### Abci

- Add next_validators_hash to header
- VoteInfo, ValidatorUpdate. See ADR-018
- Move round back from votes to commit

### Blockchain

- Fix register concrete name. (#2213)

### Clist

- Speedup functions (#2208)

### Cmap

- Remove defers (#2210)

### Config

- Reduce default mempool size (#2300)

### Crypto

- Add compact bit array for intended usage in the multisig
- Threshold multisig implementation
- Add compact bit array for intended usage in the multisig (#2140)
- Remove unnecessary prefixes from amino route variable names (#2205)

### Crypto/secp256k1

- Fix signature malleability, adopt more efficient en… (#2239)

### Libs

- Remove usage of custom Fmt, in favor of fmt.Sprintf (#2199)

### Libs/autofile

- Bring back loops (#2261)

### Make

- Update protoc_abci use of awk

### Makefile

- Lint flags

### Mempool

- Keep cache hashmap and linked list in sync (#2188)
- Store txs by hash inside of cache (#2234)

### Types

- Allow genesis file to have 0 validators (#2148)

## [0.23.0] - 2018-08-05

### ADR

- Fix malleability problems in Secp256k1 signatures

### Documentation

- Modify blockchain spec to reflect validator set changes (#2107)

### Abci

- Change validators to last_commit_info in RequestBeginBlock (#2074)
- Update readme for building protoc (#2124)

### Adr

- Encoding for cryptography at launch (#2121)
- Protocol versioning
- Chain-versions

### Adr-018

- Abci validators

### Ci

- Reduce log output in test_cover (#2105)

### Consensus

- Fix test for blocks with evidence
- Failing test for ProposerAddress

### Crypto

- Add compact bit array for intended usage in the multisig
- Remove interface from crypto.Signature

### Libs

- Make bitarray functions lock parameters that aren't the caller (#2081)

### Libs/autofile/group_test

- Remove unnecessary logging (#2100)

### Libs/cmn

- Remove Tempfile, Tempdir, switch to ioutil variants (#2114)

### Libs/cmn/writefileatomic

- Handle file already exists gracefully (#2113)

### Libs/common

- Refactor tempfile code into its own file

### P2p

- Connect to peers from a seed node immediately (#2115)
- Add test vectors for deriving secrets (#2120)

### P2p/pex

- Allow configured seed nodes to not be resolvable over DNS (#2129)
- Fix mismatch between dialseeds and checkseeds. (#2151)

### Types

- Fix formatting when printing signatures

## [0.22.8-rc0] - 2018-07-27

### Adr

- PeerTransport (#2069)

### Libs

- Update BitArray go docs (#2079)

## [0.22.7] - 2018-07-26

### .github

- Split the issue template into two seperate templates (#2073)

### Security

- Remove RipeMd160.

### Crypto

- Add benchmarking code for signature schemes (#2061)
- Switch hkdfchacha back to xchacha (#2058)

### Makefile

- Add `make check_dep` and remove `make ensure_deps` (#2055)

### P2p/secret_connection

- Switch salsa usage to hkdf + chacha

### Rpc

- Improve slate for Jenkins (#2070)

## [0.22.6] - 2018-07-25

### Github

- Update PR template to indicate changing pending changelog. (#2059)

### Rpc

- Log error when we timeout getting validators from consensus (#2045)

## [0.22.6-rc0] - 2018-07-25

### Abci

- Remove fee (#2043)

### Consensus

- Include evidence in proposed block parts. fixes #2050

### Dep

- Revert updates

### P2p

- Reject addrs coming from private peers (#2032)
- Fix conn leak. part of #2046

### Rpc

- Fix /blockchain OOM #2049
- Validate height in abci_query

### Tools

- Clean up Makefile and remove LICENSE file (#2042)

## [0.22.5] - 2018-07-24

### WIP

- More empty struct examples

### Common/rand

- Remove exponential distribution functions (#1979)

### Config

- 10x default send/recv rate (#1978)

### Crypto

- Refactor to move files out of the top level directory
- Remove Ed25519 and Secp256k1 suffix on GenPrivKey
- Fix package imports from the refactor

### Crypto/ed25519

- Update the godocs (#2002)
- Remove privkey.Generate method (#2022)

### Crypto/secp256k1

- Add godocs, remove indirection in privkeys (#2017)

### Libs/common/rand

- Update godocs

### Makefile

- Fix protoc_libs

### Mempool

- Chan bool -> chan struct{}

### Rpc

- Test Validator retrevial timeout

### Tools

- Remove redundant grep -v vendors/ (#1996)

## [0.22.4-rc0] - 2018-07-14

### Consensus

- Wait on stop if not fastsync

### Tools/tm-bench

- Don't count the first block if its empty
- Remove testing flags from help (#1949)
- Don't count the first block if its empty (#1948)

### Tools/tmbench

- Fix the end time being used for statistics calculation
- Improve accuracy with large tx sizes.
- Move statistics to a seperate file

## [0.22.3] - 2018-07-10

### Dep

- Pin all deps to version or commit

## [0.22.1] - 2018-07-10

### Documentation

- Md fixes & latest tm-bench/monitor

### Rpc/lib/server

- Add test for int parsing

### State

- Err if 0 power validator is added to the validator set
- Format panics

## [0.22.0-autodraft] - 2018-07-04

### Documentation

- Remove node* files
- Update getting started and remove old script (now in scripts/install)

## [0.22.0-rc2] - 2018-07-02

### P2p

- External address

### Tmbench

- Make it more resilient to WSConn breaking (#111)

## [0.22.0-rc1] - 2018-07-01

### Consensus

- Stop wal

## [0.22.0-rc0] - 2018-07-01

### Documentation

- Minor fix for abci query peer filter
- Update address spec to sha2 for ed25519

### Config

- Rename skip_upnp to upnp (#1827)

### Crypto

- Abstract pubkey / signature size when known to constants (#1808)

### Tmbench

- Make sendloop act in one second segments (#110)

## [0.21.1-rc1] - 2018-06-27

### Documentation

- Update abci links (#1796)
- Update js-abci example

### Abci

- Remove old repo docs
- Remove nested .gitignore
- Remove LICENSE
- Add comment for doc update

### Adr

- Update readme

### Adr-009

- No pubkeys in beginblock
- Add references

### Ci

- Setup abci in dependency step
- Move over abci-cli tests

### Consensus

- Fix addProposalBlockPart

### Crypto

- Rename last traces of go-crypto (#1786)

### Crypto/hkdfchachapoly

- Add testing seal to the test vector

### Mempool

- Log hashes, not whole tx

### P2p/trust

- Fix nil pointer error on TrustMetric Copy() (#1819)

### Rpc

- Break up long lines

### Tm-bench

- Improve code shape
- Update dependencies, add total metrics

### Tmbench

- Fix iterating through the blocks, update readme
- Make tx size configurable
- Update dependencies to use tendermint's master

## [0.20.1-rc0] - 2018-06-19

### Documentation

- Some organizational cleanup
- DuplicateVoteEvidence

### Consensus

- Fixes #1754

### Mempool

- Fix cache_size==0. closes #1761

## [0.21.0-rc0] - 2018-06-15

### Bech32

- Wrap error messages

### Docs

- Update description of seeds and persistent peers

### Documentation

- Start move back to md
- Cleanup/clarify build process
- Pretty fixes

### All

- Gofmt (#1743)

## [0.20.0] - 2018-06-07

### Documentation

- Add BSD install script

### ResponseEndBlock

- Ensure Address matches PubKey if provided

## [0.19.9-rc0] - 2018-06-06

### Evidence

- Dont send evidence to unsynced peers
- Check peerstate exists; dont send old evidence
- Give each peer a go-routine

### State

- S -> state
- B -> block

## [0.19.8] - 2018-06-04

### Documentation

- A link to quick install script

### Scripts

- Quickest/easiest fresh install

## [0.19.7-rc0] - 2018-05-31

### Merkle

- Remove unused funcs. unexport simplemap. improv docs
- Use amino for byteslice encoding

## [0.19.6-rc2] - 2018-05-29

### Documentation

- Fix dead links, closes #1608
- Use absolute links (#1617)
- Update ABCI output (#1635)

### Circle

- Add GOCACHE=off and -v to tests

### Consensus

- Link to spec from readme (#1609)

### Tmhash

- Add Sum function

## [0.19.5-rc1] - 2018-05-20

### Consensus

- Only fsync wal after internal msgs

## [0.19.5-rc0] - 2018-05-20

### Documentation

- Lil fixes
- Update install instructions, closes #1580
- Blockchain and consensus dirs

### Testing

- Less bash
- More smoothness

### Networks

- Update readmes

## [0.19.4-rc0] - 2018-05-17

### Documentation

- Add diagram, closes #1565 (#1577)

### Circle

- Fix config.yml

### P2p

- Prevent connections from same ip

### Spec

- Move to final location (#1576)

## [0.19.3] - 2018-05-15

### Absent_validators

- Repeated int -> repeated bytes

## [0.19.3-rc0] - 2018-05-14

### Grpcdb

- Better readability for docs and constructor names

### Remotedb

- A client package implementing the db.DB interface

### Rpc

- Add voting power totals to vote bitarrays
- /consensus_state for simplified output

## [0.19.2-rc0] - 2018-04-30

### Node

- Remove dup code from rebase
- Remove commented out trustMetric

### P2p

- Dont require minor versions to match in handshake
- Explicit netaddress errors
- Some comments and a log line
- MinNumOutboundPeers. Closes #1501
- Change some logs from Error to Debug. #1476
- Small lint

### P2p/pex

- Minor cleanup and comments
- Some addrbook fixes

### Rpc

- Add n_peers to /net_info

### Spec

- Pex update
- Abci notes. closes #1257

## [0.19.1] - 2018-04-28

### P2p

- NodeInfo.Channels is HexBytes

### Rpc

- Lower_case peer_round_states, use a list, add the node_address
- Docs/comments

### Spec

- Update encoding.md
- Note on byte arrays, clean up bitarrays and more, add merkle proof, add crypto.go script
- Add Address spec. notes about Query

## [0.19.0-rc3] - 2018-04-09

### P2p

- Switch - reconnect only if persistent
- Don't use dial funcn in peerconfig

### Types

- Lock block on MakePartSet

## [0.18.0-autodraft] - 2018-04-06

### Documentation

- Build updates

### ValidatorSet#GetByAddress

- Return -1 if no validator was found

### WIP

- Fix rpc/core

### Consensus

- Close pubsub channels. fixes #1372

### Https

- //github.com/tendermint/tendermint/pull/1128#discussion_r162799294

### P2p

- Persistent - redial if first dial fails

## [0.17.1] - 2018-03-27

### Types

- Fix genesis.AppStateJSON

## [0.17.0] - 2018-03-27

### Documentation

- Add document 'On Determinism'
- Wrong command-line flag
- The character for 1/3 fraction could not be rendered in PDF on readthedocs. (#1326)
- Update quick start guide (#1351)

### Adr

- Amend decisions for PrivValidator

### Cmd/tendermint/commands/lite

- Add tcp scheme to address URLs (#1297)

### Common

- NewBitArray never crashes on negatives (#170)
- Remove {Left, Right}PadString (#168)
- NewBitArray never crashes on negatives (#170)

### Config

- Fix private_peer_ids

### Consensus

- Return from go-routine in test
- Return from errors sooner in addVote

### Lite/proxy

- Validation* tests and hardening for nil dereferences
- Consolidate some common test headers into a variable

### P2p

- Introduce peerConn to simplify peer creation (#1226)
- Keep reference to connections in test peer

### PrivVal

- Improve SocketClient network code (#1315)

### State

- Builds
- Fix txResult issue with UnmarshalBinary into ptr

### Types

- Update for new go-wire. WriteSignBytes -> SignBytes
- Remove dep on p2p
- Tests build
- Builds
- Revert to old wire. builds
- Working on tests...
- P2pID -> P2PID
- Fix validator_set_test issue with UnmarshalBinary into ptr
- Bring back json.Marshal/Unmarshal for genesis/priv_val
- TestValidatorSetVerifyCommit
- Uncomment some tests
- Hash invoked for nil Data and Header should not panic
- Compile time assert to, and document sort.Interface
- Revert CheckTx/DeliverTx changes. make them the same

### Types/validator_set_test

- Move funcs around

### Wire

- No codec yet

## [0.16.0] - 2018-02-21

### Documentation

- Update ecosystem.rst (#1037)
- Add abci spec
- Add counter/dummy code snippets
- Updates from review (#1076)
- Tx formats: closes #1083, #536
- Fix tx formats [ci skip]
- Update getting started [ci skip]

### Testing

- Use shasum to avoid rarer dependency

### Blockchain

- Test wip for hard to test functionality [ci skip]

### Cli

- WriteDemoConfig -> WriteConfigVals

### Cmn

- Fix HexBytes.MarshalJSON

### Common

- Fix BitArray.Update to avoid nil dereference
- BitArray: feedback from @adrianbrink to simplify tests
- IsHex should be able to handle 0X prefixed strings

### Common/BitArray

- Reduce fragility with methods

### Config

- Fix addrbook path to go in config

### Consensus

- Rename test funcs
- Minor cosmetic
- Fix SetLogger in tests
- Print go routines in failed test

### Example/dummy

- Remove iavl dep - just use raw db

### Hd

- Comments and some cleanup

### Keys/keybase.go

- Comments and fixes

### Lite

- MemStoreProvider GetHeightBinarySearch method + fix ValKeys.signHeaders
- < len(v) in for loop check, as per @melekes' recommendation
- TestCacheGetsBestHeight with GetByHeight and GetByHeightBinarySearch
- Comment out iavl code - TODO #1183

### Mempool

- Cfg.CacheSize and expose InitWAL

### Merkle

- Remove go-wire dep by copying EncodeByteSlice

### Nano

- Update comments

### P2p

- PrivKey need not be Ed25519
- Reorder some checks in addPeer; add comments to NodeInfo
- Peer.Key -> peer.ID
- Add ID to NetAddress and use for AddrBook
- Support addr format ID@IP:PORT
- Authenticate peer ID
- Remove deprecated Dockerfile
- Seed mode fixes from rebase and review
- Seed disconnects after sending addrs
- Add back lost func
- Use sub dirs
- Tmconn->conn and types->p2p
- Use conn.Close when peer is nil
- Notes about ListenAddr
- AddrBook.Save() on DialPeersAsync
- Add Channels to NodeInfo and don't send for unknown channels
- Fix tests for required channels
- Fix break in double loop

### P2p/conn

- Better handling for some stop conditions

### P2p/pex

- Wait to connect to all peers in reactor test

### P2p/trustmetric

- Non-deterministic test

### Priv-val

- Fix timestamp for signing things that only differ by timestamp

### Spec

- Convert to rst
- Typos & other fixes
- Remove notes, see #1152
- More fixes
- Minor fixes

### Types

- Check bufio.Reader
- TxEventBuffer.Flush now uses capacity preserving slice clearing idiom
- RequestInitChain.AppStateBytes

### Types/priv_validator

- Fixes for latest p2p and cmn

### Wip

- Priv val via sockets
- Comment types
- Fix code block in ADR
- Fix nil pointer deference
- Avoid underscore in var name
- Check error of wire read

## [0.15.0] - 2017-12-29

### CRandHex

- Fix up doc to mention length of digits

### Proposal

- New Makefile standard template (#168)

### README

- Document the minimum Go version

### Testing

- Longer timeout
- Add some timeouts

### All

- Fix vet issues with build tags, formatting

### Batch

- Progress

### Blockchain

- Test fixes
- Update for new state

### Cmd/abci-cli

- Use a single connection per session
- Implement batch

### Cmd/tendermint

- Fix initialization file creation checks (#991)

### Cmn

- Fix race condition in prng
- Fix repeate timer test with manual ticker
- Fix race

### Common

- No more relying on math/rand.DefaultSource
- Use names prng and mrand
- Use genius simplification of tests from @ebuchman
- Rand* warnings about cryptographic unsafety

### Config

- Write all default options to config file
- Lil fixes
- Unexpose chainID

### Consensus

- Fix makeBlockchainFromWAL
- Remove log stmt. closes #987
- Note about duplicate evidence

### Db

- Some comments in types.go
- Test panic on nil key
- Some test cleanup
- Fixes to fsdb and clevledb
- Memdb iterator
- Goleveldb iterator
- Cleveldb iterator
- Fsdb iterator
- Fix c and go iterators
- Simplify exists check, fix IsKeyInDomain signature, Iterator Close

### Evidence

- More funcs in store.go
- Store tests and fixes
- Pool test
- Reactor test
- Reactor test

### Mempool

- Remove Peer interface. use p2p.Peer

### P2p/trust

- Remove extra channels

### Protoc

- "//nolint: gas" directive after pb generation (#164)

### Rpc

- GetHeight helper function
- Fix getHeight

### Spec

- Fixes from review

### State

- TestValidateBlock
- Move methods to funcs
- BlockExecutor
- Re-order funcs. fix tests
- Send byzantine validators in BeginBlock

### Types

- Compile type assertions to avoid sneaky runtime surprises
- Check ResponseCheckTx too
- Update String() test to assert Prevote type
- Rename exampleVote to examplePrecommit on vote_test
- Add test for IsVoteTypeValid
- Params.Update()
- Comments; compiles; evidence test
- Evidences for merkle hashing; Evidence.String()
- Tx.go comments
- Evidence cleanup
- Better error messages for votes

### Types/params

- Introduce EvidenceParams

### Wip

- Tendermint specification

## [0.14.0] - 2017-12-12

### Adr

- Update 007 trust metric usage

### Appveyor

- Use make

### Blockchain

- Add tests and more docs for BlockStore
- Update store comments
- Updated store docs/comments from review
- Deduplicate store header value tests
- Less fragile and involved tests for blockstore
- Block creator helper for compressing tests as per @ebuchman
- Note about store tests needing simplification ...

### Consensus

- Fix typo on ticker.go documentation

### Linter

- Enable in CI & make deterministic

### P2p

- Exponential backoff on reconnect. closes #939

## [0.13.0] - 2017-12-06

### Documentation

- Add note about putting GOPATH/bin on PATH
- Correction, closes #910

### Testing

- Sunset tmlibs/process.Process
- Wait for node heights before checking app hash
- Fix ensureABCIIsUp
- Fix test/app/counter_test.sh

### Abci-cli

- Print OK if code is 0
- Prefix flag variables with flag

### Client

- Use vars for retry intervals

### Common

- Comments for Service

### Dummy

- Include app.key tag

### Glide

- Update grpc version

### Mempool

- Implement Mempool.CloseWAL
- Return error on cached txs
- Assert -> require in test

### P2p/trust

- Split into multiple files and improve function order
- Lock on Copy()

### Rpc

- Make time human readable. closes #926

### Shame

- Forgot to add new code pkg

### Types

- Use data.Bytes directly in type.proto via gogo/protobuf. wow
- Consolidate some file
- Add note about ReadMessage having no cap
- RequestBeginBlock includes absent and byzantine validators
- Drop uint64 from protobuf.go
- IsOK()
- Int32 with gogo int
- Fix for broken customtype int in gogo
- Add MarshalJSON funcs for Response types with a Code
- Add UnmarshalJSON funcs for Response types

## [0.12.1] - 2017-11-28

### Documentation

- Fix links, closes #860

### PubKeyFromBytes

- Return zero value PubKey on error

### Security

- Use bytes.Equal for key comparison

### WIP

- Begin parallel refactoring with go-wire Write methods and MConnection

### Blockchain

- Add comment in AddPeer. closes #666

### Certifiers

- Test uses WaitForHeight

### Clist

- Reduce numTimes in test

### Consensus

- Ensure prs.ProposalBlockParts is initialized. fixes #810
- Fix for initializing block parts during catchup
- Make mempool_test deterministic
- Fix LastCommit log
- Crank timeout in timeoutWaitGroup

### Consensus/WAL

- Benchmark WALDecode across data sizes

### Db

- Sort keys for memdb iterator

### Errcheck

- PR comment fixes

### Lint

- Apply deadcode/unused

### Linter

- Address deadcode, implement incremental lint testing
- Sort through each kind and address small fixes

### Linting

- Replace megacheck with metalinter
- Apply 'gofmt -s -w' throughout
- Apply misspell
- Apply errcheck part1
- Apply errcheck part2
- Moar fixes
- Few more fixes

### Node

- Clean makeNodeInfo

### P2p

- Update readme, some minor things
- Some fixes re @odeke-em issues #813,#816,#817
- Comment on the wg.Add before go saveRoutine()
- Peer should respect errors from SetDeadline
- Use fake net.Pipe since only >=Go1.10 implements SetDeadline
- NetPipe for <Go1.10 in own file with own build tag
- Fix non-routable addr in test
- Fix comment on addPeer (thanks @odeke-em)
- Make Switch.DialSeeds use a new PRNG per call
- Disable trustmetric test while being fixed

### P2p/addrbook

- Comments
- AddrNew/Old -> bucketsNew/Old
- Simplify PickAddress
- AddAddress returns error. more defensive PickAddress
- Add non-terminating test
- Fix addToOldBucket
- Some comments

### P2p/connetion

- Remove panics, test error cases

### P2p/pex

- Simplify ensurePeers

### Rpc

- Wait for rpc servers to be available in tests
- Fix tests

### Rpc/lib/server

- Add handlers tests
- Update with @melekes and @ebuchman feedback
- Separate out Notifications test
- Minor changes to test

### Rpc/lib/types

- RPCResponse.Result is not a pointer

### Rpc/wsevents

- Small cleanup

### Server

- Minor refactor

### State

- Return to-be-used function

### Types

- Add gas and fee fields to CheckTx

### WsConnection

- Call onDisconnect

## [0.12.0] - 2017-10-28

### Documentation

- Add py-tendermint to abci-servers
- Remove mention of type byte
- Add info about tm-migrate
- Update abci example details [ci skip]
- Typo
- Smaller logo (200px)
- Comb through step by step
- Fixup abci guide

### GroupReader#Read

- Return io.EOF if file is empty

### Makefile

- Fix linter

### Testing

- Add simple client/server test with no addr prefix
- Update for abci-cli consolidation. shell formatting

### Blockchain/pool

- Some comments and small changes

### Blockchain/store

- Comment about panics

### Cli

- Clean up error handling
- Use cobra's new ExactArgs() feature

### Cmn

- Kill

### Consensus

- Kill process on app error

### Console

- Fix output, closes #93
- Fix tests

### Dummy

- Verify pubkey is go-crypto encoded in DeliverTx. closes #51

### Glide

- More external deps locked to versions

### Keys

- Transactions.go -> types.go

### Linting

- A few fixes

### Rpc

- Use /iavl repo in test (#713)

### Rpc/client

- Use compile time assertions instead of methods

### Rpc/lib/client

- Add jitter for exponential backoff of WSClient
- Jitter test updates and only to-be run on releases

### Server

- Use cmn.ProtocolAndAddress

### SocketClient

- Fix and test for StopForError deadlock

### Types

- ConsensusParams test + document the ranges/limits
- ConsensusParams: add feedback from @ebuchman and @melekes
- Unexpose valset.To/FromBytes

## [0.11.1] - 2017-10-10

### Documentation

- Add ABCI implementations
- Added passchain to the ecosystem.rst in the applications section;
- Fix build warnings
- Add stratumn

### [docs

- Typo fix] remove misplaced "the"
- Typo fix] add missing "have"

### All

- No more anonymous imports

### Autofile

- Ensure file is open in Sync

### Blockchain

- Fixing reactor tests

### Blockchain/reactor

- RespondWithNoResponseMessage for missing height

### Changelog

- Add genesis amount->power

### Db

- Fix MemDB.Close

### Example

- Fix func suffix

### Glide

- Update for autofile fix

### Linter

- Couple fixes
- Add metalinter to Makefile & apply some fixes
- Last fixes & add to circle

### Linting

- Fixup some stuffs
- Little more fixes

### Makefile

- Remove megacheck

### Rpc

- Fix client websocket timeout (#687)
- Subscribe on reconnection (#689)

### Rpc/lib

- Remove dead files, closes #710

### Types/heartbeat

- Test all Heartbeat functions

### Upnp

- Keep a link

## [0.11.0] - 2017-09-22

### Documentation

- Give index a Tools section
- Update and clean up adr
- Use README.rst to be pulled from tendermint
- Re-add the images
- Add original README's from tools repo
- Convert from md to rst
- Update index.rst
- Move images in from tools repo
- Harmonize headers for tools docs
- Add kubes docs to mintnet doc, from tools
- Add original tm-bench/monitor files
- Organize tm-bench/monitor description
- Pull from tools on build
- Finish pull from tools
- Organize the directory, #656
- Add software.json from website (ecosystem)
- Rename file
- Add and re-format the ecosystem from website
- Pull from tools' master branch
- Using ABCI-CLI
- Remove last section from ecosystem
- Organize install a bit better

### Makefile

- Remove redundant lint

### Adr

- Add 005 consensus params

### Circle

- Add metalinter to test

### Cmd

- Dont wait for genesis. closes #562

### Common

- Fingerprint comment
- WriteFileAtomic use tempfile in current dir

### Consensus

- Remove support for replay by #HEIGHT. closes #567
- Use filepath for windows compatibility, closes #595

### Lint

- Couple more fixes

### Linting

- Cover the basics
- Catch some errors
- Add to Makefile & do some fixes
- Next round  of fixes

### Metalinter

- Add linter to Makefile like tendermint

### Node

- NewNode takes DBProvider and GenDocProvider

### P2p

- Fully test PeerSet, more docs, parallelize PeerSet tests
- Minor comment fixes
- Delete unused and untested *IPRangeCount functions
- Sw.AddPeer -> sw.addPeer
- Allow listener with no external connection

### Readme

- Re-organize & update docs links

### State

- Minor comment fixes

### Types

- Remove redundant version file
- PrivVal.Sign returns an error
- More . -> cmn
- Comments

## [0.10.4] - 2017-09-05

### Documentation

- Add conf.py
- Test
- Add sphinx Makefile & requirements
- Consolidate ADRs
- Convert markdown to rst
- Organize the specification
- Rpc docs to be slate, see #526, #629
- Use maxdepth 2 for spec
- Port website's intro for rtd
- Rst-ify the intro
- Fix image links
- Link fixes
- Clean a bunch of stuff up
- Logo, add readme, fixes
- Pretty-fy

### Cmd

- Don't load config for version command. closes #620

### Cmd/tendermint/commands

- Update ParseConfig doc

### Consensus

- Recover panics in receive routine

### Db

- Fix memdb iterator

### Mempool

- Reactor test

### P2p

- Put maxMsgPacketPayloadSize, recvRate, sendRate in config
- Test fix

### Readme

- Update install instruction (#100)

### Rpc

- Typo fixes
- Comments
- Historical validators
- Block and Commit take pointers; return latest on nil

### Rtd

- Build fixes

### State

- Comments; use wire.BinaryBytes
- Persist validators

## [0.10.3] - 2017-08-10

### Documentation

- Tons of minor improvements

### Fix

- Ansible playbook to deploy tendermint

### README

- Add godoc instead of tedious MD regeneration

### Cmd

- --consensus.no_empty_blocks

### Common

- ProtocolAndAddress

### Common/IsDirEmpty

- Do not mask non-existance errors

### Consensus

- More comments
- IsProposer func
- Remove rs from handleMsg
- Log ProposalHeartbeat msg
- Test proposal heartbeat

### Hd

- Optimize ReverseBytes + add tests

### Http

- Http-utils added after extraction

### Mempool

- Comments

### P2p

- Sw.peers.List() is empty in sw.OnStart

### Rpc

- Move grpc_test from test/ to grpc/

### Scripts/txs

- Add 0x and randomness

### Types

- Block comments

### Ws

- Small comment

## [0.10.2] - 2017-07-10

### Documentation

- Add docs from website

### Consensus

- Improve logging for conflicting votes
- Better logging

### Contributing

- Use full version from site

### P2p

- Fix test

## [0.10.1] - 2017-06-28

### Documentation

- Update for 0.10.0 [ci skip]"

### Ansible

- Update tendermint and basecoin versions
- Added option to provide accounts for genesis generation, terraform: added option to secure DigitalOcean servers, devops: added DNS name creation to tendermint terraform

### Blockchain

- Explain isCaughtUp logic

### Rpc

- SetWriteDeadline for ws ping. fixes #553

### Rpc/lib

- Test tcp and unix
- Set logger on ws conn

## [0.10.0] - 2017-06-03

### Makefile

- Add megacheck & some additional fixes

### Core

- Apply megacheck vet tool (unused, gosimple, staticcheck)

### Dist

- Dont mkdir in container
- Dont mkdir in container

## [0.10.0-rc1] - 2017-05-18

### BROKEN

- Attempt to replace go-wire.JSON with json.Unmarshall in rpc

### CHANGELOG

- Update release date
- Update release date

### Testing

- Jq .result[1] -> jq .result
- P2p.seeds and p2p.pex

### Cli

- Support --root and --home
- More descriptive naming
- Viper.Set(HomeFlag, rootDir)

### Cmd

- Fixes for new config
- Query params are flags

### Config

- Pex_reactor -> pex

### Consensus

- Comment about test_data [ci skip]
- Fix tests

### Ebuchman

- Added some demos on how to parse unknown types

### Log

- Tm -> TM

### Node

- ConfigFromViper

### P2p

- Use cmn instead of .
- Fix race by peer.Start() before peers.Add()

### Rpc

- Repsonse types use data.Bytes
- Response types use Result instead of pb Response
- Fix tests
- Decode args without wire
- Cleanup some comments [ci skip]
- Fix tests

### Rpc/lib

- No Result wrapper

### Types

- []byte -> data.Bytes
- Result and Validator use data.Bytes
- Methods convert pb types to use data.Bytes

## [0.9.2] - 2017-04-26

### Documentation

- Go-events -> tmlibs/events

### Testing

- Test_libs all use Makefile

### Commands

- Run -> RunE

### Merkle

- Go-common -> tmlibs

### Premerge2

- Rpc -> rpc/tendermint

### Rpc

- Use HTTP error codes

## [0.9.1] - 2017-04-21

### Testing

- Check err on cmd.Wait

### Blockpool

- Fix removePeer bug

### Changelog

- Add prehistory

### Cli

- Testnet cmd inits files for testnet
- ResetAll doesnt depend on cobra

### Consensus

- Timeout on replayLastBlock

### Consensus/replay

- Remove timeout

### Consensus/wal

- #HEIGHT -> #ENDHEIGHT

### Readme

- Js-tmsp -> js-abci

### Rpc

- Dial_seeds msg. addresses #403
- Better arg validation for /tx
- /tx allows height+hash

### Rpc/core/types

- UintX -> int

### Rpc/test

- /tx
- Restore txindexer after setting null

### Secp256k1

- Use compressed pubkey, bitcoin-style address

### State

- ABCIResponses, s.Save() in ApplyBlock

### Wal

- Gr.Close()

## [0.9.0] - 2017-03-06

### Client

- DumpConsensusState, not DialSeeds. Cleanup

### Makefile

- Add gmt and lint
- Add 'build' target

### Query

- Height -> LastHeight
- LastHeight -> Height :)

### Testing

- Unexport internal function.
- Update docker to 1.7.4
- Dont use log files on circle
- Shellcheck
- Forward CIRCLECI var through docker
- Only use syslog on circle
- More logging
- Wait for tendermint proc
- Add extra kill after fail index triggered
- Wait for ports to be freed
- Use --pex on restart
- Install abci apps first
- Fix docker and apps
- Dial_seeds
- Docker exec doesnt work on circle
- Bump sleep to 5 for bound ports release
- Better client naming
- Use unix socket for rpc
- Shellcheck

### Update

- JTMSP -> jABCI

### Cleanup

- Replace common.Exit with log.Crit or log.Fatal

### Consensus

- Nice error msg if ApplyBlock fails
- Handshake replay test using wal
- More handshake replay tests
- Some more informative logging

### Fmt

- Run 'make fmt'

### Glide

- Use versions where applicable

### Lint

- Remove dot import (go-common)
- S/common.Fmt/fmt.Sprintf
- S/+=1/++, remove else clauses

### Make

- Dont use -v on go test

### Repeat_timer

- Drain channel in Stop; done -> wg

### Rpc

- /commit
- Fix SeenCommit condition

### State

- Remove StateIntermediate

### Types

- Use mtx on PartSet.String()
- ValSet LastProposer->Proposer and Proposer()->GetProposer()

## [0.8.0] - 2017-01-13

### Connect2Switches

- Panic on err

### Testing

- RandConsensusNet takes more args
- Crank circle timeouts
- Automate building consensus/test_data
- Circle artifacts
- Dont start cs until all peers connected
- Shorten timeouts
- Remove codecov patch threshold
- Kill and restart all nodes
- Use PROXY_APP=persistent_dummy
- Use fail-test failure indices
- More unique container names
- Set log_level=info
- Always rebuild grpc_client
- Split up test/net/test.sh

### Blockchain

- Thread safe store.Height()

### Consensus

- Wal.Flush() and cleanup replay tests
- TimeoutTicker, skip TimeoutCommit on HasAll
- Mv timeoutRoutine into TimeoutTicker
- No internal vars in reactor.String()
- Sync wal.writeHeight
- Remove crankTimeoutPropose from tests
- Be more explicit when we need to write height after handshake
- Let time.Timer handle non-positive durations
- Check HasAll when TwoThirdsMajority

### Glide

- Update go-wire

### Shame

- Version bump 0.7.4

### State

- AppHashIsStale -> IntermediateState

### Tmsp

- ResponseInfo and ResponseEndBlock

### Types

- Benchmark WriteSignBytes
- Canonical_json.go
- SignatureEd25519 -> Signature

## [0.7.4] - 2016-12-14

### Testing

- App persistence
- Tmsp query result is json
- Increase proposal timeout
- Cleanup and fix scripts
- Crank it to eleventy
- More cleanup on p2p

### Addrbook

- Toggle strict routability

### Blockchain

- Use ApplyBlock

### Consensus

- Test reactor
- Fix panic on POLRound=-1
- Ensure dir for cswal on reactor tests
- Lock before loading commit
- Track index of privVal
- Test validator set change

### Counter

- Fix tx buffer overflow

### Cswal

- Write #HEIGHT:1 for empty wal

### Dummy

- Valset changes and tests

### Glide

- Update go-common

### Rpc

- Remove restriction on DialSeeds

### Shame

- Forgot a file

### State

- ApplyBlock

### Types

- Pretty print validators
- Update LastBlockInfo and ConfigInfo
- Copy vote set bit array
- Copy commit bit array

## [0.7.3] - 2016-10-21

### Testing

- Codecov
- Use glide with mintnet/netmon
- Install glide for network test

### Consensus

- Hvs.StringIndented needed a lock. addresses #284

### Log

- Move some Info to Debug

### Replay

- Larger read buffer
- More tests
- Ensure cs.height and wal.height match

### Rpc

- Use interfaces for pipe

### Service

- Reset() for restarts

### Version

- Bump 0.7.3

## [0.7.1] - 2016-09-11

### Testing

- Refactor bash; test fastsync (failing)
- Name client conts so we dont need to rm them because circle
- Test dummy using rpc query
- Add xxd dep to dockerfile
- More verbosity
- Add killall to dockerfile. cleanup

### Client

- Safe error handling

### Config

- All urls use tcp:// or unix:// prefix
- Filter_peers defaults to false
- Reduce timeouts during test

### Consensus

- Add note about replay test
- No sign err in replay; fix a race

### Proxy

- Typed app conns
- NewAppConns takes a NewTMSPClient func
- Wrap NewTMSPClient in ClientCreator
- Nil -> nilapp

### Throttle_timer

- Fix race, use mtx instead of atomic

### Types

- PrivVal.LastSignature. closes #247

## [0.7.0] - 2016-08-07

### Documentation

- Move FROM to golang:1.4 because 1.4.2 broke

### Makefile

- Go test --race

### Testing

- Broadcast_tx with tmsp; p2p
- Add throughput benchmark using mintnet and netmon
- Install mintnet, netmon
- Use MACH_PREFIX
- Cleanup
- Dont run cloud test on push to master
- README.md

### Binary

- Prevent runaway alloc

### Block/state

- Add CallTx type
- Gas price for block and tx

### Circle

- Docker 1.10.0

### Client

- ResultsCh chan json.RawMessage, ErrorsCh
- Wsc.String()

### Config

- Hardcode default genesis.json
- Block size, consensus timeouts, recheck tx
- Cswal_light, mempool_broadcast, mempool_reap
- Toggle authenticated encryption
- Disable_data_hash (for testing)

### Consensus

- Broadcast evidence tx on ErrVoteConflictingSignature
- Check both vote orderings for dupeout txs
- Fix negative timeout; log levels
- Msg saving and replay
- Replay console
- Use replay log to avoid sign regression
- Don't wait for wal if conS not running
- Dont allow peer round states to decrease
- Cswal doesnt write any consensus msgs in light mode
- Fix more races in tests
- Fix race from OnStop accessing cs.Height
- T.Fatal -> panic
- Hvs.Reset(height, valSet)
- Increase mempool_test timeout
- Don't print shared vars in cs.String()

### Daemon

- Refactor out of cmd into own package

### Db

- Add Close() to db interface. closes #31

### Events

- Integrate event switch into services via Eventable interface

### Glide

- Update go-common
- Update lock and add util scripts

### Mempool

- Add GetState()
- Remove bad txs from cacheMap
- Don't remove committed txs from cache

### P2p

- Push handshake containing chainId for early disconnect. Closes #12
- Fix switch_test to account for handshake
- Broadcast spawns goroutine to Send on each peer and times out after 10 seconds. Closes #7
- Fix switch test for Broadcast returning success channel

### Rpc

- Add status and net info
- Return tx hash, creates contract, contract addr in broadcast (required some helper functions). Closes #30
- Give each call a dedicated Response struct, add basic test
- Separate out golang API into rpc/core
- Generalized rpc using reflection on funcs and params
- Fixes for better type handlings, explicit error field in response, more tests
- Cleanup, more tests, working http and jsonrpc
- Fix tests to count mempool; copy responses to avoid data races
- Return (*Response, error) for all functions
- GetStorage and Call methods. Tests.
- Decrement mempool count after block mined
- GetStorage and Call methods. Tests.
- Decrement mempool count after block mined
- Auto generated client methods using rpc-gen
- Myriad little fixes
- Cleanup, use client for tests, rpc-gen fixes
- Websockets
- Tests cleanup, use client lib for JSONRPC testing too
- Test CallCode and Call
- Fix memcount error in tests
- Use gorilla websockets
- First successful websocket event subscription
- Websocket events testing
- Use NewBlock event in rpc tests
- Cleanup tests and test contract calls
- Genesis route
- Remove unecessary response wrappers
- Add app_hash to /status
- TMResult and TMEventData
- Test cleanup
- Unsafe_set_config
- Num_unconfirmed_txs (avoid sending txs back)
- Start/stop cpu profiler
- Unsafe_write_heap_profile
- Broadcast tests. closes #219
- Unsafe_flush_mempool. closes #190

### Rpc/tests

- Panic dont t.Fatal. use random txs for broadcast

### Server

- Allow multiple connections
- Return result with error

### Service

- Start/stop logs are info, ignored are debug

### State

- ExecTx bug fixes for create contract
- Fix debug logs
- Fix CreateAddress to use Address not Word
- Fixes for creating a contract and msging it in the same block
- Fix GetStorage on blockcache with unknown account
- FireEvents flag on ExecTx and fixes for GetAccount

### Vm

- Check errors early to avoid infinite loop
- Fix Pad functions, state: add debug log for create new account
- Fix endianess by flipping on subslic
- Flip sha3 result
- Fix errors not being returned
- Eventable and flip fix on CALL address
- Catch stack underflow on Peek()


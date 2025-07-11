## [1.5.0-dev.3] - 2025-07-11

### Bug Fixes

- Retry dash core requests when they fail (#1139)

### Features

- Rollback --store flag to to include block store in rollback (#1137)

### Miscellaneous Tasks

- Remove shumkov from CODEOWNERS (#1119)
- Update secp256k1 to use new version of btcsuite/btcd/btcec/v2 (#1118)

### Build

- Bump github.com/oasisprotocol/oasis-core/go (#1110)
- Bump docker/build-push-action from 6.15.0 to 6.16.0 (#1109)
- Bump golangci/golangci-lint-action from 7.0.0 to 8.0.0 (#1112)
- Bump golang.org/x/sync from 0.13.0 to 0.14.0 (#1111)
- Bump actions/setup-go from 5.4.0 to 5.5.0 (#1116)
- Bump bls-signatures go-grpc-* weightedrand snappy x/net x/crypto buf and others (#1117)
- Bump docker/build-push-action from 6.16.0 to 6.17.0 (#1123)
- Bump google.golang.org/grpc from 1.72.0 to 1.72.2 (#1124)
- Bump github.com/prometheus/common from 0.63.0 to 0.64.0 (#1120)
- Bump github.com/oasisprotocol/oasis-core/go (#1125)
- Bump golang.org/x/time from 0.11.0 to 0.12.0 (#1131)
- Bump google.golang.org/grpc from 1.72.2 to 1.73.0 (#1129)
- Bump docker/build-push-action from 6.17.0 to 6.18.0 (#1126)
- Bump golang.org/x/crypto from 0.38.0 to 0.39.0 (#1130)
- Bump golang.org/x/sync from 0.14.0 to 0.15.0 (#1128)
- Bump docker/setup-buildx-action from 3.10.0 to 3.11.1 (#1136)
- Bump github.com/prometheus/common from 0.64.0 to 0.65.0 (#1135)
- Bump github.com/oasisprotocol/oasis-core/go (#1134)
- Bump github.com/bufbuild/buf from 1.54.0 to 1.55.1 (#1133)

## [1.5.0-dev.2] - 2025-04-25

### Bug Fixes

- Statesync is unstable and doesn't time out (#1059)

### Miscellaneous Tasks

- Update changelog and version to 1.5.0-dev.2 (#1108)

### Build

- Bump github.com/fxamacker/cbor/v2 from 2.4.0 to 2.8.0 (#1103)
- Bump github.com/prometheus/client_model from 0.6.1 to 0.6.2 (#1104)
- Bump google.golang.org/grpc from 1.71.1 to 1.72.0 (#1105)
- Bump github.com/bufbuild/buf from 1.50.0 to 1.53.0 (#1102)
- Update go to 1.24.2 (#1106)
- Major update of mockery to 3.2.4 (#1107)
- Bump github.com/oasisprotocol/oasis-core/go (#1089)

## [1.5.0-dev.1] - 2025-04-16

### Features

- Filter unconfirmed txs by tx hash (#1053)
- Allow defining validator power threshold in consensus params (#1052)

### Miscellaneous Tasks

- Update changelog and version to 1.5.0-dev.1 (#1101)

### Build

- Optional jobs must report success to merge PR (#993)
- Bump actions/setup-go from 5.1.0 to 5.2.0 (#1003)
- Bump docker/setup-buildx-action from 3.7.1 to 3.8.0 (#1007)
- Bump google.golang.org/grpc from 1.68.1 to 1.69.0 (#1006)
- Bump golang.org/x/crypto from 0.30.0 to 0.31.0 (#1005)
- Bump google.golang.org/grpc from 1.69.0 to 1.69.4 (#1025)
- Bump docker/build-push-action from 6.10.0 to 6.11.0 (#1023)
- Bump golang.org/x/net from 0.32.0 to 0.34.0 (#1021)
- Bump golang.org/x/term from 0.27.0 to 0.28.0 (#1016)
- Bump github.com/creachadair/atomicfile from 0.3.6 to 0.3.7 (#1015)
- Bump golang.org/x/time from 0.8.0 to 0.9.0 (#1020)
- Bump github.com/creachadair/tomledit from 0.0.23 to 0.0.27 (#1019)
- Bump github.com/golangci/golangci-lint from 1.62.2 to 1.63.4 (#1018)
- Bump github.com/vektra/mockery/v2 from 2.50.0 to 2.50.4 (#1017)
- Bump github.com/prometheus/common from 0.61.0 to 0.62.0 (#1031)
- Bump docker/build-push-action from 6.11.0 to 6.12.0 (#1029)
- Bump golangci/golangci-lint-action from 6.1.1 to 6.2.0 (#1028)
- Bump github.com/bufbuild/buf from 1.47.2 to 1.50.0 (#1027)
- Bump github.com/jonboulle/clockwork from 0.3.0 to 0.5.0 (#1032)
- Bump github.com/go-pkgz/jrpc from 0.2.0 to 0.3.1 (#1033)
- Bump actions/setup-go from 5.2.0 to 5.3.0 (#1035)
- Bump docker/build-push-action from 6.12.0 to 6.13.0 (#1036)
- Bump google.golang.org/grpc from 1.69.4 to 1.70.0 (#1037)
- Bump alpine to 3.21, golang to 1.23.6 (#1042)
- Bump golang.org/x/sync from 0.10.0 to 0.11.0 (#1050)
- Bump golang.org/x/term from 0.28.0 to 0.29.0 (#1051)
- Bump golang.org/x/time from 0.9.0 to 0.10.0 (#1048)
- Bump docker/setup-buildx-action from 3.8.0 to 3.9.0 (#1046)
- Bump golangci/golangci-lint-action from 6.2.0 to 6.3.2 (#1045)
- Bump github.com/cometbft/cometbft-db from 1.0.1 to 1.0.3 (#1044)
- Bump github.com/oasisprotocol/oasis-core/go (#1047)
- Bump golang.org/x/crypto from 0.32.0 to 0.33.0 (#1049)
- Replace gogo/protobuf with cosmos/gogoproto (#1054)
- Bump golangci/golangci-lint-action from 6.3.2 to 6.5.0 (#1058)
- Bump golang.org/x/net from 0.34.0 to 0.35.0 (#1055)
- Bump github.com/spf13/cobra from 1.8.1 to 1.9.1 (#1056)
- Bump github.com/golangci/golangci-lint (#1057)
- Bump docker/build-push-action from 6.13.0 to 6.14.0 (#1063)
- Bump github.com/prometheus/client_golang (#1062)
- Bump github.com/google/go-cmp from 0.6.0 to 0.7.0 (#1060)
- Bump golang.org/x/crypto from 0.33.0 to 0.34.0 (#1061)
- Bump docker/setup-buildx-action from 3.9.0 to 3.10.0 (#1064)
- Bump docker/build-push-action from 6.14.0 to 6.15.0 (#1065)
- Update actions/cache in gha (#1070)
- Bump golang.org/x/crypto from 0.34.0 to 0.35.0 (#1069)
- Bump github.com/golangci/golangci-lint (#1068)
- Bump github.com/cometbft/cometbft-db from 1.0.3 to 1.0.4 (#1067)
- Bump pgregory.net/rapid from 0.4.8 to 1.2.0 (#1066)
- Bump golang.org/x/net from 0.35.0 to 0.37.0 (#1073)
- Bump google.golang.org/grpc from 1.70.0 to 1.71.0 (#1071)
- Bump github.com/prometheus/client_golang (#1072)
- Bump golang.org/x/time from 0.10.0 to 0.11.0 (#1076)
- Bump docker/login-action from 3.2.0 to 3.4.0 (#1078)
- Bump actions/setup-go from 5.3.0 to 5.4.0 (#1079)
- Bump github.com/creachadair/tomledit from 0.0.27 to 0.0.28 (#1083)
- Bump github.com/prometheus/common from 0.62.0 to 0.63.0 (#1085)
- Bump github.com/BurntSushi/toml (#1081)
- Bump github.com/spf13/viper from 1.19.0 to 1.20.1 (#1091)
- Bump golangci/golangci-lint-action from 6.5.0 to 7.0.0 (#1080)
- Bump github.com/creachadair/atomicfile from 0.3.7 to 0.3.8 (#1082)
- Bump google.golang.org/grpc from 1.71.0 to 1.71.1 (#1098)
- Bump golang.org/x/term from 0.30.0 to 0.31.0 (#1096)
- Bump github.com/rs/zerolog from 1.29.0 to 1.34.0 (#1087)
- Bump github.com/prometheus/client_golang (#1097)
- Bump golang.org/x/net from 0.37.0 to 0.39.0 (#1095)
- Bump github.com/golangci/golangci-lint (#1088)

## [1.4.0] - 2024-12-11

### Bug Fixes

- Validators endpoint fail during quorum rotation (#959)
- Node stalled after client has stopped (#1001)

### Miscellaneous Tasks

- [**breaking**] Docker log to stdout and minor logging tweaks (#951)
- Tune stale and dependabot settings (#967)
- Update changelog and version to 1.4.0

### Refactor

- [**breaking**] Remove support for cleveldb, boltdb, rocksdb, badgerdb (#974)

### Testing

- Update mockery configuration and regenerate mocks (#955)

### Build

- Bump actions/setup-go from 5.0.1 to 5.1.0 (#965)
- Bump github.com/creachadair/taskgroup from 0.3.2 to 0.12.0 (#961)
- Bump github.com/prometheus/common from 0.37.0 to 0.60.1 (#964)
- Bump github.com/oasisprotocol/oasis-core/go (#962)
- Replace tendermint/tm-db with cometbft/cometbft-db (#973)
- Bump golang.org/x/sync from 0.8.0 to 0.9.0 (#976)
- Bump github.com/creachadair/taskgroup from 0.12.0 to 0.13.2 (#986)
- Bump golang.org/x/crypto from 0.28.0 to 0.29.0 (#981)
- Bump bufbuild/buf-setup-action from 1.35.0 to 1.46.0 (#969)
- Bump golang.org/x/time from 0.6.0 to 0.8.0 (#980)
- Bump github.com/vektra/mockery/v2 from 2.46.3 to 2.49.1 (#988)
- Bump golang.org/x/net from 0.30.0 to 0.31.0 (#979)
- Bump github.com/golangci/golangci-lint from 1.61.0 to 1.62.2 (#985)
- Bump github.com/bufbuild/buf from 1.35.1 to 1.47.2 (#982)
- Bump google.golang.org/grpc from 1.67.1 to 1.68.0 (#977)
- Bump docker/build-push-action from 6.9.0 to 6.10.0 (#991)
- Bump github.com/oasisprotocol/oasis-core/go (#990)
- Bump bufbuild/buf-setup-action from 1.46.0 to 1.47.2 (#992)
- Bump github.com/creachadair/atomicfile from 0.2.6 to 0.3.6 (#989)
- Bump golang.org/x/term from 0.26.0 to 0.27.0 (#1000)
- Bump google.golang.org/grpc from 1.68.0 to 1.68.1 (#998)
- Bump golang.org/x/sync from 0.9.0 to 0.10.0 (#995)
- Bump golang.org/x/crypto from 0.29.0 to 0.30.0 (#996)
- Bump github.com/vektra/mockery/v2 from 2.49.1 to 2.50.0 (#999)
- Bump golang.org/x/net from 0.31.0 to 0.32.0 (#994)
- Bump github.com/prometheus/common from 0.60.1 to 0.61.0 (#997)

## [1.3.1] - 2024-11-02

### Bug Fixes

- Num of validators that didn't sign is always 0 (#905)
- We should panic if finalize block on apply commit fails (#966)

### Documentation

- Update readme (#934)
- Fix broken links (#940)

### Miscellaneous Tasks

- Update changelog and version to 1.3.1

### Testing

- Update tests for new proposal selection algo (#925)
- Fix proposer selection test (#926)

### Build

- Bump golangci/golangci-lint-action from 6.0.1 to 6.1.1 (#950)
- Bump docker/setup-buildx-action from 3.3.0 to 3.7.1 (#949)
- Bump golang.org/x/crypto from 0.25.0 to 0.28.0 (#945)
- Bump golang.org/x/term from 0.22.0 to 0.25.0 (#942)
- Bump docker/build-push-action from 6.0.0 to 6.9.0 (#935)
- Go 1.23, mockery 2.46.2, golangci-lint 1.61 (#954)

## [1.3.0] - 2024-09-19

### Bug Fixes

- Validators form islands on genesis (#850)
- Panic on block_results when consensus params change (#923)

### Miscellaneous Tasks

- Update changelog and version to 1.3.0

## [1.1.0-dev.3] - 2024-07-25

### Features

- Allow overriding genesis time in InitChain (#847)

### Miscellaneous Tasks

- Update changelog and version to 1.1.0-dev.3 (#848)

## [1.1.0-dev.2] - 2024-07-24

### Bug Fixes

- Address already in use (#845)
- Active validators not always connected to each other (#844)

### Miscellaneous Tasks

- Update changelog and version to 1.1.0-dev.2 (#846)

## [1.1.0-dev.1] - 2024-07-23

### Features

- [**breaking**] Replace dash core quorum sign with quorum platformsign (#828)

### Miscellaneous Tasks

- Update changelog and version to 1.1.0-dev.1 (#842)

### Build

- Bump bufbuild/buf-setup-action from 1.33.0 to 1.35.0 (#841)
- Run dependabot on default branch, not master (#843)

## [1.2.1] - 2024-08-29

### Miscellaneous Tasks

- Update changelog and version to 1.2.1

## [1.2.1-dev.1] - 2024-08-29

### Bug Fixes

- Genesis.json not loaded on restart before genesis block is mined
- Panic when loading invalid node key file (#888)

### Miscellaneous Tasks

- Update changelog and version to 1.2.1-dev.1

### Build

- Bump git-cliff to 2.4 and remove history before 1.0 (#882)

## [1.2.0] - 2024-08-15

### Bug Fixes

- Vote extensions verified multiple times (#867)

### Features

- Configuration of deadlock detection (#880)

### Miscellaneous Tasks

- Update changelog and version to 1.2.0

## [1.2.0-dev.3] - 2024-08-12

### Bug Fixes

- Msg queue is too small for mainnet (#863)
- Non-active validators can't verify evidence signatures (#865)

### Miscellaneous Tasks

- Improve logs (#866)
- Update changelog and version to 1.2.0-dev.3 (#868)

## [1.2.0-dev.2] - 2024-08-05

### Bug Fixes

- Build of dev releases fails due to invalid tags (#859)

### Miscellaneous Tasks

- Update changelog and version to 1.2.0-dev.2 (#861)

### Build

- E2e tests fail due to lack of docker-compose command (#860)

## [1.2.0-dev.1] - 2024-08-05

### Bug Fixes

- Proposal not generated after waiting for last block time to pass (#849)

### Miscellaneous Tasks

- Update changelog and version to 1.2.0-dev.1 (#858)

### Build

- Add signed binaries to releases (#854)

## [1.1.0] - 2024-07-29

### Bug Fixes

- Address already in use (#845)
- Active validators not always connected to each other (#844)
- Validators form islands on genesis (#850)

### Features

- [**breaking**] Replace dash core quorum sign with quorum platformsign (#828)
- Allow overriding genesis time in InitChain (#847)

### Miscellaneous Tasks

- Update changelog and version to 1.1.0-dev.1 (#842)
- Update changelog and version to 1.1.0-dev.2 (#846)
- Update changelog and version to 1.1.0-dev.3 (#848)
- Update changelog and version to 1.1.0

### Build

- Bump bufbuild/buf-setup-action from 1.33.0 to 1.35.0 (#841)
- Run dependabot on default branch, not master (#843)

## [1.0.0] - 2024-07-01

### Miscellaneous Tasks

- Update changelog and version to 1.0.0

### Build

- Bump github.com/stretchr/testify from 1.8.2 to 1.9.0 (#817)

## [1.0.0-dev.2] - 2024-06-26

### Bug Fixes

- Ineffective PROXY_APP and ABCI env in entrypoint (#805)

### Miscellaneous Tasks

- Update changelog and version to 1.0.0-dev.2 (#806)


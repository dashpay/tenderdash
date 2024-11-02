## [1.3.1] - 2024-11-02

### Bug Fixes

- Num of validators that didn't sign is always 0 (#905)
- We should panic if finalize block on apply commit fails (#966)

### Documentation

- Update readme (#934)
- Fix broken links (#940)

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

- Address already in use (#845)
- Active validators not always connected to each other (#844)
- Validators form islands on genesis (#850)
- Panic on block_results when consensus params change (#923)

### Features

- [**breaking**] Replace dash core quorum sign with quorum platformsign (#828)
- Allow overriding genesis time in InitChain (#847)

### Miscellaneous Tasks

- Update changelog and version to 1.1.0-dev.1 (#842)
- Update changelog and version to 1.1.0-dev.2 (#846)
- Update changelog and version to 1.1.0-dev.3 (#848)
- Update changelog and version to 1.3.0

### Build

- Bump bufbuild/buf-setup-action from 1.33.0 to 1.35.0 (#841)
- Run dependabot on default branch, not master (#843)

## [1.2.1] - 2024-08-29

### Bug Fixes

- Genesis.json not loaded on restart before genesis block is mined
- Panic when loading invalid node key file (#888)

### Miscellaneous Tasks

- Update changelog and version to 1.2.1-dev.1
- Update changelog and version to 1.2.1

### Build

- Bump git-cliff to 2.4 and remove history before 1.0 (#882)

## [1.2.0] - 2024-08-15

### Bug Fixes

- Proposal not generated after waiting for last block time to pass (#849)
- Build of dev releases fails due to invalid tags (#859)
- Msg queue is too small for mainnet (#863)
- Non-active validators can't verify evidence signatures (#865)
- Vote extensions verified multiple times (#867)

### Features

- Configuration of deadlock detection (#880)

### Miscellaneous Tasks

- Update changelog and version to 1.2.0-dev.1 (#858)
- Update changelog and version to 1.2.0-dev.2 (#861)
- Improve logs (#866)
- Update changelog and version to 1.2.0-dev.3 (#868)
- Update changelog and version to 1.2.0

### Build

- Add signed binaries to releases (#854)
- E2e tests fail due to lack of docker-compose command (#860)

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

### Bug Fixes

- Ineffective PROXY_APP and ABCI env in entrypoint (#805)

### Miscellaneous Tasks

- Update changelog and version to 1.0.0-dev.2 (#806)
- Update changelog and version to 1.0.0

### Build

- Bump github.com/stretchr/testify from 1.8.2 to 1.9.0 (#817)


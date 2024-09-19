# ADR D002: Tenderdash Consensus Versioning

## Status

Proposed

## Context

Tenderdash currently lacks a flexible mechanism for versioning and feature toggling. The need has arisen to introduce new consensus features without requiring hard forks or block version changes. Specifically, there is a proposal to add a new consensus parameter, proposerSelectionStrategy, which changes the way Tenderdash selects a proposer.

## Problem statement

Tenderdash requires a mechanism to adapt and evolve without necessitating hard forks. The current system lacks flexibility in managing changes, which can include:

- **Bug Fixes**: Addressing and resolving defects in the codebase.
- **New Features**: Introducing new functionalities to enhance the system.
- **Modifications to Existing Features**: Updating or improving current features to meet evolving requirements.

The challenge is to implement these changes in a way that allows for seamless updates, ensuring that Tenderdash can remain robust and adaptable without disrupting the network through hard forks.

We shall also ensure separation of concerns, to avoid building logic that depends on particular use case/blockchain (like Dash Platform).

## Proposed solutions

### Solution 1: Use Block Version

The block header contains a `Version` field, which includes the block version. This versioning mechanism can be utilized to manage changes in consensus logic and feature implementations.

#### Pros

- **Existing Field**: The version field is already present in the block header, providing a built-in mechanism for versioning.
- **Consistency**: Using block versions ensures that all nodes in the network are synchronized with the same versioning logic.

#### Cons

- **Complex Decision-Making**: Determining when to switch logic based on block version can be complex and may depend on specific cases, such as the Dash Platform mainnet.
- **Hard Fork Requirement**: Changing the block version is considered a hard fork, which is a significant and complex task that requires coordination across the network.
- **Hardcoded Version**: The block version is hardcoded in `version/version.go`, making changes cumbersome and extensive.
- **Client Compatibility**: All clients must understand and support all block versions, adding to the complexity and increasing the risk of compatibility issues.

### Scenario 2: Introduce features mechanism

Introduce a new mechanism within consensus parameters to define and control various features. This mechanism will allow for dynamic feature toggling based on consensus parameters rather than block versions. Each feature will have a corresponding parameter that can be set to enable, disable, or select different implementations of the feature.

#### Details

1. **New Consensus Parameters**:
   - `features`: A map or array of feature flags and their corresponding implementations. Each feature will have a key representing the feature name and a value representing the selected implementation or state (e.g., enabled, disabled).

2. **Implementation**:
   - Modify the consensus parameters to include the `features` map or array.
   - Implement factory design pattern to use implementation selected by consensus params.
   - (Optional) Organize features into a separate directory in the project directory tree

3. **Example Usage**:
   - At genesis, `features["proposerSelectionStrategy"]` is set to `height`.
   - At block height 10000, the ABCI App can change `features["proposerSelectionStrategy"]` to `heightRound`.
   - At block height 30000, the ABCI App can change `features["proposerSelectionStrategy"]` back to `height`.


#### Pros

- **Flexibility**: Using consensus parameters for feature toggling allows for more granular control over consensus behavior without requiring hard forks or block version changes.
- **Scalability**: This approach is scalable as it avoids the need to create new block versions for each feature change.
- **Separation of Concerns**: Tenderdash logic should not depend on the ABCI App specific use case. Consensus parameters provide a clean separation between the consensus engine and the application logic.

#### Cons

- Potential complexity in managing a large number of consensus parameters.

## Decision

We decided to follow solution 1: use block version.

We believe that at this point, having single, consistent consensus algorithm version is more important from long-term
perspective than managing and maintaining multiple distinct features.

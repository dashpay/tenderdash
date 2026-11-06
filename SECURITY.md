# Security

Tenderdash is a Dash Platform component. Security response and supported fixes
are coordinated with Dash Platform development, deployed networks, and upgrade
requirements; there is no separate Tenderdash minor-version support window.

## Reporting a vulnerability

Use the repository's [private vulnerability reporting](https://github.com/dashpay/tenderdash/security/advisories/new)
when available. If that option is unavailable, ask the repository maintainers
for a private reporting channel without including exploit details in a public
issue. Do not send Tenderdash reports to the inherited Cosmos bounty program.

Provide the affected Tenderdash and Dash Platform versions, network or deployment
context, impact, and a minimal reproduction. Remove private keys, credentials,
and personal data. Test only systems you own or have permission to assess;
avoid disrupting public networks or accessing other users' data.

## Coordinated response

Maintainers assess the report with Dash Platform engineers, identify affected
deployments, and prepare fixes privately when disclosure would put users at
risk. Fixes, testing, operator communication, and disclosure are coordinated
with the relevant Platform release and network upgrade. Security-sensitive
patches must reach each affected deployment line; branch naming does not decide
whether a deployed network needs a fix.

Consensus safety and liveness failures, malformed remote input, resource
exhaustion, signature or proof validation errors, RPC exposure, and secret leaks
are relevant reports. Ordinary bug reports without sensitive security details
can use GitHub issues. This policy does not promise a bounty or a fixed public
disclosure deadline.

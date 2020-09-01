# lotus changelog

# 0.5.7 / 2020-08-31

This patch release includes some bugfixes and enhancements to the sector lifecycle and message pool logic. 

## Changes

- Rebuild unsealed infos on miner restart (https://github.com/filecoin-project/lotus/pull/3401)
- CLI to attach storage paths to workers (https://github.com/filecoin-project/lotus/pull/3405)
- Do not select negative performing message chains for inclusion (https://github.com/filecoin-project/lotus/pull/3392)
- Remove a redundant error-check (https://github.com/filecoin-project/lotus/pull/3421)
- Correctly move unsealed sectors in `FinalizeSectors` (https://github.com/filecoin-project/lotus/pull/3424)
- Improve worker selection logic (https://github.com/filecoin-project/lotus/pull/3425)
- Don't use context to close bitswap (https://github.com/filecoin-project/lotus/pull/3430)
- Correctly estimate gas premium when there is only one message on chain (https://github.com/filecoin-project/lotus/pull/3428)

# 0.5.6 / 2020-08-29

We are very excited to release **lotus** 0.1.0. This is our testnet release. To install lotus and join the testnet, please visit [docs.lotu.sh](docs.lotu.sh). Please file bug reports as [issues](https://github.com/filecoin-project/lotus/issues).

A huge thank you to all contributors for this testnet release!

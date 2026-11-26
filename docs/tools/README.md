---
order: 1
parent:
  title: Tooling
  order: 8
---

# Overview

Tenderdash has some tools that are associated with it for:

- [Debugging](./debugging/pro.md)
- [Benchmarking](#benchmarking)
- [Testnets](#testnets)

## Benchmarking

- <https://github.com/informalsystems/tm-load-test>

`tm-load-test` is an external load testing tool originally developed for upstream
networks. Verify its transaction format and RPC compatibility before using it
with Tenderdash.

## Testnets

- <https://github.com/informalsystems/testnets>

This external repository contains upstream testnet configurations. They are
not Tenderdash network configurations.

Use [Docker Compose](./docker-compose.md) to spin up Tenderdash testnets on your
local machine.

Use [Terraform and Ansible](./terraform-and-ansible.md) to deploy Tenderdash
testnets to the cloud.

See the `tenderdash testnet --help` command for more help initializing testnets.

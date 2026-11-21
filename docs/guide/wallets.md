---
title: "Wallets and RPC"
description: "Accounts, private signing and network access."
---

Open **Wallets** to choose a network and configure an account. RPC is the network connection Fased uses to read and submit blockchain information. Its address and optional access credential belong to the selected network.

Wallet creation, import, recovery and approval use a protected signing flow. Chat and agents do not receive private keys. A public address can be viewed without granting permission to spend from it.

Installing Fased does not require a wallet. Connecting an existing installation preserves its records and settings. Keep wallet recovery material private and separate from ordinary account passwords.

<Note>Watch-only Solana accounts and live read-only Solana RPC (balances, tokens and transaction activity via keyless Solana RPC or OS-protected RPC keys) are available. Live signing keys, funds custody and transaction submission remain disabled — do not fund development addresses or enter real recovery phrases.</Note>

An agent, subscription or paper result cannot grant wallet authority. Any uncertain transaction needs review before another action is attempted. See [Availability](/reference/status) and [Strategies](/guide/trading).

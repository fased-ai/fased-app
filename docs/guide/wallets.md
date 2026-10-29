---
title: "Wallets and RPC"
description: "Protected custody and network-bound configuration."
---

Wallets joins network selection, public account records, protected Go-signer create/import and RPC configuration. The core and Chat never receive wallet private keys.

Choose the account/network, then configure its endpoint and credential through the Wallets flow. Credentials are encrypted; endpoint and network validation belong to the same saved account. A public address alone does not prove that the signer can use it.

Protected create/import, recovery and approval use the installed owner boundary. Ordinary installation does not require a wallet. The same-instance enrollment path preserves identity, data, URL and recovery holds rather than starting a blank instance.

<Note>Custody and RPC engineering are verified with disposable keys and controlled endpoints. Real keys, funds, RPC and live signing remain disabled. Do not fund development addresses.</Note>

A Network subscription, agent profile or successful paper result cannot authorize signing. Recovery holds and uncertain operations need explicit reconciliation. See [Status](/reference/status) and [Trading](/guide/trading).

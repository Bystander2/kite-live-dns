# Kite x402 Axum wrapper

Reusable Rust/Axum x402 v2 reverse proxy. Every `/v1/*` request is paid;
`/healthz` is free. The wrapper sends the payment to the facilitator for
verification, calls the upstream, and settles only after a complete response with status below 400, matching the
official Gin middleware. Redirects are returned to the buyer without being followed.
A failed settlement withholds the successful upstream response.

## Run the local example

```sh
cargo run --example upstream
# In another terminal, from rust-axum:
export PAY_TO=0xYourKiteWalletAddress
export UPSTREAM_URL=http://127.0.0.1:8081
export KITE_NETWORK=testnet
cargo run
curl -i 'http://localhost:8080/v1/records?type=A'
```

Replace PAY_TO with a nonzero public EVM address. No private key is required.
The unpaid request returns 402 and a base64 JSON `PAYMENT-REQUIRED` header.
Retry with a buyer-signed x402 v2 payload in `PAYMENT-SIGNATURE`; the payload
must contain `x402Version`, `accepted` (the exact advertised requirements),
and `payload`. A successful paid request returns upstream JSON and a base64
`PAYMENT-RESPONSE` containing the settlement transaction.

## Configuration

Same environment variables as the official Kite wrapper:

| Variable | Default / purpose |
| --- | --- |
| PAY_TO | Required receiving EVM address |
| UPSTREAM_URL | Required HTTP base URL; `/v1/` is stripped |
| KITE_NETWORK | `mainnet`; or `testnet` |
| PRICE_USD | `0.001` or `$0.001`; exact decimal |
| SERVICE_DESCRIPTION | Paid API wrapped for the Kite network |
| UPSTREAM_AUTH_HEADER | `Authorization` |
| UPSTREAM_AUTH_VALUE | Optional upstream credential |
| FACILITATOR_URL | `https://facilitator.pieverse.io/v2` |
| PORT | `8080` |

Testnet: `eip155:2368`, pieUSD (18 decimals),
`0x38129cf4CE5E183eFF248F42A7D345Bb1B47621A`.
Mainnet: `eip155:2366`, USDC.e (6 decimals),
`0x7aB6f3ed87C42eF0aDb67Ed95090f8bF5240149e`.

The upstream host is fixed by configuration. Redirects are not followed.
Payment and hop-by-hop headers are stripped in both directions, and the
configured credential replaces any buyer-supplied credential of that name.
Request bodies are limited to 2 MiB. Responses are buffered before settlement;
this template is intended for bounded JSON APIs, not streaming endpoints.

## Verify and package

```sh
cargo fmt --check
cargo test
cargo clippy --all-targets -- -D warnings
cargo package --allow-dirty
```

Tests use local HTTP servers with a mock facilitator. They prove 402 behavior,
paid proxy ordering, header handling, rejection, upstream failure and settlement
failure. Mock transaction values are not real on-chain payment evidence.

Published: https://crates.io/crates/kite-live-dns-axum/0.1.1

## Deployment and real payment verification

Deployed example: https://kite-rust-axum.vercel.app/ . Vercel uses a Python
bridge to start the Rust binary; Rust/Axum handles verification, proxying and settlement.

On 2026-10-04, `/healthz` returned 200 and an unpaid DNS request returned 402.
A MetaMask-signed payment of 0.001 pieUSD on Kite Testnet (`eip155:2368`)
returned HTTP 200 with DNS data and `settlement.success=true`. The independently
queried transaction receipt had `status=0x1`.

Transaction: `0x24ee2b6c2ec43915d68531abab399b4c454a8a1df9006027c80d531172f92391`.
The payer and recipient were the same address, so the token balance had no net
change. This verifies a real testnet transfer; mainnet payment remains untested.

See [verification notes](验证记录.md) and the sanitized
[payment receipt](evidence/payment-2026-10-04.json). CI additionally tests failure
paths using a mock facilitator; those tests are separate from the real payment evidence.

## Publishing
The crate name is `kite-live-dns-axum`. After authenticating to crates.io:

```sh
cargo publish --dry-run
cargo publish
```

Official reference: https://github.com/gokite-ai/kite-x402-services

Published version 0.1.1: adds service description, resource metadata, dollar-prefixed prices and facilitator rejection reasons.

Behavior comparison and known differences: [official template comparison](官方模板对照.md).

Version 0.1.2 (pending publication): aligns non-error status settlement and
failed-settlement PAYMENT-RESPONSE headers with the official Gin SDK, adds
health asset/price fields, and fixes HTTPS resource metadata and OPTIONS/TRACE
in the Vercel bridge. Strict input validation and additional header filtering remain.

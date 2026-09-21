# Live Public DNS — Kite x402 service

A paid `GET /v1/dns?name=example.com&type=A` endpoint returning current DNS records.
Unlike a static language classifier, it retrieves external state an offline model cannot know.

**Status: deployed on Cloudflare and verified with a successful paid Kite testnet call.**

Public URL: https://kite-live-dns.sakurasoga7.workers.dev

中文运行与验证记录：[验证记录.md](验证记录.md)。

Two equivalent implementations are included:

- TypeScript / Express 5 at the repository root; this is the deployed Cloudflare Workers version.
- Go 1.22 / Gin in [`go-gin/`](go-gin/), with matching x402, DNS and settlement behavior.

## Run (Node.js 22)

```sh
npm ci
npm run build
cp .env.example .env
# Set PAY_TO to your own receiving EVM address. No private key is needed by this server.
node --env-file=.env dist/index.js
curl -i 'http://localhost:8080/v1/dns?name=example.com&type=A'
```

Expected unpaid response: HTTP 402 and a base64 `PAYMENT-REQUIRED` header.
`GET /healthz` is free. Default network is Kite testnet; price is 0.01 PYUSD per successful query.
The runtime rejects an empty or zero receiving address.

## Call chain and modules

- `src/app.ts`: method/query validation → EIP-3009 signature verification → DNS lookup → Kite gasless settlement and receipt verification → response.
- `src/dns.ts`: IDN normalization, domain/type validation and bounded public DNS lookups.
- `src/kite.ts`: unchanged official template chain definitions and amount conversion.
- `src/index.ts`: environment configuration and HTTP listener.

Supported types: A, AAAA, MX, TXT, NS, CNAME, SOA, CAA. Names must have multiple labels.
Queries go only to the fixed `https://dns.google/resolve` HTTPS endpoint; user input cannot select a host.
Redirects are disabled, and requests time out after six seconds. Client credentials are not forwarded.
The EDNS client subnet is explicitly zeroed. Google sees the requested domain and server IP.
Results can be cached; this is not an authoritative DNS service or a guarantee that returned IPs are safe.
Commercial proxy/resale suitability and upstream terms still need confirmation before public launch.
See [Google's JSON API documentation](https://developers.google.com/speed/public-dns/docs/doh/json).

## Responses

- 200: `{ name, type, records, queried_at }` plus `PAYMENT-RESPONSE` after settlement.
- 400: invalid domain or record type (before payment verification).
- 402: payment required, invalid authorization, or SDK payment failure.
- 404: no DNS record / nonexistent domain; no settlement.
- 405: method other than GET; no settlement.
- 502: DNS failure / timeout; no settlement.

`records` is an array of `{ name, type, ttl, data }`; `type` is the DNS numeric record code.
`data` preserves upstream DNS text (including quoting of TXT records), and CNAME chains may be included.
`queried_at` is server query completion time, not authoritative record creation time.

## Verify locally

```sh
npm run typecheck
npm test
npm run build
# In the repository root:
npm ci
npm run validate
```

Tests cover the x402 middleware and gasless flow with injected DNS responses.
They verify challenge amount/network, call order, failed authorization, DNS failures and settlement failure.
The successful public call and on-chain receipt are saved in `evidence/real-payment-2026-09-18.json`.

## Deployment and real payment evidence

The deployed testnet service uses the current self-claimable PYUSD contract and Kite's gasless relay.
The manifest records the live URL, receiving address, 0.01 PYUSD price and `testnet` status.
Mainnet still requires a separate deployment and successful mainnet payment.

Do not include private keys, OTPs, session tokens, full payment authorizations or `.env` in evidence.
Before public launch, configure host-level request limits and concurrency limits to bound DNS load.

## Attribution

Derived from `gokite-ai/kite-x402-services/templates/typescript-express`, Apache-2.0.
See the repository root LICENSE. The original `kite.ts` is preserved without edits.

## Cloudflare Workers (prepared, runtime verification pending)

`workers/index.js` adapts the existing Express app through `httpServerHandler`.
Initialization happens in the first request because x402 initialization makes outbound HTTP calls.
`wrangler.jsonc` enables Node compatibility and defaults to testnet.

```sh
npm ci
npm run cf:dev
npm run cf:check
```

Local Wrangler reads `.env`; alternatively use ignored `.dev.vars`. Never commit either file.
Cloud deployment does not automatically upload `.env`. Set the public receiving address as a
Cloudflare `PAY_TO` binding before serving traffic; keep it consistent with the manifest.
Only use `npm run cf:deploy` after verifying the selected Cloudflare account and runtime behavior.

Native macOS tooling is unavailable on this machine. Use `Dockerfile.tools` to run
Wrangler under Linux with system CA certificates. Docker dry-run and local Workers
HTTP smoke tests have now passed; see 验证记录.md and evidence/workers-unpaid-smoke.json.
Cloudflare deployment and public unpaid HTTP checks passed. See evidence/deployment.json.

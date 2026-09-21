# Go / Gin implementation

This directory implements the same paid `GET /v1/dns` service in Go 1.22 and Gin.
It emits the same Kite testnet x402 v2 challenge, verifies the PYUSD EIP-3009
signature, calls Google Public DNS, submits through Kite's gasless relay, waits
for a successful on-chain receipt, and only then returns HTTP 200.

```sh
go mod download
go test ./...
PAY_TO=0xYourAddress PRICE_USD=0.01 go run .
curl -i 'http://localhost:8080/v1/dns?name=example.com&type=A'
```

The unpaid call returns HTTP 402 with a base64 `Payment-Required` header.
`GET /healthz` is free. Never put a private key in this service: the buyer signs
the EIP-3009 authorization in their wallet and the Kite relay submits it.

Tests assert the required `verify -> upstream -> settle` order and prove that an
invalid request or failed upstream call cannot settle.

## Free Vercel deployment

Vercel's Go runtime entry point is `api/index.go`. `vercel.json` rewrites the
public `/healthz` and `/v1/dns` paths to that function while the same Gin router
handles the request. The public receiver and price have safe defaults and can be
overridden with `PAY_TO` and `PRICE_USD` environment variables.

## Cloudflare Container deployment

The included `worker.ts` and `wrangler.jsonc` route a Worker to the Gin image.
Cloudflare Containers requires a Workers Paid plan, Docker for local image
builds, and a Wrangler login with `containers:write` permission.

```sh
npm ci
npm run cf:check
npm run cf:deploy
```

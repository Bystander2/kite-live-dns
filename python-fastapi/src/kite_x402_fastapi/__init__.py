from __future__ import annotations

import os
import re
from decimal import Decimal
from urllib.parse import urljoin

import httpx
import uvicorn
from fastapi import FastAPI, Request, Response
from x402.http import FacilitatorConfig, HTTPFacilitatorClient, PaymentOption
from x402.http.middleware.fastapi import payment_middleware
from x402.http.types import RouteConfig
from x402.mechanisms.evm.exact import ExactEvmServerScheme
from x402.schemas import AssetAmount
from x402.server import x402ResourceServer

CHAINS = {
    "testnet": ("eip155:2368", "0x38129cf4CE5E183eFF248F42A7D345Bb1B47621A", 18, "pieUSD", "1"),
    "mainnet": ("eip155:2366", "0x7aB6f3ed87C42eF0aDb67Ed95090f8bF5240149e", 6, "Bridged USDC (Kite AI)", "2"),
}


def create_app(*, pay_to: str, upstream_url: str, network_name: str = "testnet", price_usd: str = "0.001", facilitator_url: str = "https://facilitator.pieverse.io/v2", client: httpx.AsyncClient | None = None, server: x402ResourceServer | None = None, sync_on_start: bool = True) -> FastAPI:
    if not re.fullmatch(r"0x[0-9a-fA-F]{40}", pay_to) or int(pay_to, 16) == 0:
        raise ValueError("PAY_TO must be a nonzero EVM address")
    if network_name not in CHAINS:
        raise ValueError("KITE_NETWORK must be testnet or mainnet")
    network, asset, decimals, token_name, token_version = CHAINS[network_name]
    amount = str(int(Decimal(price_usd) * Decimal(10) ** decimals))
    if int(amount) <= 0 or not upstream_url.startswith(("https://", "http://")):
        raise ValueError("invalid PRICE_USD or UPSTREAM_URL")
    upstream = client or httpx.AsyncClient(timeout=20, follow_redirects=False)
    if server is None:
        server = x402ResourceServer(HTTPFacilitatorClient(FacilitatorConfig(url=facilitator_url)))
        server.register(network, ExactEvmServerScheme())
    routes = {"* /v1/*": RouteConfig(accepts=PaymentOption(scheme="exact", pay_to=pay_to, price=AssetAmount(amount=amount, asset=asset, extra={"name": token_name, "version": token_version}), network=network, max_timeout_seconds=60), description="Paid API wrapped for Kite", mime_type="application/json")}
    protect = payment_middleware(routes, server, sync_facilitator_on_start=sync_on_start)
    app = FastAPI(title="Kite x402 FastAPI Wrapper")

    @app.middleware("http")
    async def paid(request: Request, call_next):
        return await protect(request, call_next)

    @app.get("/healthz")
    async def healthz():
        return {"ok": True, "network": network, "price": price_usd, "runtime": "python/fastapi"}

    @app.api_route("/v1/{path:path}", methods=["GET", "POST", "PUT", "PATCH", "DELETE"])
    async def proxy(path: str, request: Request):
        headers = {k: v for k, v in request.headers.items() if k.lower() not in {"host", "content-length", "payment-signature", "x-payment"}}
        target = urljoin(upstream_url.rstrip("/") + "/", path)
        if request.url.query:
            target += "?" + request.url.query
        try:
            result = await upstream.request(request.method, target, headers=headers, content=await request.body())
        except httpx.HTTPError:
            return Response('{"error":"upstream_unavailable"}', status_code=502, media_type="application/json")
        return Response(result.content, status_code=result.status_code, media_type=result.headers.get("content-type"))

    return app


def main() -> None:
    app = create_app(pay_to=os.getenv("PAY_TO", ""), upstream_url=os.getenv("UPSTREAM_URL", ""), network_name=os.getenv("KITE_NETWORK", "testnet"), price_usd=os.getenv("PRICE_USD", "0.001"), facilitator_url=os.getenv("FACILITATOR_URL", "https://facilitator.pieverse.io/v2"))
    uvicorn.run(app, host="0.0.0.0", port=int(os.getenv("PORT", "8080")))


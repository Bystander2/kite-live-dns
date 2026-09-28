import base64
import json

import httpx
from fastapi.testclient import TestClient
from x402.mechanisms.evm.exact import ExactEvmServerScheme
from x402.schemas import SettleResponse, SupportedKind, SupportedResponse, VerifyResponse
from x402.server import x402ResourceServer

from kite_x402_fastapi import create_app

PAY_TO = "0xa5d1f687B741af9b2b7c2b0d77757C6a0De69055"


class Facilitator:
    def __init__(self, calls):
        self.calls = calls

    def get_supported(self):
        return SupportedResponse(
            kinds=[SupportedKind(x402_version=2, scheme="exact", network="eip155:2368")]
        )

    async def verify(self, _payload, _requirements):
        self.calls.append("verify")
        return VerifyResponse(is_valid=True, payer=PAY_TO.lower())

    async def settle(self, _payload, requirements):
        self.calls.append("settle")
        return SettleResponse(
            success=True,
            payer=PAY_TO.lower(),
            transaction="0xmock",
            network=requirements.network,
            amount=requirements.amount,
        )


def fixture(status=200):
    calls = []

    def upstream(request):
        calls.append("upstream")
        assert request.url.path == "/records"
        assert "payment-signature" not in request.headers
        return httpx.Response(status, json={"records": ["203.0.113.7"]})

    client = httpx.AsyncClient(transport=httpx.MockTransport(upstream))
    server = x402ResourceServer(Facilitator(calls))
    server.register("eip155:2368", ExactEvmServerScheme())
    server.initialize()
    app = create_app(
        pay_to=PAY_TO,
        upstream_url="https://upstream.example",
        client=client,
        server=server,
        sync_on_start=False,
    )
    return TestClient(app), calls


def accepted(response):
    required = json.loads(base64.b64decode(response.headers["payment-required"]))
    return required["accepts"][0]


def signature(requirement):
    payload = {
        "x402Version": 2,
        "accepted": requirement,
        "payload": {"signature": "0xtest", "authorization": {"from": PAY_TO}},
    }
    return base64.b64encode(json.dumps(payload).encode()).decode()


def test_unpaid_returns_kite_402_without_upstream():
    client, calls = fixture()
    response = client.get("/v1/records")
    payment = accepted(response)
    assert response.status_code == 402
    assert payment["network"] == "eip155:2368"
    assert payment["asset"] == "0x38129cf4CE5E183eFF248F42A7D345Bb1B47621A"
    assert payment["amount"] == "1000000000000000"
    assert calls == []


def test_paid_order_is_verify_upstream_settle():
    client, calls = fixture()
    requirement = accepted(client.get("/v1/records"))
    response = client.get("/v1/records", headers={"Payment-Signature": signature(requirement)})
    assert response.status_code == 200
    assert response.headers["payment-response"]
    assert calls == ["verify", "upstream", "settle"]


def test_failed_upstream_never_settles():
    client, calls = fixture(503)
    requirement = accepted(client.get("/v1/records"))
    response = client.get("/v1/records", headers={"Payment-Signature": signature(requirement)})
    assert response.status_code == 503
    assert calls == ["verify", "upstream"]


def test_health_is_free():
    client, calls = fixture()
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json()["runtime"] == "python/fastapi"
    assert calls == []


import os

from kite_x402_fastapi import create_app

app = create_app(
    pay_to=os.getenv("PAY_TO", "0xa5d1f687B741af9b2b7c2b0d77757C6a0De69055"),
    upstream_url=os.getenv("UPSTREAM_URL", "https://dns.google"),
    network_name=os.getenv("KITE_NETWORK", "testnet"),
    price_usd=os.getenv("PRICE_USD", "0.001"),
    facilitator_url=os.getenv("FACILITATOR_URL", "https://facilitator.pieverse.io/v2"),
)


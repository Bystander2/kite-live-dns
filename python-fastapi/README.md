# Kite x402 FastAPI wrapper

Reusable `/v1/*` reverse proxy using the official Python x402 middleware on Kite.
The middleware preserves `verify -> upstream -> settle` and never settles an
upstream response with status `>= 400`.

```bash
python -m venv .venv
. .venv/bin/activate
pip install -e '.[dev]'
cp .env.example .env
set -a && . ./.env && set +a
kite-x402-fastapi
curl -i http://localhost:8080/v1/example
```

Testnet uses pieUSD on `eip155:2368`; mainnet uses USDC.e on `eip155:2366`.


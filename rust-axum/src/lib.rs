use axum::{
    Router,
    body::{Body, to_bytes},
    extract::{Request, State},
    http::{HeaderMap, HeaderValue, StatusCode},
    response::{IntoResponse, Response},
    routing::{any, get},
};
use base64::{Engine, engine::general_purpose::STANDARD};
use reqwest::{Client, Url};
use serde_json::{Value, json};
use std::{env, time::Duration};

#[derive(Clone)]
pub struct Config {
    pub pay_to: String,
    pub upstream_url: String,
    pub network: String,
    pub price_usd: String,
    pub facilitator_url: String,
    pub upstream_auth: Option<(String, String)>,
}
impl Config {
    pub fn from_env() -> Self {
        let read = |key: &str, default: &str| env::var(key).unwrap_or_else(|_| default.into());
        Self {
            pay_to: read("PAY_TO", ""),
            upstream_url: read("UPSTREAM_URL", ""),
            network: read("KITE_NETWORK", "mainnet"),
            price_usd: read("PRICE_USD", "0.001"),
            facilitator_url: read("FACILITATOR_URL", "https://facilitator.pieverse.io/v2"),
            upstream_auth: env::var("UPSTREAM_AUTH_VALUE")
                .ok()
                .filter(|s| !s.is_empty())
                .map(|v| (read("UPSTREAM_AUTH_HEADER", "Authorization"), v)),
        }
    }
}
#[derive(Clone)]
struct AppState {
    config: Config,
    client: Client,
    requirements: Value,
    upstream: Url,
}

fn amount(price: &str, decimals: usize) -> Result<String, String> {
    let parts: Vec<_> = price.split('.').collect();
    if parts.len() > 2 || parts[0].is_empty() || !parts[0].bytes().all(|b| b.is_ascii_digit()) {
        return Err("PRICE_USD must be a positive decimal".into());
    }
    let fraction = parts.get(1).copied().unwrap_or("");
    if fraction.len() > decimals
        || !fraction.bytes().all(|b| b.is_ascii_digit())
        || (parts.len() == 2 && fraction.is_empty())
    {
        return Err("PRICE_USD has invalid precision".into());
    }
    let digits = format!(
        "{}{}{}",
        parts[0],
        fraction,
        "0".repeat(decimals - fraction.len())
    );
    let result = digits.trim_start_matches('0');
    if result.is_empty() || result.len() > 77 {
        return Err("PRICE_USD is zero or too large".into());
    }
    Ok(result.into())
}
pub fn router(config: Config) -> Result<Router, String> {
    let address = config
        .pay_to
        .strip_prefix("0x")
        .ok_or("PAY_TO must be an EVM address")?;
    if address.len() != 40
        || !address.bytes().all(|b| b.is_ascii_hexdigit())
        || address.bytes().all(|b| b == b'0')
    {
        return Err("PAY_TO must be a nonzero EVM address".into());
    }
    let (network, asset, decimals, name, version) = match config.network.as_str() {
        "testnet" => (
            "eip155:2368",
            "0x38129cf4CE5E183eFF248F42A7D345Bb1B47621A",
            18,
            "pieUSD",
            "1",
        ),
        "mainnet" => (
            "eip155:2366",
            "0x7aB6f3ed87C42eF0aDb67Ed95090f8bF5240149e",
            6,
            "Bridged USDC (Kite AI)",
            "2",
        ),
        _ => return Err("KITE_NETWORK must be mainnet or testnet".into()),
    };
    let requirements = json!({"scheme":"exact","network":network,"asset":asset,"amount":amount(&config.price_usd, decimals)?,"payTo":config.pay_to.to_lowercase(),"maxTimeoutSeconds":60,"extra":{"name":name,"version":version}});
    let mut upstream = Url::parse(&config.upstream_url).map_err(|_| "Invalid UPSTREAM_URL")?;
    if !matches!(upstream.scheme(), "http" | "https")
        || upstream.host_str().is_none()
        || !upstream.username().is_empty()
        || upstream.password().is_some()
        || upstream.query().is_some()
        || upstream.fragment().is_some()
    {
        return Err(
            "UPSTREAM_URL must be an HTTP base URL without credentials, query or fragment".into(),
        );
    }
    upstream.set_path(&format!("{}/", upstream.path().trim_end_matches('/')));
    let facilitator = Url::parse(&config.facilitator_url).map_err(|_| "Invalid FACILITATOR_URL")?;
    if !matches!(facilitator.scheme(), "http" | "https") || facilitator.host_str().is_none() {
        return Err("Invalid FACILITATOR_URL".into());
    }
    if let Some((key, value)) = &config.upstream_auth {
        key.parse::<axum::http::HeaderName>()
            .map_err(|_| "Invalid UPSTREAM_AUTH_HEADER")?;
        value
            .parse::<HeaderValue>()
            .map_err(|_| "Invalid UPSTREAM_AUTH_VALUE")?;
        if blocked(key) {
            return Err("UPSTREAM_AUTH_HEADER is reserved".into());
        }
    }
    let state = AppState {
        config,
        requirements,
        upstream,
        client: Client::builder()
            .timeout(Duration::from_secs(20))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|e| e.to_string())?,
    };
    Ok(Router::new()
        .route("/healthz", get(health))
        .route("/v1/", any(proxy))
        .route("/v1/{*path}", any(proxy))
        .with_state(state))
}
async fn health(State(state): State<AppState>) -> impl IntoResponse {
    axum::Json(
        json!({"ok":true,"runtime":"rust/axum","network":state.requirements["network"],"price":state.config.price_usd}),
    )
}
fn encoded(value: &Value) -> String {
    STANDARD.encode(value.to_string())
}
fn error(status: StatusCode, reason: &str) -> Response {
    (status, axum::Json(json!({"error":reason}))).into_response()
}
fn challenge(state: &AppState, reason: Option<&str>) -> Response {
    let mut value = json!({"x402Version":2,"accepts":[state.requirements.clone()]});
    if let Some(reason) = reason {
        value["error"] = json!(reason);
    }
    let mut response = (StatusCode::PAYMENT_REQUIRED, axum::Json(value.clone())).into_response();
    response.headers_mut().insert(
        "payment-required",
        HeaderValue::from_str(&encoded(&value)).unwrap(),
    );
    response
}
fn blocked(key: &str) -> bool {
    matches!(
        key.to_ascii_lowercase().as_str(),
        "host"
            | "connection"
            | "keep-alive"
            | "proxy-authenticate"
            | "proxy-authorization"
            | "te"
            | "trailer"
            | "transfer-encoding"
            | "upgrade"
            | "content-length"
            | "payment-signature"
            | "payment-required"
            | "payment-response"
            | "x-payment"
    )
}
fn filtered(headers: &HeaderMap) -> HeaderMap {
    let connection: Vec<String> = headers
        .get_all("connection")
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| {
            v.split(',')
                .map(|s| s.trim().to_ascii_lowercase())
                .collect::<Vec<_>>()
        })
        .collect();
    headers
        .iter()
        .filter(|(k, _)| !blocked(k.as_str()) && !connection.iter().any(|v| v == k.as_str()))
        .map(|(k, v)| (k.clone(), v.clone()))
        .collect()
}
async fn facilitator(state: &AppState, action: &str, payload: &Value) -> Result<Value, ()> {
    let response = state.client.post(format!("{}/{}", state.config.facilitator_url.trim_end_matches('/'), action)).json(&json!({"x402Version":2,"paymentPayload":payload,"paymentRequirements":state.requirements})).send().await.map_err(|_| ())?;
    if !response.status().is_success() {
        return Err(());
    }
    response.json().await.map_err(|_| ())
}
async fn proxy(State(state): State<AppState>, request: Request) -> Response {
    let Some(signature) = request.headers().get("payment-signature") else {
        return challenge(&state, None);
    };
    let payload: Value = match signature
        .to_str()
        .ok()
        .and_then(|s| STANDARD.decode(s).ok())
        .and_then(|bytes| serde_json::from_slice(&bytes).ok())
    {
        Some(v) => v,
        None => return challenge(&state, Some("invalid_payment_payload")),
    };
    if payload["x402Version"] != 2
        || payload["accepted"] != state.requirements
        || !payload["payload"].is_object()
    {
        return challenge(&state, Some("invalid_payment_payload"));
    }
    let verified = match facilitator(&state, "verify", &payload).await {
        Ok(v) => v,
        Err(_) => return error(StatusCode::BAD_GATEWAY, "facilitator_unavailable"),
    };
    if verified["isValid"] != true {
        return challenge(&state, Some("payment_verification_failed"));
    }
    let suffix = request.uri().path().strip_prefix("/v1/").unwrap_or("");
    // Append to a fixed base path: a buyer cannot replace the upstream host.
    let mut target = state.upstream.clone();
    target.set_path(&format!("{}{}", state.upstream.path(), suffix));
    target.set_query(request.uri().query());
    let mut headers = filtered(request.headers());
    if let Some((key, value)) = &state.config.upstream_auth {
        headers.insert(
            key.parse::<axum::http::HeaderName>().unwrap(),
            value.parse().unwrap(),
        );
    }
    let method = request.method().clone();
    let body = match to_bytes(request.into_body(), 2 * 1024 * 1024).await {
        Ok(v) => v,
        Err(_) => return error(StatusCode::PAYLOAD_TOO_LARGE, "request_body_too_large"),
    };
    let upstream = match state
        .client
        .request(method, target)
        .headers(headers)
        .body(body)
        .send()
        .await
    {
        Ok(v) => v,
        Err(_) => return error(StatusCode::BAD_GATEWAY, "upstream_unavailable"),
    };
    let status = upstream.status();
    let response_headers = filtered(upstream.headers());
    let content = match upstream.bytes().await {
        Ok(v) => v,
        Err(_) => return error(StatusCode::BAD_GATEWAY, "upstream_unavailable"),
    };
    let mut response = Response::new(Body::from(content));
    *response.status_mut() = status;
    *response.headers_mut() = response_headers;
    if !status.is_success() {
        return response;
    }
    let settled = match facilitator(&state, "settle", &payload).await {
        Ok(v) => v,
        Err(_) => return error(StatusCode::BAD_GATEWAY, "settlement_unavailable"),
    };
    if settled["success"] != true
        || settled["network"] != state.requirements["network"]
        || settled["transaction"].as_str().is_none_or(|s| s.is_empty())
    {
        return challenge(&state, Some("settlement_failed"));
    }
    response.headers_mut().insert(
        "payment-response",
        HeaderValue::from_str(&encoded(&settled)).unwrap(),
    );
    response
}

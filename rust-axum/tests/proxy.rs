use axum::{
    Json, Router,
    body::{Body, to_bytes},
    extract::State,
    http::{Request, StatusCode},
    routing::{get, post},
};
use base64::{Engine, engine::general_purpose::STANDARD};
use kite_x402_axum::{Config, router};
use serde_json::{Value, json};
use std::sync::{Arc, Mutex};
use tower::ServiceExt;
type Calls = Arc<Mutex<Vec<&'static str>>>;
async fn start(app: Router) -> (String, tokio::task::JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}", listener.local_addr().unwrap());
    (
        url,
        tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        }),
    )
}
async fn exercise(
    upstream_status: StatusCode,
    valid: bool,
    settle: bool,
    paid: bool,
) -> (axum::response::Response, Vec<&'static str>) {
    let calls: Calls = Default::default();
    let verify = move |State(calls): State<Calls>, Json(body): Json<Value>| async move {
        assert_eq!(body["x402Version"], 2);
        assert_eq!(body["paymentRequirements"]["network"], "eip155:2368");
        calls.lock().unwrap().push("verify");
        Json(json!({"isValid":valid}))
    };
    let settlement = move |State(calls): State<Calls>| async move {
        calls.lock().unwrap().push("settle");
        Json(json!({"success":settle,"network":"eip155:2368","transaction":"0xmock"}))
    };
    let (facilitator, task1) = start(
        Router::new()
            .route("/verify", post(verify))
            .route("/settle", post(settlement))
            .with_state(calls.clone()),
    )
    .await;
    let (upstream, task2) = start(
        Router::new()
            .route(
                "/base/records",
                get(
                    move |State(calls): State<Calls>, req: Request<Body>| async move {
                        calls.lock().unwrap().push("upstream");
                        assert_eq!(req.uri().query(), Some("type=A"));
                        assert_eq!(req.headers()["authorization"], "Bearer server-secret");
                        assert!(!req.headers().contains_key("payment-signature"));
                        assert!(!req.headers().contains_key("x-hop"));
                        (upstream_status, Json(json!({"records":["203.0.113.7"]})))
                    },
                ),
            )
            .with_state(calls.clone()),
    )
    .await;
    let config = Config {
        pay_to: "0x1111111111111111111111111111111111111111".into(),
        upstream_url: format!("{upstream}/base"),
        network: "testnet".into(),
        price_usd: "0.001".into(),
        facilitator_url: facilitator,
        upstream_auth: Some(("authorization".into(), "Bearer server-secret".into())),
    };
    let app = router(config).unwrap();
    let challenge = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/v1/records?type=A")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(challenge.status(), StatusCode::PAYMENT_REQUIRED);
    let required: Value = serde_json::from_slice(
        &STANDARD
            .decode(challenge.headers()["payment-required"].to_str().unwrap())
            .unwrap(),
    )
    .unwrap();
    assert_eq!(required["accepts"][0]["amount"], "1000000000000000");
    assert!(calls.lock().unwrap().is_empty());
    let response = if paid {
        let signature=STANDARD.encode(json!({"x402Version":2,"accepted":required["accepts"][0],"payload":{"signature":"mock"}}).to_string());
        app.oneshot(
            Request::builder()
                .uri("/v1/records?type=A")
                .header("payment-signature", signature)
                .header("connection", "x-hop")
                .header("x-hop", "remove-me")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap()
    } else {
        challenge
    };
    task1.abort();
    task2.abort();
    let sequence = calls.lock().unwrap().clone();
    (response, sequence)
}
#[tokio::test]
async fn unpaid_402_does_not_call_services() {
    let (r, c) = exercise(StatusCode::OK, true, true, false).await;
    assert_eq!(r.status(), 402);
    assert!(c.is_empty());
}
#[tokio::test]
async fn paid_proxy_orders_verify_upstream_settle() {
    let (r, c) = exercise(StatusCode::OK, true, true, true).await;
    assert_eq!(r.status(), 200);
    assert!(r.headers().contains_key("payment-response"));
    assert_eq!(c, vec!["verify", "upstream", "settle"]);
    let body = to_bytes(r.into_body(), 1024).await.unwrap();
    assert_eq!(
        serde_json::from_slice::<Value>(&body).unwrap()["records"][0],
        "203.0.113.7"
    );
}
#[tokio::test]
async fn upstream_failure_does_not_settle() {
    let (r, c) = exercise(StatusCode::SERVICE_UNAVAILABLE, true, true, true).await;
    assert_eq!(r.status(), 503);
    assert_eq!(c, vec!["verify", "upstream"]);
}
#[tokio::test]
async fn invalid_payment_does_not_proxy() {
    let (r, c) = exercise(StatusCode::OK, false, true, true).await;
    assert_eq!(r.status(), 402);
    assert_eq!(c, vec!["verify"]);
}
#[tokio::test]
async fn failed_settlement_withholds_upstream_success() {
    let (r, c) = exercise(StatusCode::OK, true, false, true).await;
    assert_eq!(r.status(), 402);
    assert_eq!(c, vec!["verify", "upstream", "settle"]);
}
#[tokio::test]
async fn health_is_free_and_unknown_route_is_404() {
    let config = Config {
        pay_to: "0x1111111111111111111111111111111111111111".into(),
        upstream_url: "http://localhost:1".into(),
        network: "mainnet".into(),
        price_usd: "0.001".into(),
        facilitator_url: "http://localhost:2".into(),
        upstream_auth: None,
    };
    let app = router(config.clone()).unwrap();
    assert_eq!(
        app.clone()
            .oneshot(
                Request::builder()
                    .uri("/healthz")
                    .body(Body::empty())
                    .unwrap()
            )
            .await
            .unwrap()
            .status(),
        200
    );
    assert_eq!(
        app.oneshot(
            Request::builder()
                .uri("/other")
                .body(Body::empty())
                .unwrap()
        )
        .await
        .unwrap()
        .status(),
        404
    );
    let mut bad = config;
    bad.price_usd = "0.0000001".into();
    assert!(router(bad).is_err());
}

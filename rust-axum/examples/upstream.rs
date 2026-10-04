use axum::{Json, Router, routing::get};
#[tokio::main]
async fn main() {
    let app = Router::new().route(
        "/records",
        get(|| async { Json(serde_json::json!({"records":["203.0.113.7"]})) }),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:8081")
        .await
        .unwrap();
    axum::serve(listener, app).await.unwrap();
}

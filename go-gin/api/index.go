package handler

import (
	"net/http"
	"os"
	"sync"

	"github.com/Bystander2/kite-live-dns/go-gin/internal/livedns"
)

var (
	once    sync.Once
	router  http.Handler
	initErr error
)

func initialize() {
	payTo := os.Getenv("PAY_TO")
	if payTo == "" {
		payTo = "0xa5d1f687B741af9b2B7c2B0D77757C6a0De69055"
	}
	price := os.Getenv("PRICE_USD")
	if price == "" {
		price = "0.01"
	}
	config, err := livedns.NewConfig(payTo, price)
	if err != nil {
		initErr = err
		return
	}
	router = livedns.DefaultServer(config).Router()
}

func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(initialize)
	if initErr != nil {
		http.Error(w, "invalid service configuration", http.StatusInternalServerError)
		return
	}
	switch r.URL.Query().Get("route") {
	case "healthz":
		r.URL.Path = "/healthz"
	case "dns":
		r.URL.Path = "/v1/dns"
	}
	router.ServeHTTP(w, r)
}

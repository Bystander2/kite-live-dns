package main

import (
	"log"
	"os"

	"github.com/Bystander2/kite-live-dns/go-gin/internal/livedns"
)

func main() {
	payTo := os.Getenv("PAY_TO")
	if payTo == "" {
		payTo = "0xa5d1f687B741af9b2b7c2b0d77757C6a0De69055"
	}
	price := os.Getenv("PRICE_USD")
	if price == "" {
		price = "0.01"
	}
	config, err := livedns.NewConfig(payTo, price)
	if err != nil {
		log.Fatal(err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := livedns.DefaultServer(config).Router().Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"log"
	"os"

	"github.com/Bystander2/kite-live-dns/go-gin/internal/livedns"
)

func main() {
	price := os.Getenv("PRICE_USD")
	if price == "" {
		price = "0.01"
	}
	config, err := livedns.NewConfig(os.Getenv("PAY_TO"), price)
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

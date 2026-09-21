package main

import (
	"log"
	"os"
)

func main() {
	price := os.Getenv("PRICE_USD")
	if price == "" {
		price = "0.01"
	}
	config, err := NewConfig(os.Getenv("PAY_TO"), price)
	if err != nil {
		log.Fatal(err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := defaultServer(config).Router().Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

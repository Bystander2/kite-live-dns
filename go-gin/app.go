package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type Config struct {
	PayTo  string
	Price  string
	Amount *big.Int
}

type Server struct {
	Config  Config
	Resolve Resolver
	Verify  Verifier
	Settle  Settler
}

var addressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

func parsePrice(price string) (*big.Int, error) {
	parts := strings.Split(price, ".")
	if len(parts) > 2 || len(parts) == 0 || len(parts[0]) == 0 || len(parts) == 2 && len(parts[1]) > 18 {
		return nil, errors.New("invalid PRICE_USD")
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	text := parts[0] + frac + strings.Repeat("0", 18-len(frac))
	amount, ok := new(big.Int).SetString(text, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, errors.New("invalid PRICE_USD")
	}
	return amount, nil
}

func NewConfig(payTo, price string) (Config, error) {
	if !addressPattern.MatchString(payTo) || strings.EqualFold(payTo, "0x0000000000000000000000000000000000000000") {
		return Config{}, errors.New("PAY_TO must be a nonzero EVM address")
	}
	amount, err := parsePrice(price)
	if err != nil {
		return Config{}, err
	}
	return Config{PayTo: payTo, Price: price, Amount: amount}, nil
}

func encodeHeader(value any) string {
	raw, _ := json.Marshal(value)
	return base64.StdEncoding.EncodeToString(raw)
}

func (s Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Payment-Signature")
		c.Header("Access-Control-Expose-Headers", "Payment-Required, Payment-Response")
		c.Header("Cache-Control", "no-store")
		if c.Request.Method == http.MethodOptions && c.Request.URL.Path == "/v1/dns" {
			c.Status(http.StatusNoContent)
			c.Abort()
			return
		}
		c.Next()
	})
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "network": network, "price": s.Config.Price, "runtime": "go/gin"})
	})
	r.Any("/v1/dns", func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Header("Allow", "GET")
			c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method_not_allowed"})
			return
		}
		name, typ, err := parseQuery(c.Query("name"), c.Query("type"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_query", "message": err.Error()})
			return
		}
		accepted := gin.H{"scheme": "exact", "network": network, "amount": s.Config.Amount.String(), "asset": pyusdAddress, "payTo": s.Config.PayTo, "maxTimeoutSeconds": 60, "extra": gin.H{"name": "PYUSD", "version": "1"}}
		required := gin.H{"x402Version": 2, "error": "Payment required", "resource": gin.H{"url": requestURL(c.Request), "description": "Live public DNS records for a domain", "mimeType": "application/json"}, "accepts": []any{accepted}}
		encoded := c.GetHeader("Payment-Signature")
		if encoded == "" {
			c.Header("Payment-Required", encodeHeader(required))
			c.JSON(http.StatusPaymentRequired, gin.H{})
			return
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		var payload PaymentPayload
		if err != nil || json.Unmarshal(raw, &payload) != nil {
			paymentError(c, "invalid payment payload")
			return
		}
		auth, signature, err := s.Verify(c.Request.Context(), payload, s.Config)
		if err != nil {
			paymentError(c, err.Error())
			return
		}
		records, err := s.Resolve(c.Request.Context(), name, typ)
		if err != nil {
			if errors.Is(err, errNoRecords) {
				c.JSON(http.StatusNotFound, gin.H{"error": "dns_record_not_found"})
			} else {
				c.JSON(http.StatusBadGateway, gin.H{"error": "dns_lookup_failed"})
			}
			return
		}
		txHash, err := s.Settle(c.Request.Context(), auth, signature)
		if err != nil {
			paymentError(c, err.Error())
			return
		}
		c.Header("Payment-Response", encodeHeader(gin.H{"success": true, "payer": auth.From, "transaction": txHash, "network": network}))
		c.JSON(http.StatusOK, gin.H{"name": name, "type": typ, "records": records, "queried_at": time.Now().UTC().Format(time.RFC3339Nano)})
	})
	r.NoRoute(func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "not_found"}) })
	return r
}

func requestURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host + r.URL.RequestURI()
}
func paymentError(c *gin.Context, reason string) {
	c.Header("Payment-Response", encodeHeader(gin.H{"success": false, "errorReason": reason, "transaction": "", "network": network}))
	c.JSON(http.StatusPaymentRequired, gin.H{})
}

func defaultServer(config Config) Server {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return Server{Config: config, Resolve: googleResolver(client), Verify: realVerifier(client), Settle: realSettler(client)}
}

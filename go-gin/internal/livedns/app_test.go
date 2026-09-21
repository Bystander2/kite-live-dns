package livedns

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	c, err := NewConfig("0xa5d1f687B741af9b2B7c2B0D77757C6a0De69055", "0.01")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func paidHeader(t *testing.T) string {
	t.Helper()
	p := PaymentPayload{X402Version: 2}
	p.Payload.Signature = "0x" + string(make([]byte, 130))
	p.Payload.Authorization = Authorization{From: "0x1111111111111111111111111111111111111111", To: "0xa5d1f687B741af9b2B7c2B0D77757C6a0De69055", Value: "10000000000000000"}
	raw, _ := json.Marshal(p)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestUnpaidReturnsKite402WithoutUpstream(t *testing.T) {
	called := false
	s := Server{Config: testConfig(t), Resolve: func(context.Context, string, string) ([]DNSRecord, error) { called = true; return nil, nil }, Verify: nil, Settle: nil}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/dns?name=example.com&type=A", nil)
	s.Router().ServeHTTP(w, req)
	if w.Code != http.StatusPaymentRequired || called {
		t.Fatalf("status=%d called=%v", w.Code, called)
	}
	raw, err := base64.StdEncoding.DecodeString(w.Header().Get("Payment-Required"))
	if err != nil {
		t.Fatal(err)
	}
	var challenge struct {
		Accepts []struct{ Network, Amount, Asset, PayTo string } `json:"accepts"`
	}
	if json.Unmarshal(raw, &challenge) != nil || len(challenge.Accepts) != 1 {
		t.Fatal("invalid challenge")
	}
	a := challenge.Accepts[0]
	if a.Network != network || a.Amount != "10000000000000000" || a.Asset != pyusdAddress || a.PayTo != s.Config.PayTo {
		t.Fatalf("wrong challenge: %+v", a)
	}
}

func TestPaidOrderIsVerifyUpstreamSettle(t *testing.T) {
	order := []string{}
	s := Server{Config: testConfig(t),
		Verify: func(context.Context, PaymentPayload, Config) (Authorization, []byte, error) {
			order = append(order, "verify")
			return Authorization{From: "0x1111111111111111111111111111111111111111"}, make([]byte, 65), nil
		},
		Resolve: func(context.Context, string, string) ([]DNSRecord, error) {
			order = append(order, "upstream")
			return []DNSRecord{{Name: "example.com.", Type: 1, TTL: 60, Data: "104.20.23.154"}}, nil
		},
		Settle: func(context.Context, Authorization, []byte) (string, error) {
			order = append(order, "settle")
			return "0xabc", nil
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/dns?name=example.com&type=A", nil)
	req.Header.Set("Payment-Signature", paidHeader(t))
	s.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !reflect.DeepEqual(order, []string{"verify", "upstream", "settle"}) {
		t.Fatalf("status=%d order=%v", w.Code, order)
	}
	if w.Header().Get("Payment-Response") == "" {
		t.Fatal("missing settlement header")
	}
}

func TestUpstreamFailureDoesNotSettle(t *testing.T) {
	settled := false
	s := Server{Config: testConfig(t),
		Verify: func(context.Context, PaymentPayload, Config) (Authorization, []byte, error) {
			return Authorization{}, make([]byte, 65), nil
		},
		Resolve: func(context.Context, string, string) ([]DNSRecord, error) { return nil, errors.New("upstream down") },
		Settle:  func(context.Context, Authorization, []byte) (string, error) { settled = true; return "", nil },
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/dns?name=example.com", nil)
	req.Header.Set("Payment-Signature", paidHeader(t))
	s.Router().ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway || settled {
		t.Fatalf("status=%d settled=%v", w.Code, settled)
	}
}

func TestInvalidRequestsNeverVerify(t *testing.T) {
	verified := false
	s := Server{Config: testConfig(t), Verify: func(context.Context, PaymentPayload, Config) (Authorization, []byte, error) {
		verified = true
		return Authorization{}, nil, nil
	}}
	for _, target := range []string{"/v1/dns?name=https://example.com", "/v1/dns?name=127.0.0.1", "/v1/dns?name=example.com&type=BOGUS"} {
		w := httptest.NewRecorder()
		s.Router().ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", target, w.Code)
		}
	}
	if verified {
		t.Fatal("invalid request reached verifier")
	}
}

func TestRecoversEIP3009Signer(t *testing.T) {
	key, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	auth := Authorization{
		From:        "0x" + hex.EncodeToString(keccak(key.PubKey().SerializeUncompressed()[1:])[12:]),
		To:          "0xa5d1f687B741af9b2B7c2B0D77757C6a0De69055",
		Value:       "10000000000000000",
		ValidAfter:  strconv.FormatInt(now-30, 10),
		ValidBefore: strconv.FormatInt(now+30, 10),
		Nonce:       "0x1111111111111111111111111111111111111111111111111111111111111111",
	}
	hash, err := eip712Hash(auth)
	if err != nil {
		t.Fatal(err)
	}
	compact := ecdsa.SignCompact(key, hash, false)
	signature := append(append([]byte{}, compact[1:]...), compact[0]-27)
	recovered, err := recoverAddress(auth, signature)
	if err != nil || !strings.EqualFold(recovered, auth.From) {
		t.Fatalf("recovered=%s want=%s err=%v", recovered, auth.From, err)
	}
}

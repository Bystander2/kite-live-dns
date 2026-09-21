package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

const network = "eip155:2368"
const chainID = 2368
const pyusdAddress = "0x8E04D099b1a8Dd20E6caD4b2Ab2B405B98242ec9"
const rpcURL = "https://rpc-testnet.gokite.ai"
const gaslessURL = "https://gasless.gokite.ai/testnet"

type Authorization struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
}

type PaymentPayload struct {
	X402Version int `json:"x402Version"`
	Payload     struct {
		Signature     string        `json:"signature"`
		Authorization Authorization `json:"authorization"`
	} `json:"payload"`
}

type Verifier func(context.Context, PaymentPayload, Config) (Authorization, []byte, error)
type Settler func(context.Context, Authorization, []byte) (string, error)

func parseUint(text string) (*big.Int, error) {
	value, ok := new(big.Int).SetString(text, 10)
	if !ok || value.Sign() < 0 {
		return nil, errors.New("invalid integer")
	}
	return value, nil
}

func decodeSignature(text string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(text, "0x"))
	if err != nil || len(raw) != 65 {
		return nil, errors.New("invalid signature")
	}
	if raw[64] >= 27 {
		raw[64] -= 27
	}
	if raw[64] > 1 {
		return nil, errors.New("invalid recovery id")
	}
	return raw, nil
}

func keccak(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, part := range parts {
		_, _ = h.Write(part)
	}
	return h.Sum(nil)
}

func uint256(value string) ([]byte, error) {
	n, err := parseUint(value)
	if err != nil || n.BitLen() > 256 {
		return nil, errors.New("invalid uint256")
	}
	return n.FillBytes(make([]byte, 32)), nil
}

func addressWord(value string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(raw) != 20 {
		return nil, errors.New("invalid address")
	}
	word := make([]byte, 32)
	copy(word[12:], raw)
	return word, nil
}

func bytes32(value string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(raw) != 32 {
		return nil, errors.New("invalid bytes32")
	}
	return raw, nil
}

func eip712Hash(auth Authorization) ([]byte, error) {
	contract, err := addressWord(pyusdAddress)
	if err != nil {
		return nil, err
	}
	chain := big.NewInt(chainID).FillBytes(make([]byte, 32))
	domainType := keccak([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	domain := keccak(domainType, keccak([]byte("PYUSD")), keccak([]byte("1")), chain, contract)
	from, err := addressWord(auth.From)
	if err != nil {
		return nil, err
	}
	to, err := addressWord(auth.To)
	if err != nil {
		return nil, err
	}
	value, err := uint256(auth.Value)
	if err != nil {
		return nil, err
	}
	after, err := uint256(auth.ValidAfter)
	if err != nil {
		return nil, err
	}
	before, err := uint256(auth.ValidBefore)
	if err != nil {
		return nil, err
	}
	nonce, err := bytes32(auth.Nonce)
	if err != nil {
		return nil, err
	}
	transferType := keccak([]byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"))
	message := keccak(transferType, from, to, value, after, before, nonce)
	return keccak([]byte{0x19, 0x01}, domain, message), nil
}

func recoverAddress(auth Authorization, signature []byte) (string, error) {
	hash, err := eip712Hash(auth)
	if err != nil {
		return "", err
	}
	compact := make([]byte, 65)
	compact[0] = 27 + signature[64]
	copy(compact[1:], signature[:64])
	publicKey, _, err := ecdsa.RecoverCompact(compact, hash)
	if err != nil {
		return "", err
	}
	address := keccak(publicKey.SerializeUncompressed()[1:])[12:]
	return "0x" + hex.EncodeToString(address), nil
}

func realVerifier(client *http.Client) Verifier {
	return func(ctx context.Context, payload PaymentPayload, config Config) (Authorization, []byte, error) {
		auth := payload.Payload.Authorization
		if payload.X402Version != 2 || !strings.EqualFold(auth.To, config.PayTo) {
			return auth, nil, errors.New("invalid payment payload")
		}
		value, err := parseUint(auth.Value)
		if err != nil || value.Cmp(config.Amount) < 0 {
			return auth, nil, errors.New("payment amount is too small")
		}
		signature, err := decodeSignature(payload.Payload.Signature)
		if err != nil {
			return auth, nil, err
		}
		signer, err := recoverAddress(auth, signature)
		if err != nil || !strings.EqualFold(signer, auth.From) {
			return auth, nil, errors.New("invalid payment signer")
		}
		now, err := latestBlockTime(ctx, client)
		if err != nil {
			return auth, nil, err
		}
		after, err1 := parseUint(auth.ValidAfter)
		before, err2 := parseUint(auth.ValidBefore)
		if err1 != nil || err2 != nil || now.Cmp(after) <= 0 || now.Cmp(before) >= 0 {
			return auth, nil, errors.New("authorization is not currently valid")
		}
		return auth, signature, nil
	}
}

func rpc(ctx context.Context, client *http.Client, method string, params []any, result any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Error != nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return errors.New("RPC returned no result")
	}
	return json.Unmarshal(envelope.Result, result)
}

func latestBlockTime(ctx context.Context, client *http.Client) (*big.Int, error) {
	var block struct {
		Timestamp string `json:"timestamp"`
	}
	if err := rpc(ctx, client, "eth_getBlockByNumber", []any{"latest", false}, &block); err != nil {
		return nil, err
	}
	value, ok := new(big.Int).SetString(strings.TrimPrefix(block.Timestamp, "0x"), 16)
	if !ok {
		return nil, errors.New("invalid block timestamp")
	}
	return value, nil
}

func realSettler(client *http.Client) Settler {
	return func(ctx context.Context, auth Authorization, signature []byte) (string, error) {
		v := int(signature[64] + 27)
		body, _ := json.Marshal(map[string]any{"from": auth.From, "to": auth.To, "value": auth.Value, "validAfter": auth.ValidAfter, "validBefore": auth.ValidBefore, "nonce": auth.Nonce, "tokenAddress": pyusdAddress, "v": v, "r": "0x" + hex.EncodeToString(signature[:32]), "s": "0x" + hex.EncodeToString(signature[32:64])})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, gaslessURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var result struct {
			TxHash string `json:"txHash"`
			Detail string `json:"detail"`
			Error  string `json:"error"`
		}
		if json.Unmarshal(raw, &result) != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || result.TxHash == "" {
			return "", fmt.Errorf("settlement failed: %s %s", result.Detail, result.Error)
		}
		for i := 0; i < 20; i++ {
			var receipt struct {
				Status string `json:"status"`
			}
			err := rpc(ctx, client, "eth_getTransactionReceipt", []any{result.TxHash}, &receipt)
			if err == nil {
				if receipt.Status != "0x1" {
					return "", errors.New("transaction reverted")
				}
				return result.TxHash, nil
			}
			time.Sleep(500 * time.Millisecond)
		}
		return "", errors.New("transaction unconfirmed")
	}
}

package livedns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

var recordTypes = map[string]bool{"A": true, "AAAA": true, "MX": true, "TXT": true, "NS": true, "CNAME": true, "SOA": true, "CAA": true}
var labelPattern = regexp.MustCompile(`^[a-z0-9_](?:[a-z0-9_-]{0,61}[a-z0-9_])?$`)

var errNoRecords = errors.New("dns record not found")

type DNSRecord struct {
	Name string `json:"name"`
	Type int    `json:"type"`
	TTL  int    `json:"ttl"`
	Data string `json:"data"`
}

type Resolver func(context.Context, string, string) ([]DNSRecord, error)

func parseQuery(rawName, rawType string) (string, string, error) {
	rawName = strings.TrimSuffix(strings.TrimSpace(rawName), ".")
	if rawName == "" || strings.ContainsAny(rawName, " /\\?#@:%") {
		return "", "", errors.New("name must be a domain")
	}
	name, err := idna.Lookup.ToASCII(rawName)
	if err != nil {
		return "", "", errors.New("name must be a valid domain")
	}
	name = strings.ToLower(name)
	if len(name) > 253 || net.ParseIP(name) != nil || !strings.Contains(name, ".") {
		return "", "", errors.New("name must be a fully qualified domain")
	}
	for _, label := range strings.Split(name, ".") {
		if !labelPattern.MatchString(label) {
			return "", "", errors.New("name must be a valid fully qualified domain")
		}
	}
	typ := strings.ToUpper(rawType)
	if typ == "" {
		typ = "A"
	}
	if !recordTypes[typ] {
		return "", "", errors.New("unsupported DNS record type")
	}
	return name, typ, nil
}

func googleResolver(client *http.Client) Resolver {
	return func(ctx context.Context, name, typ string) ([]DNSRecord, error) {
		u, _ := url.Parse("https://dns.google/resolve")
		q := u.Query()
		q.Set("name", name)
		q.Set("type", typ)
		q.Set("edns_client_subnet", "0.0.0.0/0")
		u.RawQuery = q.Encode()
		requestCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
		req.Header.Set("Accept", "application/dns-json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("DNS upstream HTTP %d", resp.StatusCode)
		}
		var body struct {
			Status int         `json:"Status"`
			TC     bool        `json:"TC"`
			Answer []DNSRecord `json:"Answer"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, err
		}
		if body.Status == 3 || (body.Status == 0 && len(body.Answer) == 0) {
			return nil, errNoRecords
		}
		if body.Status != 0 || body.TC {
			return nil, errors.New("DNS upstream failed or truncated")
		}
		for _, record := range body.Answer {
			if record.Name == "" || record.Type <= 0 || record.TTL < 0 || record.Data == "" {
				return nil, errors.New("invalid DNS upstream response")
			}
		}
		return body.Answer, nil
	}
}

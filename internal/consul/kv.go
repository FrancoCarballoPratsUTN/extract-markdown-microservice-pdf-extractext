package consul

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type KVClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type kvEntry struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func NewKVClient(baseURL, token string, client *http.Client) *KVClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &KVClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		client:  client,
	}
}

func (c *KVClient) LoadPrefix(prefix string) (map[string]string, error) {
	if strings.TrimSpace(prefix) == "" {
		return nil, fmt.Errorf("consul KV prefix must not be empty")
	}

	endpoint := c.baseURL + "/v1/kv/" + strings.TrimLeft(prefix, "/")
	request, err := http.NewRequest(http.MethodGet, endpoint+"?"+url.Values{"recurse": {"true"}}.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Consul KV request: %w", err)
	}
	if c.token != "" {
		request.Header.Set("X-Consul-Token", c.token)
	}

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read Consul KV prefix: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("read Consul KV prefix: unexpected status %s", response.Status)
	}

	var entries []kvEntry
	if err := json.NewDecoder(response.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode Consul KV response: %w", err)
	}

	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key := strings.TrimPrefix(entry.Key, strings.TrimRight(prefix, "/")+"/")
		if key == entry.Key || key == "" {
			return nil, fmt.Errorf("Consul KV response contains key outside prefix: %q", entry.Key)
		}
		value, err := base64.StdEncoding.DecodeString(entry.Value)
		if err != nil {
			return nil, fmt.Errorf("decode Consul KV value for %q: %w", key, err)
		}
		values[key] = string(value)
	}
	return values, nil
}

package consul

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const requestTimeout = 5 * time.Second

// KVClient es un cliente mínimo de la API HTTP de Consul para leer el KV y
// gestionar el registro del servicio.
type KVClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type kvEntry struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// NewKVClient construye un cliente de Consul. Si client es nil, usa un cliente
// HTTP con timeout para que un Consul colgado no bloquee el arranque ni el
// apagado.
func NewKVClient(baseURL, token string, client *http.Client) *KVClient {
	if client == nil {
		// Sin deadline, un Consul colgado puede bloquear el arranque o el
		// apagado indefinidamente. El presupuesto es por request.
		client = &http.Client{Timeout: requestTimeout}
	}

	return &KVClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		client:  client,
	}
}

// NewKVClientWithCA construye un cliente que confía en la CA indicada para las
// conexiones TLS con Consul. Una ruta vacía usa el cliente por defecto.
func NewKVClientWithCA(baseURL, token, caFile string) (*KVClient, error) {
	if caFile == "" {
		return NewKVClient(baseURL, token, nil), nil
	}
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read Consul CA file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("parse Consul CA file")
	}
	return NewKVClient(baseURL, token, &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		},
	}), nil
}

// LoadPrefix lee de forma recursiva todas las claves bajo prefix y devuelve un
// mapa con las claves relativas al prefijo.
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

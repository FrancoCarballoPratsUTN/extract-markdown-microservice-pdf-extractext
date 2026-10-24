package consul

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type ServiceRegistration struct {
	ID      string
	Name    string
	Address string
	Port    int
	Check   string
	Tags    []string
}

func (c *KVClient) RegisterService(registration ServiceRegistration) error {
	if registration.ID == "" || registration.Name == "" || registration.Address == "" ||
		registration.Port < 1 || registration.Check == "" {
		return fmt.Errorf("invalid Consul service registration")
	}
	payload := map[string]any{
		"ID": registration.ID, "Name": registration.Name, "Address": registration.Address,
		"Port": registration.Port, "Tags": registration.Tags,
		"Check": map[string]any{
			"HTTP": registration.Check, "Interval": "10s", "Timeout": "3s",
			"DeregisterCriticalServiceAfter": "1m",
		},
	}
	return c.agentRequest(http.MethodPut, "/v1/agent/service/register", payload)
}

func (c *KVClient) DeregisterService(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("Consul service ID must not be empty")
	}
	return c.agentRequest(http.MethodPut, "/v1/agent/service/deregister/"+id, nil)
}

func (c *KVClient) agentRequest(method, path string, payload any) error {
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode Consul service request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create Consul service request: %w", err)
	}
	if c.token != "" {
		request.Header.Set("X-Consul-Token", c.token)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("Consul service request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("Consul service request: unexpected status %s", response.Status)
	}
	return nil
}

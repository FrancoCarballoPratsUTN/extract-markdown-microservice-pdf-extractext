package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const defaultConsulServiceName = "extract"

// ConsulConfig es la configuración de Consul y del registro del servicio.
// Las variables CONSUL_* se leen siempre del entorno del proceso, incluso
// cuando el resto de la configuración viene del KV: son el bootstrap de esa
// conexión. Los defaults que dependen del puerto o del hostname los aplica
// ServiceRegistration, no la lectura.
type ConsulConfig struct {
	Enabled    bool
	HTTPAddr   string
	HTTPToken  string
	HTTPCAFile string
	KVPrefix   string

	ServiceName     string
	ServiceID       string
	ServiceAddress  string
	ServiceCheckURL string
	ServiceTags     []string
}

// ConsulServiceRegistration es el registro con los defaults ya resueltos,
// listo para que el composition root lo traduzca al cliente de Consul sin
// volver a leer el entorno.
type ConsulServiceRegistration struct {
	ID      string
	Name    string
	Address string
	Port    int
	Check   string
	Tags    []string
}

// LoadConsul lee las variables CONSUL_* del entorno. Consul es opcional:
// deshabilitado no valida nada; habilitado exige la conexión al KV completa.
func LoadConsul() (ConsulConfig, error) {
	return loadConsul(os.Getenv)
}

func loadConsul(getenv func(string) string) (ConsulConfig, error) {
	if !strings.EqualFold(getenv("CONSUL_ENABLED"), "true") {
		return ConsulConfig{}, nil
	}

	cfg := ConsulConfig{
		Enabled:         true,
		HTTPAddr:        strings.TrimSpace(getenv("CONSUL_HTTP_ADDR")),
		HTTPToken:       strings.TrimSpace(getenv("CONSUL_HTTP_TOKEN")),
		HTTPCAFile:      getenv("CONSUL_HTTP_CA_FILE"),
		KVPrefix:        strings.TrimSpace(getenv("CONSUL_KV_PREFIX")),
		ServiceName:     getenv("CONSUL_SERVICE_NAME"),
		ServiceID:       strings.TrimSpace(getenv("CONSUL_SERVICE_ID")),
		ServiceAddress:  strings.TrimSpace(getenv("CONSUL_SERVICE_ADDRESS")),
		ServiceCheckURL: getenv("CONSUL_SERVICE_CHECK_URL"),
		ServiceTags:     splitConsulTags(getenv("CONSUL_SERVICE_TAGS")),
	}
	if cfg.HTTPAddr == "" || cfg.HTTPToken == "" || cfg.KVPrefix == "" {
		return ConsulConfig{}, fmt.Errorf("CONSUL_HTTP_ADDR, CONSUL_HTTP_TOKEN and CONSUL_KV_PREFIX are required when CONSUL_ENABLED=true")
	}
	return cfg, nil
}

// ServiceRegistration resuelve el registro del servicio aplicando los
// defaults que dependen del puerto y del hostname. Sólo tiene sentido con
// Consul habilitado; el composition root decide si el registro se realiza.
func (c ConsulConfig) ServiceRegistration(port string) (ConsulServiceRegistration, error) {
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		return ConsulServiceRegistration{}, fmt.Errorf("invalid service port %q: %w", port, err)
	}

	address := c.ServiceAddress
	if address == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return ConsulServiceRegistration{}, fmt.Errorf("resolve service hostname: %w", err)
		}
		address = hostname
	}

	name := c.ServiceName
	if name == "" {
		name = defaultConsulServiceName
	}
	id := c.ServiceID
	if id == "" {
		id = name + "-" + address
	}
	check := c.ServiceCheckURL
	if check == "" {
		check = "http://" + address + ":" + port + "/healthz"
	}

	return ConsulServiceRegistration{
		ID: id, Name: name, Address: address, Port: portNumber, Check: check, Tags: c.ServiceTags,
	}, nil
}

// splitConsulTags separa los tags por ';'. Las comas permanecen dentro de un
// tag porque las cadenas de middlewares de Traefik las usan internamente.
func splitConsulTags(raw string) []string {
	var tags []string
	for _, tag := range strings.Split(raw, ";") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

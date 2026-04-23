package config

import (
	"fmt"
	"strings"
	"time"
)

type OutputFlag []string

func (f *OutputFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *OutputFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty output specification")
	}

	*f = append(*f, value)
	return nil
}

type Endpoint struct {
	Addr       string
	StreamID   string
	Passphrase string
}

type Config struct {
	Input        Endpoint
	Outputs      []Endpoint
	BufferSize   int
	WriteTimeout time.Duration
}

func ParseOutputs(values []string) ([]Endpoint, error) {
	outputs := make([]Endpoint, 0, len(values))

	for _, value := range values {
		parts := strings.Split(value, ",")
		if len(parts) == 0 || len(parts) > 3 {
			return nil, fmt.Errorf("%q must be addr[,streamid[,passphrase]]", value)
		}

		addr := strings.TrimSpace(parts[0])
		if addr == "" {
			return nil, fmt.Errorf("%q has empty address", value)
		}

		endpoint := Endpoint{Addr: addr}
		if len(parts) > 1 {
			endpoint.StreamID = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			endpoint.Passphrase = strings.TrimSpace(parts[2])
		}

		outputs = append(outputs, endpoint)
	}

	return outputs, nil
}

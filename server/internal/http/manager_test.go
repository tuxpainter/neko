package http

import (
	"slices"
	"testing"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/internal/media"
)

func TestMediaWebTransportRequiresTLS(t *testing.T) {
	tests := []struct {
		name    string
		cert    string
		key     string
		enabled bool
	}{
		{name: "no TLS"},
		{name: "certificate only", cert: "cert.pem"},
		{name: "key only", key: "key.pem"},
		{name: "certificate and key", cert: "cert.pem", key: "key.pem", enabled: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newMediaWebTransport(
				&config.Server{Bind: "127.0.0.1:8080", Cert: test.cert, Key: test.key},
				&media.Manager{},
			)
			if got := server != nil; got != test.enabled {
				t.Fatalf("enabled = %t, want %t", got, test.enabled)
			}
			if test.enabled && !slices.Contains(server.H3.TLSConfig.NextProtos, "h3") {
				t.Fatalf("HTTP/3 ALPN is not configured: %v", server.H3.TLSConfig.NextProtos)
			}
		})
	}
}

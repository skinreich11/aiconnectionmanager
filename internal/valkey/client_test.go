package valkey

import "testing"

func TestNewClientRequiresURL(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("NewClient should require a Valkey URL")
	}
}

func TestNewClientAcceptsRedisAndTLSURLs(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
	}{
		{name: "local", url: "redis://valkey:6379"},
		{name: "elasticache TLS", url: "rediss://cache.example:6379"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(Config{URL: test.url, Password: "secret"})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if err := client.Close(); err != nil {
				t.Fatalf("close client: %v", err)
			}
		})
	}
}

func TestNewClientRejectsUnsupportedURL(t *testing.T) {
	if _, err := NewClient(Config{URL: "http://valkey:6379"}); err == nil {
		t.Fatal("NewClient should reject an HTTP URL")
	}
}

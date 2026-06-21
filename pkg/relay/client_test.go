package relay

import (
	"testing"
)

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost:9999")

	if c.relayURL != "http://localhost:9999" {
		t.Fatalf("expected URL 'http://localhost:9999', got '%s'", c.relayURL)
	}

	if c.IsConnected() {
		t.Fatal("new client should not be connected")
	}
}

func TestNewClient_TrailingSlash(t *testing.T) {
	c := NewClient("http://localhost:9999/")

	if c.relayURL != "http://localhost:9999" {
		t.Fatalf("expected trailing slash to be stripped, got '%s'", c.relayURL)
	}
}

func TestClient_SendWithoutConnection(t *testing.T) {
	c := NewClient("http://localhost:9999")

	err := c.SendRaw(nil, []byte("test"))
	if err == nil {
		t.Fatal("expected error when sending without connection")
	}
}

func TestClient_ReceiveWithoutConnection(t *testing.T) {
	c := NewClient("http://localhost:9999")

	_, err := c.ReceiveRaw(nil)
	if err == nil {
		t.Fatal("expected error when receiving without connection")
	}
}

func TestClient_RegisterWithoutAuth(t *testing.T) {
	c := NewClient("http://localhost:9999")

	err := c.RegisterToken(nil, "test-token-01")
	if err == nil {
		t.Fatal("expected error when registering without authentication")
	}
}

func TestClient_CloseWithoutConnection(t *testing.T) {
	c := NewClient("http://localhost:9999")

	// Should not error
	err := c.Close()
	if err != nil {
		t.Fatalf("Close on unconnected client should not error: %v", err)
	}
}

func TestClient_RelayURL(t *testing.T) {
	c := NewClient("https://relay.hop.to")

	if c.RelayURL() != "https://relay.hop.to" {
		t.Fatalf("expected 'https://relay.hop.to', got '%s'", c.RelayURL())
	}
}

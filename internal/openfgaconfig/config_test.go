package openfgaconfig

import (
	"testing"

	"github.com/openfga/go-sdk/credentials"
)

func TestNewConfiguresAPITokenCredentials(t *testing.T) {
	config := New("https://openfga.example.test", "token-1", "store-1")

	if config.ApiUrl != "https://openfga.example.test" {
		t.Fatalf("ApiUrl = %q, want %q", config.ApiUrl, "https://openfga.example.test")
	}
	if config.StoreId != "store-1" {
		t.Fatalf("StoreId = %q, want %q", config.StoreId, "store-1")
	}
	if config.Credentials == nil {
		t.Fatal("Credentials = nil, want API token credentials")
	}
	if config.Credentials.Method != credentials.CredentialsMethodApiToken {
		t.Fatalf("Credentials.Method = %q, want %q", config.Credentials.Method, credentials.CredentialsMethodApiToken)
	}
	if config.Credentials.Config.ApiToken != "token-1" {
		t.Fatalf("ApiToken = %q, want %q", config.Credentials.Config.ApiToken, "token-1")
	}
}

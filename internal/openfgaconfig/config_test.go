package openfgaconfig

import (
	"encoding/json"
	"testing"

	openfgaclient "github.com/openfga/go-sdk/client"
	"github.com/openfga/go-sdk/credentials"
)

func TestNewConfiguresAPITokenCredentials(t *testing.T) {
	config := New("https://openfga.example.test", "token-1", "store-1", "model-1")

	if config.ApiUrl != "https://openfga.example.test" {
		t.Fatalf("ApiUrl = %q, want %q", config.ApiUrl, "https://openfga.example.test")
	}
	if config.StoreId != "store-1" {
		t.Fatalf("StoreId = %q, want %q", config.StoreId, "store-1")
	}
	if config.AuthorizationModelId != "model-1" {
		t.Fatalf("AuthorizationModelId = %q, want %q", config.AuthorizationModelId, "model-1")
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

func TestMarshalRoundTripsIntoClientConfiguration(t *testing.T) {
	data, err := Marshal(New("https://openfga.example.test", "token-1", "store-1", "model-1"))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var config openfgaclient.ClientConfiguration
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if config.ApiUrl != "https://openfga.example.test" {
		t.Fatalf("ApiUrl = %q, want %q", config.ApiUrl, "https://openfga.example.test")
	}
	if config.StoreId != "store-1" {
		t.Fatalf("StoreId = %q, want %q", config.StoreId, "store-1")
	}
	if config.AuthorizationModelId != "model-1" {
		t.Fatalf("AuthorizationModelId = %q, want %q", config.AuthorizationModelId, "model-1")
	}
	if config.Credentials == nil || config.Credentials.Config == nil {
		t.Fatal("Credentials = nil, want API token credentials")
	}
	if config.Credentials.Method != credentials.CredentialsMethodApiToken {
		t.Fatalf("Credentials.Method = %q, want %q", config.Credentials.Method, credentials.CredentialsMethodApiToken)
	}
	if config.Credentials.Config.ApiToken != "token-1" {
		t.Fatalf("ApiToken = %q, want %q", config.Credentials.Config.ApiToken, "token-1")
	}
}

func TestMarshalOmitsCredentialsWithoutToken(t *testing.T) {
	data, err := Marshal(New("https://openfga.example.test", "", "store-1", "model-1"))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var config openfgaclient.ClientConfiguration
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if config.Credentials != nil {
		t.Fatalf("Credentials = %#v, want nil", config.Credentials)
	}
}

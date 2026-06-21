package openfgaconfig

import (
	"encoding/json"
	"fmt"

	openfgaclient "github.com/openfga/go-sdk/client"
	"github.com/openfga/go-sdk/credentials"
)

func New(apiURL, apiToken, storeID, authorizationModelID string) *openfgaclient.ClientConfiguration {
	config := &openfgaclient.ClientConfiguration{
		ApiUrl:               apiURL,
		StoreId:              storeID,
		AuthorizationModelId: authorizationModelID,
	}
	if apiToken != "" {
		config.Credentials = &credentials.Credentials{
			Method: credentials.CredentialsMethodApiToken,
			Config: &credentials.Config{ApiToken: apiToken},
		}
	}
	return config
}

func Marshal(config *openfgaclient.ClientConfiguration) ([]byte, error) {
	if config == nil {
		return nil, fmt.Errorf("client configuration is required")
	}

	var creds *credentialsJSON
	if config.Credentials != nil {
		creds = &credentialsJSON{
			Method: config.Credentials.Method,
			Config: config.Credentials.Config,
		}
	}

	return json.MarshalIndent(clientConfigurationJSON{
		ApiURL:               config.ApiUrl,
		StoreID:              config.StoreId,
		AuthorizationModelID: config.AuthorizationModelId,
		Credentials:          creds,
		DefaultHeaders:       config.DefaultHeaders,
		UserAgent:            config.UserAgent,
		Debug:                config.Debug,
	}, "", "  ")
}

type clientConfigurationJSON struct {
	ApiURL               string            `json:"api_url"`
	StoreID              string            `json:"store_id"`
	AuthorizationModelID string            `json:"authorization_model_id"`
	Credentials          *credentialsJSON  `json:"credentials,omitempty"`
	DefaultHeaders       map[string]string `json:"default_headers,omitempty"`
	UserAgent            string            `json:"user_agent,omitempty"`
	Debug                bool              `json:"debug,omitempty"`
}

type credentialsJSON struct {
	Method credentials.CredentialsMethod `json:"method,omitempty"`
	Config *credentials.Config           `json:"config,omitempty"`
}

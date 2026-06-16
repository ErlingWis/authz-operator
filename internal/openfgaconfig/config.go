package openfgaconfig

import (
	openfgaclient "github.com/openfga/go-sdk/client"
	"github.com/openfga/go-sdk/credentials"
)

func New(apiURL, apiToken, storeID string) *openfgaclient.ClientConfiguration {
	config := &openfgaclient.ClientConfiguration{
		ApiUrl:  apiURL,
		StoreId: storeID,
	}
	if apiToken != "" {
		config.Credentials = &credentials.Credentials{
			Method: credentials.CredentialsMethodApiToken,
			Config: &credentials.Config{ApiToken: apiToken},
		}
	}
	return config
}

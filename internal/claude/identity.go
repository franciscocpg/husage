package claude

import (
	"encoding/json"
	"errors"
	"os"
)

type identity struct {
	AccountID string `json:"accountUuid"`
	OrgID     string `json:"organizationUuid"`
	Name      string `json:"organizationName"`
	Email     string `json:"emailAddress"`
}

func (i identity) key() string { return i.AccountID + ":" + i.OrgID }
func (i identity) valid() bool { return i.AccountID != "" && i.OrgID != "" }

func readIdentity(path string) (identity, error) {
	var config struct {
		Account identity `json:"oauthAccount"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return identity{}, err
	}
	if err := json.Unmarshal(b, &config); err != nil {
		return identity{}, errors.New("could not parse Claude account metadata")
	}
	return config.Account, nil
}

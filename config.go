// Config and token-cache handling for the ft CLI.
//
// ~/.ft_cli_creds (KEY=VALUE lines) holds your own account details. Never
// commit this file or place real credentials in the repository:
//
//	FT_API_BASE=https://your-api.example.com
//	FT_SUBDOMAIN=your-org
//	FT_EMAIL=you@example.com
//	FT_PASSWORD=...                        # optional; if absent, `ft login` prompts (no echo)
//	FT_COGNITO_CLIENT_ID=...               # required for login
//	FT_COGNITO_REGION=...                  # required for login
//	FT_COGNITO_POOL_ID=...                 # required for login
//
// Tokens from `ft login` are cached in ~/.ft_cli_token.json and refreshed
// automatically, so you only type your password when the refresh token lapses.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultCognitoAPI  = "https://cognito-idp.%s.amazonaws.com/"
	defaultClientID    = ""
	defaultRegion      = ""
	defaultPoolID      = ""
	credsFileName      = ".ft_cli_creds"
	tokenCacheFileName = ".ft_cli_token.json"
)

type creds struct {
	APIBase, Subdomain, Email, Password string
	ClientID, Region, PoolID            string
}

var dashboardAPI string

func home() string { h, _ := os.UserHomeDir(); return h }

func loadCreds() (creds, error) {
	c := creds{ClientID: defaultClientID, Region: defaultRegion, PoolID: defaultPoolID}
	f, err := os.Open(filepath.Join(home(), credsFileName))
	if err != nil {
		return c, fmt.Errorf("open ~/%s: %w", credsFileName, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "FT_API_BASE":
			c.APIBase = strings.TrimRight(strings.TrimSpace(v), "/")
		case "FT_SUBDOMAIN":
			c.Subdomain = strings.TrimSpace(v)
		case "FT_EMAIL":
			c.Email = strings.TrimSpace(v)
		case "FT_PASSWORD":
			c.Password = v
		case "FT_COGNITO_CLIENT_ID":
			c.ClientID = strings.TrimSpace(v)
		case "FT_COGNITO_REGION":
			c.Region = strings.TrimSpace(v)
		case "FT_COGNITO_POOL_ID":
			c.PoolID = strings.TrimSpace(v)
		}
	}
	dashboardAPI = c.APIBase
	return c, sc.Err()
}

// tokenCache is what we persist between runs.
type tokenCache struct {
	IDToken      string    `json:"id_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Email        string    `json:"email"`
}

func tokenPath() string { return filepath.Join(home(), tokenCacheFileName) }

func loadToken() (tokenCache, error) {
	var t tokenCache
	b, err := os.ReadFile(tokenPath())
	if err != nil {
		return t, err
	}
	return t, json.Unmarshal(b, &t)
}

func saveToken(t tokenCache) error {
	b, _ := json.MarshalIndent(t, "", "  ")
	return os.WriteFile(tokenPath(), b, 0o600)
}

func (t tokenCache) valid() bool {
	return t.IDToken != "" && time.Now().Before(t.ExpiresAt.Add(-60*time.Second))
}

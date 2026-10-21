// Cognito authentication for your own Forethought account.
//
// Uses the SRP flow (USER_SRP_AUTH, see srp.go) on the same app client the
// dashboard uses, so the password never leaves the machine. After that, the
// cached refresh token gets new ID tokens without a password.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func cognitoURL(c creds) string { return fmt.Sprintf(defaultCognitoAPI, c.Region) }

type authResult struct {
	AuthenticationResult struct {
		IDToken      string `json:"IdToken"`
		RefreshToken string `json:"RefreshToken"`
		ExpiresIn    int    `json:"ExpiresIn"`
	} `json:"AuthenticationResult"`
	ChallengeName string `json:"ChallengeName"`
}

func cognitoCall(c creds, target string, body map[string]any) (*authResult, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, cognitoURL(c), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService."+target)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		var e struct{ Type, Message, Error string }
		json.Unmarshal(b, &e)
		msg := e.Message
		if msg == "" {
			msg = e.Error
		}
		return nil, fmt.Errorf("cognito %s failed (HTTP %d): %s", target, resp.StatusCode, msg)
	}
	var r authResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// passwordLogin exchanges email+password for tokens via the SRP handshake
// (the only flow this Cognito client allows). The password never leaves the
// machine; SRP sends only a zero-knowledge proof.
func passwordLogin(c creds) (tokenCache, error) {
	return srpLogin(c)
}

// refreshLogin gets a new ID token from a refresh token (no password needed).
func refreshLogin(c creds, rt string) (tokenCache, error) {
	r, err := cognitoCall(c, "InitiateAuth", map[string]any{
		"AuthFlow":       "REFRESH_TOKEN_AUTH",
		"ClientId":       c.ClientID,
		"AuthParameters": map[string]string{"REFRESH_TOKEN": rt},
	})
	if err != nil {
		return tokenCache{}, err
	}
	ar := r.AuthenticationResult
	if ar.IDToken == "" {
		return tokenCache{}, fmt.Errorf("refresh returned no ID token; run `ft login` again")
	}
	return tokenCache{
		IDToken:      ar.IDToken,
		RefreshToken: rt, // refresh flow does not return a new one
		ExpiresAt:    time.Now().Add(time.Duration(ar.ExpiresIn) * time.Second),
		Email:        c.Email,
	}, nil
}

// ensureToken returns a valid ID token, refreshing or re-logging in as needed.
func ensureToken(c creds) (string, error) {
	t, _ := loadToken()
	if t.valid() {
		return t.IDToken, nil
	}
	var refreshErr error
	if t.RefreshToken != "" {
		nt, err := refreshLogin(c, t.RefreshToken)
		if err == nil {
			if err := saveToken(nt); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  could not cache the refreshed token: %v\n", err)
			}
			return nt.IDToken, nil
		}
		refreshErr = err
	}
	if c.Password == "" {
		if refreshErr != nil {
			return "", fmt.Errorf("token refresh failed (%v); run `ft login`", refreshErr)
		}
		return "", fmt.Errorf("session expired and no password on file; run `ft login`")
	}
	nt, err := passwordLogin(c)
	if err != nil {
		return "", err
	}
	if err := saveToken(nt); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  could not cache the token: %v\n", err)
	}
	return nt.IDToken, nil
}

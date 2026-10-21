// AWS Cognito SRP (USER_SRP_AUTH) login.
//
// SRP proves you know the password without ever transmitting it: the password
// stays on this machine, and only a zero-knowledge proof crosses the network.
// This mirrors the flow the Forethought web dashboard uses in the browser.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// The 3072-bit group Cognito uses (RFC 5054 style), hex, and generator g=2.
const srpNHex = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1" +
	"29024E088A67CC74020BBEA63B139B22514A08798E3404DD" +
	"EF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245" +
	"E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED" +
	"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3D" +
	"C2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F" +
	"83655D23DCA3AD961C62F356208552BB9ED529077096966D" +
	"670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B" +
	"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9" +
	"DE2BCBF6955817183995497CEA956AE515D2261898FA0510" +
	"15728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64" +
	"ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7" +
	"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6B" +
	"F12FFA06D98A0864D87602733EC86A64521F2B18177B200C" +
	"BBE117577A615D6C770988C0BAD946E208E24FA074E5AB31" +
	"43DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF"

var (
	srpN, _  = new(big.Int).SetString(srpNHex, 16)
	srpG     = big.NewInt(2)
	srpK     = hexToBig(sha256Hex(hexDecode(padHex(srpNHex) + padHex("2"))))
	infoBits = []byte("Caldera Derived Key")
)

func hexToBig(s string) *big.Int { n, _ := new(big.Int).SetString(s, 16); return n }
func hexDecode(s string) []byte  { b, _ := hex.DecodeString(s); return b }
func sha256Hex(b []byte) string  { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// padHex matches Cognito's padding: even length, and a leading 00 when the top
// bit is set, so values are treated as unsigned fixed-width.
func padHex(h string) string {
	if len(h)%2 == 1 {
		h = "0" + h
	} else if strings.IndexByte("89abcdefABCDEF", h[0]) != -1 {
		h = "00" + h
	}
	return h
}

func hkdfSHA256(ikm, salt, info []byte, n int) []byte {
	prkMac := hmac.New(sha256.New, salt)
	prkMac.Write(ikm)
	prk := prkMac.Sum(nil)
	t := hmac.New(sha256.New, prk)
	t.Write(info)
	t.Write([]byte{1})
	return t.Sum(nil)[:n]
}

// cognitoRaw posts a JSON body to the Cognito IDP endpoint and returns the body.
func cognitoRaw(c creds, target string, body map[string]any) ([]byte, error) {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, cognitoURL(c), bytes.NewReader(raw))
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
	return b, nil
}

// srpLogin runs the two-step SRP handshake and returns cached tokens.
func srpLogin(c creds) (tokenCache, error) {
	// a is the client's secret ephemeral; A = g^a mod N.
	aBytes := make([]byte, 128)
	rand.Read(aBytes)
	a := new(big.Int).Mod(new(big.Int).SetBytes(aBytes), srpN)
	A := new(big.Int).Exp(srpG, a, srpN)
	if new(big.Int).Mod(A, srpN).Sign() == 0 {
		return tokenCache{}, fmt.Errorf("A mod N is zero; retry")
	}

	init, err := cognitoRaw(c, "InitiateAuth", map[string]any{
		"AuthFlow": "USER_SRP_AUTH",
		"ClientId": c.ClientID,
		"AuthParameters": map[string]string{
			"USERNAME": c.Email,
			"SRP_A":    A.Text(16),
		},
	})
	if err != nil {
		return tokenCache{}, err
	}
	var ch struct {
		ChallengeName       string            `json:"ChallengeName"`
		ChallengeParameters map[string]string `json:"ChallengeParameters"`
	}
	if err := json.Unmarshal(init, &ch); err != nil {
		return tokenCache{}, err
	}
	if ch.ChallengeName != "PASSWORD_VERIFIER" {
		return tokenCache{}, fmt.Errorf("unexpected challenge %q (MFA or similar); not supported by this CLI", ch.ChallengeName)
	}

	cp := ch.ChallengeParameters
	saltHex := cp["SALT"]
	secretBlock := cp["SECRET_BLOCK"]
	srpBHex := cp["SRP_B"]
	userIDForSRP := cp["USER_ID_FOR_SRP"]
	B := hexToBig(srpBHex)
	if new(big.Int).Mod(B, srpN).Sign() == 0 {
		return tokenCache{}, fmt.Errorf("B mod N is zero")
	}

	// u = H(PAD(A) | PAD(B))
	u := hexToBig(sha256Hex(hexDecode(padHex(A.Text(16)) + padHex(srpBHex))))

	// x = H(PAD(salt) | H(poolName | userId | ":" | password))
	poolName := c.PoolID
	if i := strings.Index(poolName, "_"); i != -1 {
		poolName = poolName[i+1:]
	}
	userPassHash := sha256Hex([]byte(poolName + userIDForSRP + ":" + c.Password))
	x := hexToBig(sha256Hex(hexDecode(padHex(saltHex) + userPassHash)))

	// S = (B - k * g^x) ^ (a + u*x) mod N
	gx := new(big.Int).Exp(srpG, x, srpN)
	kgx := new(big.Int).Mod(new(big.Int).Mul(srpK, gx), srpN)
	base := new(big.Int).Mod(new(big.Int).Sub(B, kgx), srpN)
	exp := new(big.Int).Add(a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exp, srpN)

	// HKDF key from PAD(S), salt PAD(u)
	key := hkdfSHA256(hexDecode(padHex(S.Text(16))), hexDecode(padHex(u.Text(16))), infoBits, 16)

	now := time.Now().UTC()
	timestamp := now.Format("Mon Jan 2 15:04:05 ") + "UTC " + now.Format("2006")

	secretBlockBytes, err := base64.StdEncoding.DecodeString(secretBlock)
	if err != nil {
		return tokenCache{}, fmt.Errorf("decode secret block: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(poolName))
	mac.Write([]byte(userIDForSRP))
	mac.Write(secretBlockBytes)
	mac.Write([]byte(timestamp))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	resp, err := cognitoRaw(c, "RespondToAuthChallenge", map[string]any{
		"ChallengeName": "PASSWORD_VERIFIER",
		"ClientId":      c.ClientID,
		"ChallengeResponses": map[string]string{
			"PASSWORD_CLAIM_SECRET_BLOCK": secretBlock,
			"PASSWORD_CLAIM_SIGNATURE":    signature,
			"TIMESTAMP":                   timestamp,
			"USERNAME":                    userIDForSRP,
		},
	})
	if err != nil {
		return tokenCache{}, err
	}
	var r authResult
	if err := json.Unmarshal(resp, &r); err != nil {
		return tokenCache{}, err
	}
	if r.ChallengeName != "" {
		return tokenCache{}, fmt.Errorf("extra challenge after password (%s), e.g. MFA — not supported; paste a token instead", r.ChallengeName)
	}
	ar := r.AuthenticationResult
	if ar.IDToken == "" {
		return tokenCache{}, fmt.Errorf("no ID token returned")
	}
	return tokenCache{
		IDToken:      ar.IDToken,
		RefreshToken: ar.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(ar.ExpiresIn) * time.Second),
		Email:        c.Email,
	}, nil
}

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OIDC is an authorization-code client for an issuer such as Authentik.
type OIDC struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTP         *http.Client
}

type provider struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

func (o *OIDC) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (o *OIDC) discover(ctx context.Context) (provider, error) {
	endpoint := strings.TrimRight(o.Issuer, "/") + "/.well-known/openid-configuration"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return provider{}, err
	}
	response, err := o.client().Do(request)
	if err != nil {
		return provider{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return provider{}, fmt.Errorf("oidc discovery returned %d", response.StatusCode)
	}
	var found provider
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&found); err != nil {
		return provider{}, err
	}
	if found.AuthorizationEndpoint == "" || found.TokenEndpoint == "" || found.UserinfoEndpoint == "" {
		return provider{}, fmt.Errorf("oidc discovery document is incomplete")
	}
	return found, nil
}

// AuthCodeURL returns the browser redirect for a login.
func (o *OIDC) AuthCodeURL(ctx context.Context, state, challenge string) (string, error) {
	found, err := o.discover(ctx)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", o.ClientID)
	values.Set("redirect_uri", o.RedirectURL)
	values.Set("scope", "openid email")
	values.Set("state", state)
	values.Set("code_challenge", challenge)
	values.Set("code_challenge_method", "S256")
	return found.AuthorizationEndpoint + "?" + values.Encode(), nil
}

// Exchange trades an authorization code for the user's email.
func (o *OIDC) Exchange(ctx context.Context, code, verifier string) (string, error) {
	found, err := o.discover(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", o.RedirectURL)
	form.Set("client_id", o.ClientID)
	form.Set("code_verifier", verifier)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, found.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(o.ClientID, o.ClientSecret)
	response, err := o.client().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc token returned %d", response.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", fmt.Errorf("oidc token response has no access_token")
	}
	infoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, found.UserinfoEndpoint, nil)
	if err != nil {
		return "", err
	}
	infoReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	infoResp, err := o.client().Do(infoReq)
	if err != nil {
		return "", err
	}
	defer infoResp.Body.Close()
	if infoResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc userinfo returned %d", infoResp.StatusCode)
	}
	var info struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
	}
	if err := json.NewDecoder(io.LimitReader(infoResp.Body, 1<<20)).Decode(&info); err != nil {
		return "", err
	}
	if info.Email == "" {
		return "", fmt.Errorf("oidc userinfo has no email")
	}
	if info.EmailVerified != nil && !*info.EmailVerified {
		return "", fmt.Errorf("oidc email is not verified")
	}
	return strings.ToLower(strings.TrimSpace(info.Email)), nil
}

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOIDCExchange(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"authorization_endpoint":"` + server.URL + `/auth","token_endpoint":"` + server.URL + `/token","userinfo_endpoint":"` + server.URL + `/userinfo"}`))
		case "/token":
			if user, pass, ok := r.BasicAuth(); !ok || user != "client" || pass != "secret" {
				t.Fatalf("client auth %q %q", user, pass)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("code") != "code-1" || r.Form.Get("code_verifier") == "" {
				t.Fatalf("form %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"access_token":"access-1"}`))
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer access-1" {
				t.Fatalf("userinfo auth %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"email":"Owner@Example.com","email_verified":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &OIDC{
		Issuer:       server.URL,
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURL:  "http://godesk.test/v1/auth/oidc/callback",
	}
	email, err := client.Exchange(context.Background(), "code-1", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	if email != "owner@example.com" {
		t.Fatalf("email %s", email)
	}
}

package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/auth"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/secretbox"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/store/memory"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

func TestVerticalSlice(t *testing.T) {
	var mu sync.Mutex
	var sentUser string
	var sentTo string
	var hooks []string
	gatewaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/message":
			sentUser = user
			var payload struct {
				PhoneNumbers []string `json:"phoneNumbers"`
			}
			_ = json.Unmarshal(body, &payload)
			if len(payload.PhoneNumbers) > 0 {
				sentTo = payload.PhoneNumbers[0]
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"gw-send-1","state":"Pending"}`))
		case "/webhooks":
			var payload struct {
				URL   string `json:"url"`
				Event string `json:"event"`
			}
			_ = json.Unmarshal(body, &payload)
			if payload.Event == "sms:received" {
				hooks = append(hooks, payload.URL)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gatewaySrv.Close()

	var deliveries [][]byte
	var authHeader string
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		deliveries = append(deliveries, body)
		authHeader = r.Header.Get("Authorization")
		mu.Unlock()
		if r.Header.Get("X-Event-Type") != "message.phone.received" {
			t.Errorf("event header %s", r.Header.Get("X-Event-Type"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhookSrv.Close()

	tokens, err := auth.NewTokens("test-jwt-secret-must-be-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New("test-jwt-secret-must-be-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	api := New(Server{
		Store:      memory.New(),
		Tokens:     tokens,
		Box:        box,
		PublicURL:  "http://godesk.test",
		BcryptCost: bcrypt.MinCost,
	})
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	signup := postJSON(t, server.URL+"/v1/auth/signup", "", map[string]string{
		"email": "Ada@Example.com", "password": "correct horse",
	})
	if signup.StatusCode != http.StatusOK {
		t.Fatalf("signup %d %s", signup.StatusCode, signup.Body)
	}
	var session struct {
		Data struct {
			Token  string `json:"token"`
			APIKey string `json:"api_key"`
		} `json:"data"`
	}
	decode(t, signup.Body, &session)
	if session.Data.Token == "" || session.Data.APIKey == "" {
		t.Fatalf("session %+v", session.Data)
	}

	conflict := postJSON(t, server.URL+"/v1/auth/signup", "", map[string]string{
		"email": "ada@example.com", "password": "correct horse",
	})
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate signup %d", conflict.StatusCode)
	}
	badLogin := postJSON(t, server.URL+"/v1/auth/login", "", map[string]string{
		"email": "ada@example.com", "password": "wrong-password",
	})
	if badLogin.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login %d", badLogin.StatusCode)
	}

	var phoneBIngest string
	for _, device := range []map[string]string{
		{"name": "SIM A", "phone_number": "+18005550199", "username": "phone-a"},
		{"name": "SIM B", "phone_number": "+18005550198", "username": "phone-b"},
	} {
		created := postJSON(t, server.URL+"/v1/devices", session.Data.Token, map[string]string{
			"name":         device["name"],
			"phone_number": device["phone_number"],
			"base_url":     gatewaySrv.URL,
			"username":     device["username"],
			"password":     "gateway-secret",
			"mode":         "local",
		})
		if created.StatusCode != http.StatusCreated {
			t.Fatalf("device %s: %d %s", device["name"], created.StatusCode, created.Body)
		}
		var payload struct {
			Data struct {
				Registration string `json:"webhook_registration"`
				IngestURL    string `json:"ingest_url"`
			} `json:"data"`
		}
		decode(t, created.Body, &payload)
		if payload.Data.Registration != "ok" {
			t.Fatalf("registration %q", payload.Data.Registration)
		}
		phoneBIngest = payload.Data.IngestURL
	}

	sent := postJSON(t, server.URL+"/v1/messages/send", "", map[string]string{
		"from": "+18005550198", "to": "+18005550100", "content": "hello from b",
	}, session.Data.APIKey)
	if sent.StatusCode != http.StatusOK {
		t.Fatalf("send %d %s", sent.StatusCode, sent.Body)
	}
	var sentBody struct {
		Message string `json:"message"`
		Data    struct {
			Status string `json:"status"`
			Owner  string `json:"owner"`
		} `json:"data"`
	}
	decode(t, sent.Body, &sentBody)
	if sentBody.Message != "message added to queue" || sentBody.Data.Status != "pending" || sentBody.Data.Owner != "+18005550198" {
		t.Fatalf("send body %+v", sentBody)
	}
	mu.Lock()
	if sentUser != "phone-b" || sentTo != "+18005550100" {
		t.Fatalf("gateway saw user %q to %q", sentUser, sentTo)
	}
	if len(hooks) < 2 {
		t.Fatalf("expected a received webhook per device, got %d", len(hooks))
	}
	mu.Unlock()
	ingestToken := phoneBIngest[strings.LastIndex(phoneBIngest, "/")+1:]
	ingest := server.URL + "/v1/gateway/events/" + ingestToken

	hook := postJSON(t, server.URL+"/v1/webhooks", session.Data.Token, map[string]any{
		"url": webhookSrv.URL + "/sms", "signing_key": "hook-secret", "events": []string{"message.phone.received"},
	})
	if hook.StatusCode != http.StatusCreated {
		t.Fatalf("webhook %d %s", hook.StatusCode, hook.Body)
	}

	event := map[string]any{
		"event": "sms:received",
		"payload": map[string]any{
			"messageId": "inbound-1",
			"message":   "ping",
			"sender":    "+18005550100",
			"recipient": "+18005550198",
			"simNumber": 1,
		},
	}
	first := postJSON(t, ingest, "", event)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("ingest %d %s", first.StatusCode, first.Body)
	}
	second := postJSON(t, ingest, "", event)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("duplicate ingest %d", second.StatusCode)
	}
	mu.Lock()
	if len(deliveries) != 1 {
		t.Fatalf("webhook deliveries %d", len(deliveries))
	}
	delivery := deliveries[0]
	header := authHeader
	mu.Unlock()

	var cloud struct {
		Type string `json:"type"`
		Data struct {
			Content string `json:"content"`
			Owner   string `json:"owner"`
			Contact string `json:"contact"`
		} `json:"data"`
	}
	decode(t, string(delivery), &cloud)
	if cloud.Type != "message.phone.received" || cloud.Data.Content != "ping" || cloud.Data.Owner != "+18005550198" || cloud.Data.Contact != "+18005550100" {
		t.Fatalf("cloud event %+v", cloud)
	}
	if !strings.HasPrefix(header, "Bearer ") {
		t.Fatalf("signing header %q", header)
	}
	parsed, err := jwt.Parse(strings.TrimPrefix(header, "Bearer "), func(token *jwt.Token) (any, error) {
		return []byte("hook-secret"), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("webhook jwt: %v", err)
	}

	health, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health %d", health.StatusCode)
	}
	dashboard, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer dashboard.Body.Close()
	page, _ := io.ReadAll(dashboard.Body)
	if !bytes.Contains(page, []byte("GoDesk SMS")) {
		t.Fatal("dashboard missing")
	}
}

type recorded struct {
	StatusCode int
	Body       string
}

func postJSON(t *testing.T, endpoint, bearer string, payload any, apiKey ...string) recorded {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if len(apiKey) > 0 && apiKey[0] != "" {
		request.Header.Set("x-api-key", apiKey[0])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return recorded{StatusCode: response.StatusCode, Body: string(body)}
}

func decode(t *testing.T, raw string, dest any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendAndRegisterLocal(t *testing.T) {
	var sawSend, sawHook bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "sms" || pass != "secret" {
			t.Errorf("basic auth %q %q ok=%v", user, pass, ok)
		}
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/message":
			sawSend = true
			var payload struct {
				PhoneNumbers []string `json:"phoneNumbers"`
				TextMessage  struct {
					Text string `json:"text"`
				} `json:"textMessage"`
				SimNumber int `json:"simNumber"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.TextMessage.Text != "hello" || payload.PhoneNumbers[0] != "+18005550100" || payload.SimNumber != 1 {
				t.Fatalf("payload %+v", payload)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"gw-1","state":"Pending"}`))
		case "/webhooks":
			sawHook = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := New()
	device := Device{Mode: "local", BaseURL: server.URL, Username: "sms", Password: "secret"}
	if err := client.RegisterWebhooks(context.Background(), device, "https://godesk.example/v1/gateway/events/token"); err != nil {
		t.Fatal(err)
	}
	id, err := client.SendSMS(context.Background(), device, "+18005550100", "hello", 1)
	if err != nil {
		t.Fatal(err)
	}
	if id != "gw-1" || !sawSend || !sawHook {
		t.Fatalf("id %q send %v hook %v", id, sawSend, sawHook)
	}
}

func TestPrivatePaths(t *testing.T) {
	if sendPath("private") != "/api/3rdparty/v1/messages" {
		t.Fatal(sendPath("private"))
	}
	if webhookPath("local") != "/webhooks" {
		t.Fatal(webhookPath("local"))
	}
}

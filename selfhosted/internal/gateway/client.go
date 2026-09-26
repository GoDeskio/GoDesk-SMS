package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Device is the subset of a handset the gateway client needs.
type Device struct {
	Mode     string
	BaseURL  string
	Username string
	Password string
}

// Client calls SMS Gateway for Android. Local mode uses the on-phone server.
// Private mode uses the self-hosted server's 3rdparty API paths.
type Client struct {
	HTTP *http.Client
}

// New returns a client that does not follow redirects, so basic-auth credentials
// stay on the device URL the operator registered.
func New() *Client {
	return &Client{HTTP: &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// SendSMS posts one text message. sim is 1 or 2.
func (c *Client) SendSMS(ctx context.Context, device Device, to, text string, sim int) (string, error) {
	payload := map[string]any{
		"textMessage":  map[string]string{"text": text},
		"phoneNumbers": []string{to},
		"simNumber":    sim,
	}
	var decoded struct {
		ID string `json:"id"`
	}
	if err := c.post(ctx, device, sendPath(device.Mode), payload, &decoded); err != nil {
		return "", err
	}
	return decoded.ID, nil
}

// RegisterWebhooks asks the gateway to POST sms events to callback.
func (c *Client) RegisterWebhooks(ctx context.Context, device Device, callback string) error {
	for _, event := range []string{"sms:received", "sms:sent", "sms:delivered", "sms:failed"} {
		body := map[string]string{"url": callback, "event": event}
		if err := c.post(ctx, device, webhookPath(device.Mode), body, nil); err != nil {
			return fmt.Errorf("register %s: %w", event, err)
		}
	}
	return nil
}

func (c *Client) post(ctx context.Context, device Device, path string, payload any, dest any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint, err := join(device.BaseURL, path)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(device.Username, device.Password)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode == http.StatusConflict {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("gateway %s returned %d", path, response.StatusCode)
	}
	if dest == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode gateway response: %w", err)
	}
	return nil
}

func sendPath(mode string) string {
	if mode == "private" {
		return "/api/3rdparty/v1/messages"
	}
	return "/message"
}

func webhookPath(mode string) string {
	if mode == "private" {
		return "/api/3rdparty/v1/webhooks"
	}
	return "/webhooks"
}

func join(base, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", err
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

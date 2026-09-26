package httpapi

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/auth"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/gateway"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/model"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/phone"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/secretbox"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/store"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/urls"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

//go:embed dashboard.html
var dashboardHTML []byte

// Server is the self-hosted GoDesk HTTP API and dashboard.
type Server struct {
	Store      store.Store
	Tokens     *auth.Tokens
	Box        *secretbox.Box
	Gateway    *gateway.Client
	PublicURL  string
	OIDC       *auth.OIDC
	BcryptCost int
	Now        func() time.Time
	HTTP       *http.Client
}

// New fills defaults and returns a server.
func New(s Server) *Server {
	if s.BcryptCost == 0 {
		s.BcryptCost = bcrypt.DefaultCost
	}
	if s.Now == nil {
		s.Now = func() time.Time { return time.Now().UTC() }
	}
	if s.Gateway == nil {
		s.Gateway = gateway.New()
	}
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 5 * time.Second}
	}
	s.PublicURL = strings.TrimRight(s.PublicURL, "/")
	return &s
}

// Handler is the public HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /v1/auth/signup", s.signup)
	mux.HandleFunc("POST /v1/auth/login", s.login)
	mux.HandleFunc("GET /v1/auth/oidc/start", s.oidcStart)
	mux.HandleFunc("GET /v1/auth/oidc/callback", s.oidcCallback)
	mux.HandleFunc("GET /v1/users/me", s.me)
	mux.HandleFunc("POST /v1/devices", s.createDevice)
	mux.HandleFunc("GET /v1/devices", s.listDevices)
	mux.HandleFunc("DELETE /v1/devices/{id}", s.deleteDevice)
	mux.HandleFunc("POST /v1/devices/{id}/webhooks", s.registerDeviceWebhooks)
	mux.HandleFunc("POST /v1/messages/send", s.sendMessage)
	mux.HandleFunc("GET /v1/messages", s.listMessages)
	mux.HandleFunc("POST /v1/webhooks", s.createWebhook)
	mux.HandleFunc("GET /v1/webhooks", s.listWebhooks)
	mux.HandleFunc("DELETE /v1/webhooks/{id}", s.deleteWebhook)
	mux.HandleFunc("POST /v1/gateway/events/{token}", s.gatewayEvent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) dashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(dashboardHTML)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	s.writeSession(w, r, body.Email, body.Password, true)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	s.writeSession(w, r, body.Email, body.Password, false)
}

func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, email, password string, create bool) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if len(password) < 8 || len(password) > 200 {
		writeError(w, http.StatusUnprocessableEntity, "password must be 8 to 200 characters")
		return
	}
	var user model.User
	if create {
		hash, err := auth.HashPassword(password, s.BcryptCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not hash password")
			return
		}
		apiKey, err := auth.RandomToken(32)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not create api key")
			return
		}
		user = model.User{
			ID:           uuid.NewString(),
			Email:        normalized,
			PasswordHash: hash,
			APIKey:       apiKey,
			CreatedAt:    s.Now(),
		}
		if err := s.Store.CreateUser(r.Context(), user); err != nil {
			if errors.Is(err, store.ErrConflict) {
				writeError(w, http.StatusConflict, "an account with that email already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "could not create account")
			return
		}
	} else {
		user, err = s.Store.UserByEmail(r.Context(), normalized)
		if err != nil || !auth.CheckPassword(user.PasswordHash, password) {
			writeError(w, http.StatusUnauthorized, "email or password is incorrect")
			return
		}
	}
	s.writeAuth(w, http.StatusOK, user)
}

func (s *Server) writeAuth(w http.ResponseWriter, status int, user model.User) {
	token, err := s.Tokens.SessionToken(user.ID, user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	writeJSON(w, status, map[string]any{
		"status":  "success",
		"message": "authenticated",
		"data": map[string]any{
			"token":   token,
			"api_key": user.APIKey,
			"user": map[string]string{
				"id":    user.ID,
				"email": user.Email,
			},
		},
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "user",
		"data": map[string]any{
			"api_key": user.APIKey,
			"user": map[string]string{
				"id":    user.ID,
				"email": user.Email,
			},
		},
	})
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var body struct {
		Name        string `json:"name"`
		PhoneNumber string `json:"phone_number"`
		BaseURL     string `json:"base_url"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		Mode        string `json:"mode"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	device, note, err := s.buildDevice(r.Context(), user.ID, body.Name, body.PhoneNumber, body.BaseURL, body.Username, body.Password, body.Mode)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.Store.CreateDevice(r.Context(), device); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "a device with that phone number is already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not save device")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":  "success",
		"message": "device registered",
		"data":    s.deviceJSON(device, note),
	})
}

func (s *Server) buildDevice(ctx context.Context, userID, name, phoneNumber, baseURL, username, password, mode string) (model.Device, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return model.Device{}, "", errors.New("name is required")
	}
	number, err := phone.Normalize(phoneNumber)
	if err != nil {
		return model.Device{}, "", err
	}
	origin, err := urls.NormalizeBaseURL(baseURL, false)
	if err != nil {
		return model.Device{}, "", err
	}
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return model.Device{}, "", errors.New("gateway username and password are required")
	}
	if mode == "" {
		mode = model.ModeLocal
	}
	if mode != model.ModeLocal && mode != model.ModePrivate {
		return model.Device{}, "", errors.New("mode must be local or private")
	}
	sealed, err := s.Box.Seal(password)
	if err != nil {
		return model.Device{}, "", errors.New("could not protect the gateway password")
	}
	token, err := auth.RandomToken(32)
	if err != nil {
		return model.Device{}, "", errors.New("could not create an ingest token")
	}
	device := model.Device{
		ID:          uuid.NewString(),
		UserID:      userID,
		Name:        name,
		PhoneNumber: number,
		BaseURL:     origin,
		Username:    username,
		Password:    sealed,
		Mode:        mode,
		IngestToken: token,
		CreatedAt:   s.Now(),
	}
	note := "ok"
	if err := s.Gateway.RegisterWebhooks(ctx, gateway.Device{
		Mode: device.Mode, BaseURL: device.BaseURL, Username: username, Password: password,
	}, s.ingestURL(token)); err != nil {
		note = err.Error()
		log.Printf("device webhook registration failed for %s: %s", device.ID, note)
	}
	return device, note, nil
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	devices, err := s.Store.ListDevices(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list devices")
		return
	}
	out := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		out = append(out, s.deviceJSON(device, ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "devices", "data": out})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := s.Store.DeleteDevice(r.Context(), user.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete device")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "device deleted"})
}

func (s *Server) registerDeviceWebhooks(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	device, err := s.Store.DeviceByID(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	password, err := s.Box.Open(device.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the gateway password")
		return
	}
	note := "ok"
	if err := s.Gateway.RegisterWebhooks(r.Context(), gateway.Device{
		Mode: device.Mode, BaseURL: device.BaseURL, Username: device.Username, Password: password,
	}, s.ingestURL(device.IngestToken)); err != nil {
		note = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "webhook registration attempted",
		"data":    s.deviceJSON(device, note),
	})
}

func (s *Server) deviceJSON(device model.Device, registration string) map[string]any {
	return map[string]any{
		"id":                   device.ID,
		"name":                 device.Name,
		"phone_number":         device.PhoneNumber,
		"base_url":             device.BaseURL,
		"username":             device.Username,
		"mode":                 device.Mode,
		"ingest_url":           s.ingestURL(device.IngestToken),
		"webhook_registration": registration,
		"created_at":           device.CreatedAt,
	}
}

func (s *Server) ingestURL(token string) string {
	return s.PublicURL + "/v1/gateway/events/" + token
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var body struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Content   string `json:"content"`
		RequestID string `json:"request_id"`
		SIM       string `json:"sim"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	from, err := phone.Normalize(body.From)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "from "+err.Error())
		return
	}
	to, err := phone.Normalize(body.To)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "to "+err.Error())
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" || len(content) > 1600 {
		writeError(w, http.StatusUnprocessableEntity, "content is required and must be at most 1600 characters")
		return
	}
	device, err := s.Store.DeviceByPhone(r.Context(), user.ID, from)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "no device is registered for "+from)
		return
	}
	password, err := s.Box.Open(device.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the gateway password")
		return
	}
	simNumber := 1
	simLabel := model.SIM1
	if strings.EqualFold(body.SIM, model.SIM2) {
		simNumber = 2
		simLabel = model.SIM2
	}
	now := s.Now()
	message := model.Message{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		DeviceID:  device.ID,
		RequestID: strings.TrimSpace(body.RequestID),
		Owner:     from,
		Contact:   to,
		Content:   content,
		Type:      model.TypeMobileTerminated,
		Status:    model.StatusPending,
		SIM:       simLabel,
		CreatedAt: now,
		UpdatedAt: now,
	}
	gatewayID, err := s.Gateway.SendSMS(r.Context(), gateway.Device{
		Mode: device.Mode, BaseURL: device.BaseURL, Username: device.Username, Password: password,
	}, to, content, simNumber)
	if err != nil {
		message.Status = model.StatusFailed
		message.FailureReason = "gateway request failed"
		_ = s.insertIgnoringDuplicate(r.Context(), message)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"status":  "error",
			"message": "the SMS gateway did not accept the message",
			"data":    messageJSON(message),
		})
		return
	}
	message.GatewayID = gatewayID
	if _, err := s.Store.InsertMessage(r.Context(), message); err != nil {
		writeError(w, http.StatusInternalServerError, "the gateway accepted the message but it could not be stored")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "message added to queue",
		"data":    messageJSON(message),
	})
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	messages, err := s.Store.ListMessages(r.Context(), user.ID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list messages")
		return
	}
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		out = append(out, messageJSON(message))
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "messages", "data": out})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var body struct {
		URL        string   `json:"url"`
		SigningKey string   `json:"signing_key"`
		Events     []string `json:"events"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	endpoint, err := urls.NormalizeBaseURL(body.URL, true)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if len(body.Events) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "events must include message.phone.received")
		return
	}
	hook := model.Webhook{
		ID:         uuid.NewString(),
		UserID:     user.ID,
		URL:        endpoint,
		SigningKey: body.SigningKey,
		Events:     body.Events,
		CreatedAt:  s.Now(),
	}
	if err := s.Store.CreateWebhook(r.Context(), hook); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save webhook")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":  "success",
		"message": "webhook saved",
		"data":    webhookJSON(hook),
	})
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	hooks, err := s.Store.ListWebhooks(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list webhooks")
		return
	}
	out := make([]map[string]any, 0, len(hooks))
	for _, hook := range hooks {
		out = append(out, webhookJSON(hook))
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "webhooks", "data": out})
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := s.Store.DeleteWebhook(r.Context(), user.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete webhook")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "webhook deleted"})
}

func (s *Server) gatewayEvent(w http.ResponseWriter, r *http.Request) {
	device, err := s.Store.DeviceByIngestToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unknown device token")
		return
	}
	var event struct {
		Event   string          `json:"event"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := readJSON(r, &event); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	var payload struct {
		MessageID string `json:"messageId"`
		Message   string `json:"message"`
		Sender    string `json:"sender"`
		Recipient string `json:"recipient"`
		SimNumber *int   `json:"simNumber"`
		Reason    string `json:"reason"`
	}
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "payload is invalid")
			return
		}
	}
	switch event.Event {
	case "sms:received":
		s.receiveSMS(w, r, device, payload.MessageID, payload.Message, payload.Sender, payload.Recipient, payload.SimNumber)
	case "sms:sent", "sms:delivered", "sms:failed":
		status := model.StatusSent
		if event.Event == "sms:delivered" {
			status = model.StatusDelivered
		}
		if event.Event == "sms:failed" {
			status = model.StatusFailed
		}
		if payload.MessageID != "" {
			_ = s.Store.UpdateMessageByGatewayID(r.Context(), device.UserID, payload.MessageID, status, payload.Reason)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "event stored"})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "event ignored"})
	}
}

func (s *Server) receiveSMS(w http.ResponseWriter, r *http.Request, device model.Device, gatewayID, content, sender, recipient string, simNumber *int) {
	owner := recipient
	if owner == "" {
		owner = device.PhoneNumber
	}
	owner, err := phone.Normalize(owner)
	if err != nil {
		owner = device.PhoneNumber
	}
	contact, err := phone.Normalize(sender)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "sender "+err.Error())
		return
	}
	simLabel := model.SIM1
	if simNumber != nil && *simNumber == 2 {
		simLabel = model.SIM2
	}
	now := s.Now()
	message := model.Message{
		ID:        uuid.NewString(),
		UserID:    device.UserID,
		DeviceID:  device.ID,
		Owner:     owner,
		Contact:   contact,
		Content:   content,
		Type:      model.TypeMobileOriginated,
		Status:    model.StatusReceived,
		SIM:       simLabel,
		GatewayID: gatewayID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	created, err := s.Store.InsertMessage(r.Context(), message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store message")
		return
	}
	if created {
		s.fanout(r.Context(), device.UserID, message)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "message received", "data": messageJSON(message)})
}

func (s *Server) fanout(ctx context.Context, userID string, message model.Message) {
	hooks, err := s.Store.ListWebhooksByEvent(ctx, userID, model.EventMessagePhoneReceived)
	if err != nil {
		log.Printf("list webhooks: %v", err)
		return
	}
	eventID := uuid.NewString()
	body, err := json.Marshal(map[string]any{
		"specversion":     "1.0",
		"id":              eventID,
		"source":          s.PublicURL,
		"type":            model.EventMessagePhoneReceived,
		"time":            s.Now(),
		"datacontenttype": "application/json",
		"data": map[string]any{
			"message_id":  message.ID,
			"user_id":     userID,
			"owner":       message.Owner,
			"encrypted":   false,
			"contact":     message.Contact,
			"timestamp":   message.CreatedAt,
			"content":     message.Content,
			"sim":         message.SIM,
			"attachments": []string{},
		},
	})
	if err != nil {
		return
	}
	for _, hook := range hooks {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
		if err != nil {
			continue
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Event-Type", model.EventMessagePhoneReceived)
		if strings.TrimSpace(hook.SigningKey) != "" {
			token, err := s.Tokens.WebhookToken(userID, hook.URL, hook.SigningKey)
			if err == nil {
				request.Header.Set("Authorization", "Bearer "+token)
			}
		}
		response, err := s.HTTP.Do(request)
		if err != nil {
			log.Printf("webhook delivery failed: %v", err)
			continue
		}
		response.Body.Close()
	}
}

func (s *Server) insertIgnoringDuplicate(ctx context.Context, message model.Message) bool {
	created, err := s.Store.InsertMessage(ctx, message)
	if err != nil {
		log.Printf("store message: %v", err)
		return false
	}
	return created
}

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		writeError(w, http.StatusNotFound, "OIDC is not configured")
		return
	}
	state, err := auth.RandomToken(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start login")
		return
	}
	verifier, challenge, err := auth.PKCE()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start login")
		return
	}
	cookie, err := s.Tokens.OIDCStateCookie(state, verifier)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start login")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "godesk_oidc",
		Value:    cookie,
		Path:     "/v1/auth/oidc",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(s.PublicURL, "https://"),
		MaxAge:   600,
	})
	location, err := s.OIDC.AuthCodeURL(r.Context(), state, challenge)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not reach the OIDC issuer")
		return
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		writeError(w, http.StatusNotFound, "OIDC is not configured")
		return
	}
	cookie, err := r.Cookie("godesk_oidc")
	if err != nil {
		writeError(w, http.StatusBadRequest, "OIDC state is missing")
		return
	}
	state, verifier, err := s.Tokens.ParseOIDCState(cookie.Value)
	if err != nil || state == "" || state != r.URL.Query().Get("state") {
		writeError(w, http.StatusBadRequest, "OIDC state does not match")
		return
	}
	email, err := s.OIDC.Exchange(r.Context(), r.URL.Query().Get("code"), verifier)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "OIDC login failed")
		return
	}
	user, err := s.Store.UserByEmail(r.Context(), email)
	if errors.Is(err, store.ErrNotFound) {
		random, randErr := auth.RandomToken(32)
		if randErr != nil {
			writeError(w, http.StatusInternalServerError, "could not create account")
			return
		}
		hash, hashErr := auth.HashPassword(random, s.BcryptCost)
		if hashErr != nil {
			writeError(w, http.StatusInternalServerError, "could not create account")
			return
		}
		apiKey, keyErr := auth.RandomToken(32)
		if keyErr != nil {
			writeError(w, http.StatusInternalServerError, "could not create account")
			return
		}
		user = model.User{ID: uuid.NewString(), Email: email, PasswordHash: hash, APIKey: apiKey, CreatedAt: s.Now()}
		if err := s.Store.CreateUser(r.Context(), user); err != nil {
			writeError(w, http.StatusInternalServerError, "could not create account")
			return
		}
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load account")
		return
	}
	token, err := s.Tokens.SessionToken(user.ID, user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.Redirect(w, r, "/#token="+token, http.StatusFound)
}

func (s *Server) currentUser(r *http.Request) (model.User, bool) {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		userID, _, err := s.Tokens.ParseSession(strings.TrimPrefix(header, "Bearer "))
		if err == nil {
			user, err := s.Store.UserByID(r.Context(), userID)
			if err == nil {
				return user, true
			}
		}
	}
	if apiKey := r.Header.Get("x-api-key"); apiKey != "" {
		user, err := s.Store.UserByAPIKey(r.Context(), apiKey)
		if err == nil {
			return user, true
		}
	}
	return model.User{}, false
}

func messageJSON(message model.Message) map[string]any {
	var requestID any
	if message.RequestID != "" {
		requestID = message.RequestID
	}
	var reason any
	if message.FailureReason != "" {
		reason = message.FailureReason
	}
	return map[string]any{
		"id":             message.ID,
		"request_id":     requestID,
		"owner":          message.Owner,
		"user_id":        message.UserID,
		"contact":        message.Contact,
		"content":        message.Content,
		"type":           message.Type,
		"status":         message.Status,
		"sim":            message.SIM,
		"failure_reason": reason,
		"created_at":     message.CreatedAt,
		"updated_at":     message.UpdatedAt,
	}
}

func webhookJSON(hook model.Webhook) map[string]any {
	return map[string]any{
		"id":         hook.ID,
		"url":        hook.URL,
		"events":     hook.Events,
		"created_at": hook.CreatedAt,
	}
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dest)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"status": "error", "message": message})
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 320 || !strings.Contains(value, "@") || strings.HasPrefix(value, "@") || strings.HasSuffix(value, "@") {
		return "", errors.New("email is invalid")
	}
	return value, nil
}

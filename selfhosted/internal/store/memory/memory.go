package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/model"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/store"
)

// Store is an in-memory Store used by tests and local experiments.
type Store struct {
	mu       sync.Mutex
	users    map[string]model.User
	byEmail  map[string]string
	byAPIKey map[string]string
	devices  map[string]model.Device
	messages []model.Message
	webhooks map[string]model.Webhook
}

// New returns an empty store.
func New() *Store {
	return &Store{
		users:    map[string]model.User{},
		byEmail:  map[string]string{},
		byAPIKey: map[string]string{},
		devices:  map[string]model.Device{},
		webhooks: map[string]model.Webhook{},
	}
}

func (s *Store) Ping(context.Context) error { return nil }

func (s *Store) CreateUser(_ context.Context, user model.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byEmail[user.Email]; ok {
		return store.ErrConflict
	}
	if _, ok := s.byAPIKey[user.APIKey]; ok {
		return store.ErrConflict
	}
	s.users[user.ID] = user
	s.byEmail[user.Email] = user.ID
	s.byAPIKey[user.APIKey] = user.ID
	return nil
}

func (s *Store) UserByEmail(_ context.Context, email string) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byEmail[email]
	if !ok {
		return model.User{}, store.ErrNotFound
	}
	return s.users[id], nil
}

func (s *Store) UserByID(_ context.Context, id string) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return model.User{}, store.ErrNotFound
	}
	return user, nil
}

func (s *Store) UserByAPIKey(_ context.Context, apiKey string) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byAPIKey[apiKey]
	if !ok {
		return model.User{}, store.ErrNotFound
	}
	return s.users[id], nil
}

func (s *Store) CreateDevice(_ context.Context, device model.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.devices {
		if existing.UserID == device.UserID && existing.PhoneNumber == device.PhoneNumber {
			return store.ErrConflict
		}
	}
	s.devices[device.ID] = device
	return nil
}

func (s *Store) ListDevices(_ context.Context, userID string) ([]model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Device
	for _, device := range s.devices {
		if device.UserID == userID {
			out = append(out, device)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) DeviceByID(_ context.Context, userID, id string) (model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[id]
	if !ok || device.UserID != userID {
		return model.Device{}, store.ErrNotFound
	}
	return device, nil
}

func (s *Store) DeviceByPhone(_ context.Context, userID, phone string) (model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, device := range s.devices {
		if device.UserID == userID && device.PhoneNumber == phone {
			return device, nil
		}
	}
	return model.Device{}, store.ErrNotFound
}

func (s *Store) DeviceByIngestToken(_ context.Context, token string) (model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, device := range s.devices {
		if device.IngestToken == token {
			return device, nil
		}
	}
	return model.Device{}, store.ErrNotFound
}

func (s *Store) DeleteDevice(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[id]
	if !ok || device.UserID != userID {
		return store.ErrNotFound
	}
	delete(s.devices, id)
	return nil
}

func (s *Store) InsertMessage(_ context.Context, message model.Message) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message.GatewayID != "" {
		for _, existing := range s.messages {
			if existing.UserID == message.UserID && existing.GatewayID == message.GatewayID {
				return false, nil
			}
		}
	}
	s.messages = append(s.messages, message)
	return true, nil
}

func (s *Store) ListMessages(_ context.Context, userID string, limit int) ([]model.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Message
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].UserID == userID {
			out = append(out, s.messages[i])
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Store) UpdateMessageByGatewayID(_ context.Context, userID, gatewayID, status, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.messages {
		if s.messages[i].UserID == userID && s.messages[i].GatewayID == gatewayID {
			s.messages[i].Status = status
			s.messages[i].FailureReason = reason
			s.messages[i].UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	return store.ErrNotFound
}

func (s *Store) CreateWebhook(_ context.Context, hook model.Webhook) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webhooks[hook.ID] = hook
	return nil
}

func (s *Store) ListWebhooks(_ context.Context, userID string) ([]model.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.webhooksFor(userID, ""), nil
}

func (s *Store) ListWebhooksByEvent(_ context.Context, userID, event string) ([]model.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.webhooksFor(userID, event), nil
}

func (s *Store) webhooksFor(userID, event string) []model.Webhook {
	var out []model.Webhook
	for _, hook := range s.webhooks {
		if hook.UserID != userID {
			continue
		}
		if event != "" && !contains(hook.Events, event) {
			continue
		}
		out = append(out, hook)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) DeleteWebhook(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	hook, ok := s.webhooks[id]
	if !ok || hook.UserID != userID {
		return store.ErrNotFound
	}
	delete(s.webhooks, id)
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

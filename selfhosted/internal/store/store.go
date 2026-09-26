package store

import (
	"context"
	"errors"

	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/model"
)

// ErrNotFound is returned when a row does not exist for this caller.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a unique field is already taken.
var ErrConflict = errors.New("conflict")

// Store is the persistence used by the self-hosted API.
type Store interface {
	Ping(ctx context.Context) error
	CreateUser(ctx context.Context, user model.User) error
	UserByEmail(ctx context.Context, email string) (model.User, error)
	UserByID(ctx context.Context, id string) (model.User, error)
	UserByAPIKey(ctx context.Context, apiKey string) (model.User, error)
	CreateDevice(ctx context.Context, device model.Device) error
	ListDevices(ctx context.Context, userID string) ([]model.Device, error)
	DeviceByID(ctx context.Context, userID, id string) (model.Device, error)
	DeviceByPhone(ctx context.Context, userID, phone string) (model.Device, error)
	DeviceByIngestToken(ctx context.Context, token string) (model.Device, error)
	DeleteDevice(ctx context.Context, userID, id string) error
	InsertMessage(ctx context.Context, message model.Message) (created bool, err error)
	ListMessages(ctx context.Context, userID string, limit int) ([]model.Message, error)
	UpdateMessageByGatewayID(ctx context.Context, userID, gatewayID, status, reason string) error
	CreateWebhook(ctx context.Context, hook model.Webhook) error
	ListWebhooks(ctx context.Context, userID string) ([]model.Webhook, error)
	ListWebhooksByEvent(ctx context.Context, userID, event string) ([]model.Webhook, error)
	DeleteWebhook(ctx context.Context, userID, id string) error
}

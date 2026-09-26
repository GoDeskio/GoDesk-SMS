package model

import "time"

// User is a GoDesk account. PasswordHash is empty for display and is a bcrypt
// hash in storage. APIKey is the httpsms-compatible x-api-key credential.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	APIKey       string
	CreatedAt    time.Time
}

// Device is one SMS Gateway for Android handset. Password is sealed before it
// is stored and opened only when calling the gateway.
type Device struct {
	ID          string
	UserID      string
	Name        string
	PhoneNumber string
	BaseURL     string
	Username    string
	Password    string
	Mode        string
	IngestToken string
	CreatedAt   time.Time
}

// Message is an outbound or inbound SMS in the httpsms shape.
type Message struct {
	ID            string
	UserID        string
	DeviceID      string
	RequestID     string
	Owner         string
	Contact       string
	Content       string
	Type          string
	Status        string
	SIM           string
	GatewayID     string
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Webhook is an httpsms-compatible subscription.
type Webhook struct {
	ID         string
	UserID     string
	URL        string
	SigningKey string
	Events     []string
	CreatedAt  time.Time
}

const (
	ModeLocal   = "local"
	ModePrivate = "private"

	TypeMobileTerminated = "mobile-terminated"
	TypeMobileOriginated = "mobile-originated"

	StatusPending   = "pending"
	StatusSent      = "sent"
	StatusDelivered = "delivered"
	StatusFailed    = "failed"
	StatusReceived  = "received"

	SIM1 = "SIM1"
	SIM2 = "SIM2"

	EventMessagePhoneReceived = "message.phone.received"
)

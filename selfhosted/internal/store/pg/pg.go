package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/model"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the Postgres implementation used by docker compose.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects, applies migrations, and returns a store.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	store := &Store{pool: pool}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the pool.
func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var applied bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&applied)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) CreateUser(ctx context.Context, user model.User) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, api_key, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		user.ID, user.Email, user.PasswordHash, user.APIKey, user.CreatedAt)
	return mapConflict(err)
}

func (s *Store) UserByEmail(ctx context.Context, email string) (model.User, error) {
	return s.oneUser(ctx, `SELECT id, email, password_hash, api_key, created_at FROM users WHERE email = $1`, email)
}

func (s *Store) UserByID(ctx context.Context, id string) (model.User, error) {
	return s.oneUser(ctx, `SELECT id, email, password_hash, api_key, created_at FROM users WHERE id = $1`, id)
}

func (s *Store) UserByAPIKey(ctx context.Context, apiKey string) (model.User, error) {
	return s.oneUser(ctx, `SELECT id, email, password_hash, api_key, created_at FROM users WHERE api_key = $1`, apiKey)
}

func (s *Store) oneUser(ctx context.Context, query, arg string) (model.User, error) {
	var user model.User
	err := s.pool.QueryRow(ctx, query, arg).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.APIKey, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, store.ErrNotFound
	}
	return user, err
}

func (s *Store) CreateDevice(ctx context.Context, device model.Device) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO devices (id, user_id, name, phone_number, base_url, username, password, mode, ingest_token, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		device.ID, device.UserID, device.Name, device.PhoneNumber, device.BaseURL, device.Username, device.Password, device.Mode, device.IngestToken, device.CreatedAt)
	return mapConflict(err)
}

func (s *Store) ListDevices(ctx context.Context, userID string) ([]model.Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, name, phone_number, base_url, username, password, mode, ingest_token, created_at
		FROM devices WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Device
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, device)
	}
	return out, rows.Err()
}

func (s *Store) DeviceByID(ctx context.Context, userID, id string) (model.Device, error) {
	return s.oneDevice(ctx, `
		SELECT id, user_id, name, phone_number, base_url, username, password, mode, ingest_token, created_at
		FROM devices WHERE user_id = $1 AND id = $2`, userID, id)
}

func (s *Store) DeviceByPhone(ctx context.Context, userID, phone string) (model.Device, error) {
	return s.oneDevice(ctx, `
		SELECT id, user_id, name, phone_number, base_url, username, password, mode, ingest_token, created_at
		FROM devices WHERE user_id = $1 AND phone_number = $2`, userID, phone)
}

func (s *Store) DeviceByIngestToken(ctx context.Context, token string) (model.Device, error) {
	return s.oneDevice(ctx, `
		SELECT id, user_id, name, phone_number, base_url, username, password, mode, ingest_token, created_at
		FROM devices WHERE ingest_token = $1`, token)
}

func (s *Store) oneDevice(ctx context.Context, query string, args ...any) (model.Device, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return model.Device{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return model.Device{}, err
		}
		return model.Device{}, store.ErrNotFound
	}
	return scanDevice(rows)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDevice(row scanner) (model.Device, error) {
	var device model.Device
	err := row.Scan(&device.ID, &device.UserID, &device.Name, &device.PhoneNumber, &device.BaseURL, &device.Username, &device.Password, &device.Mode, &device.IngestToken, &device.CreatedAt)
	return device, err
}

func (s *Store) DeleteDevice(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM devices WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) InsertMessage(ctx context.Context, message model.Message) (bool, error) {
	if message.GatewayID != "" {
		var existing string
		err := s.pool.QueryRow(ctx, `SELECT id FROM messages WHERE user_id = $1 AND gateway_id = $2`, message.UserID, message.GatewayID).Scan(&existing)
		if err == nil {
			return false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO messages (id, user_id, device_id, request_id, owner, contact, content, type, status, sim, gateway_id, failure_reason, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		message.ID, message.UserID, nullIfEmpty(message.DeviceID), message.RequestID, message.Owner, message.Contact, message.Content,
		message.Type, message.Status, message.SIM, message.GatewayID, message.FailureReason, message.CreatedAt, message.UpdatedAt)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ListMessages(ctx context.Context, userID string, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, COALESCE(device_id, ''), request_id, owner, contact, content, type, status, sim, gateway_id, failure_reason, created_at, updated_at
		FROM messages WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		var message model.Message
		if err := rows.Scan(&message.ID, &message.UserID, &message.DeviceID, &message.RequestID, &message.Owner, &message.Contact, &message.Content, &message.Type, &message.Status, &message.SIM, &message.GatewayID, &message.FailureReason, &message.CreatedAt, &message.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMessageByGatewayID(ctx context.Context, userID, gatewayID, status, reason string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE messages SET status = $3, failure_reason = $4, updated_at = $5
		WHERE user_id = $1 AND gateway_id = $2`, userID, gatewayID, status, reason, time.Now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) CreateWebhook(ctx context.Context, hook model.Webhook) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO webhooks (id, user_id, url, signing_key, events, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		hook.ID, hook.UserID, hook.URL, hook.SigningKey, hook.Events, hook.CreatedAt)
	return err
}

func (s *Store) ListWebhooks(ctx context.Context, userID string) ([]model.Webhook, error) {
	return s.queryWebhooks(ctx, `SELECT id, user_id, url, signing_key, events, created_at FROM webhooks WHERE user_id = $1 ORDER BY created_at`, userID)
}

func (s *Store) ListWebhooksByEvent(ctx context.Context, userID, event string) ([]model.Webhook, error) {
	return s.queryWebhooks(ctx, `
		SELECT id, user_id, url, signing_key, events, created_at
		FROM webhooks WHERE user_id = $1 AND $2 = ANY (events) ORDER BY created_at`, userID, event)
}

func (s *Store) queryWebhooks(ctx context.Context, query string, args ...any) ([]model.Webhook, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Webhook
	for rows.Next() {
		var hook model.Webhook
		if err := rows.Scan(&hook.ID, &hook.UserID, &hook.URL, &hook.SigningKey, &hook.Events, &hook.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, hook)
	}
	return out, rows.Err()
}

func (s *Store) DeleteWebhook(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhooks WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return store.ErrConflict
	}
	return err
}

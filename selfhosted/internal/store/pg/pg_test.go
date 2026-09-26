package pg

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/model"
)

func TestMigrationsAndUsers(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	user := model.User{
		ID:           "user-1",
		Email:        "pg@example.com",
		PasswordHash: "hash",
		APIKey:       "api-key-1",
		CreatedAt:    time.Now().UTC(),
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, user); err == nil {
		t.Fatal("expected a conflict on the second insert")
	}
	loaded, err := store.UserByEmail(ctx, user.Email)
	if err != nil || loaded.APIKey != user.APIKey {
		t.Fatalf("loaded %+v err %v", loaded, err)
	}
}

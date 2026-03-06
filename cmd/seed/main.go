package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	_ = godotenv.Load()

	email := os.Getenv("SEED_EMAIL")
	password := os.Getenv("SEED_PASSWORD")
	name := os.Getenv("SEED_NAME")

	if email == "" || password == "" {
		return fmt.Errorf("SEED_EMAIL and SEED_PASSWORD are required")
	}
	if name == "" {
		name = "Admin"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	id := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO staff (id, email, name, password_hash, role)
		 VALUES ($1, $2, $3, $4, 'admin')
		 ON CONFLICT (email) DO UPDATE SET password_hash = $4, name = $3, updated_at = now()`,
		id, email, name, string(hash),
	)
	if err != nil {
		return fmt.Errorf("insert staff: %w", err)
	}

	fmt.Printf("Staff user created: %s (%s)\n", email, name)
	return nil
}

#!/bin/sh
set -e

echo "Running database migrations..."
goose -dir db/migrations postgres "$DATABASE_URL" up

echo "Starting server..."
exec ./server

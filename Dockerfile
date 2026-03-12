# Stage 1: Generate templ and build Tailwind CSS
FROM node:22-alpine AS frontend

WORKDIR /src

# Root-level Tailwind deps
COPY package.json package-lock.json ./
RUN npm ci

# Svelte checkout deps
COPY ui/checkout/package.json ui/checkout/package-lock.json ./ui/checkout/
RUN cd ui/checkout && npm ci

# Copy source for CSS and checkout builds
COPY internal/ui/ ./internal/ui/
COPY ui/checkout/ ./ui/checkout/

# Build Tailwind CSS
RUN npx @tailwindcss/cli \
    -i internal/ui/assets/css/input.css \
    -o internal/ui/assets/css/output.css \
    --minify

# Build Svelte checkout bundle
RUN cd ui/checkout && npm run build

# Stage 2: Generate templ and compile Go binary
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# Install templ and goose
RUN go install github.com/a-h/templ/cmd/templ@latest && \
    go install github.com/pressly/goose/v3/cmd/goose@latest

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy full source
COPY . .

# Copy frontend build artifacts
COPY --from=frontend /src/internal/ui/assets/css/output.css ./internal/ui/assets/css/output.css
COPY --from=frontend /src/internal/ui/assets/checkout/ ./internal/ui/assets/checkout/

# Generate templ
RUN templ generate

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./cmd/server

# Stage 3: Minimal runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary and goose
COPY --from=builder /app/server ./server
COPY --from=builder /go/bin/goose /usr/local/bin/goose

# Copy static assets
COPY --from=builder /src/internal/ui/assets/ ./internal/ui/assets/

# Copy migrations
COPY --from=builder /src/db/migrations/ ./db/migrations/

# Copy email templates
COPY --from=builder /src/internal/emailtemplates/html/ ./internal/emailtemplates/html/
COPY --from=builder /src/internal/emailtemplates/text/ ./internal/emailtemplates/text/

# Copy entrypoint
COPY entrypoint.sh ./entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["./entrypoint.sh"]

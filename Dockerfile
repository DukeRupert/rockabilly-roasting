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

# Install templ and goose. Pin templ to the same version as the library in
# go.mod — using @latest broke v1.37.0 deploy when the CLI started emitting
# calls into APIs newer than the pinned library version.
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1001 && \
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

# Build version metadata, stamped into the server binary via -ldflags. Passed
# from CI (deploy workflows) as the release tag and git SHA; default to dev.
ARG VERSION=dev
ARG COMMIT=unknown

# Build binaries
RUN CGO_ENABLED=0 GOOS=linux go build \
        -ldflags "-X github.com/dukerupert/hiri/internal/platform/build.Version=${VERSION} -X github.com/dukerupert/hiri/internal/platform/build.Commit=${COMMIT}" \
        -o /app/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -o /app/seed ./cmd/seed && \
    CGO_ENABLED=0 GOOS=linux go build -o /app/migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=linux go build -o /app/os-migrate ./cmd/os-migrate && \
    CGO_ENABLED=0 GOOS=linux go build -o /app/sentrycheck ./cmd/sentrycheck && \
    CGO_ENABLED=0 GOOS=linux go build -o /app/fix-pending-paid ./cmd/fix-pending-paid && \
    CGO_ENABLED=0 GOOS=linux go build -o /app/geocode-warm ./cmd/geocode-warm

# Stage 3: Minimal runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binaries and goose
COPY --from=builder /app/server ./server
COPY --from=builder /app/seed ./seed
COPY --from=builder /app/migrate ./migrate
COPY --from=builder /app/os-migrate ./os-migrate
COPY --from=builder /app/sentrycheck ./sentrycheck
COPY --from=builder /app/fix-pending-paid ./fix-pending-paid
# geocode-warm belongs in the image rather than being run from a checkout on
# the host: the container egresses IPv4 through the host NAT, which is the
# address the Google API key is restricted to. The host itself prefers IPv6 and
# would be denied.
COPY --from=builder /app/geocode-warm ./geocode-warm
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

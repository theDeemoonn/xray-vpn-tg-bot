# Stage 1: Build the application
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install build tools if necessary (e.g., git)
# RUN apk add --no-cache git

# Copy module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire source code
COPY . .

# Build the application binary
# CGO_ENABLED=0 for static linking (good for alpine)
# -ldflags="-w -s" strips debug information, making the binary smaller
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/main cmd/app/main.go

# Stage 2: Create the final lightweight image
FROM alpine:latest

WORKDIR /app

# Install necessary runtime dependencies (e.g., ca-certificates for HTTPS, tzdata for timezones)
RUN apk add --no-cache ca-certificates tzdata

# Copy the built binary from the builder stage
COPY --from=builder /app/main .

# Copy configuration files (optional, they can also be mounted as volumes)
COPY configs/ /app/configs/

# Expose the application port
EXPOSE 8080

# Set the entrypoint
# The application will read config from /app/configs/local.yaml by default,
# but this can be overridden by the CONFIG_PATH environment variable.
ENTRYPOINT ["/app/main"] 
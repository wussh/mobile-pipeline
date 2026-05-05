# ==========================================
# WARNING: iOS BUILD LIMITATIONS IN DOCKER
# ==========================================
# Docker runs Linux containers. You CANNOT install Xcode or run 
# `flutter build ipa` inside a Linux Docker container. 
# iOS builds MUST run directly on a macOS host.
# 
# This Dockerfile is provided if you intend to use this server
# for ANDROID ONLY (APK) on a Linux host, or if you are using 
# a macOS-based container orchestrator (like Tart or Anka).
# 
# If your goal is to run this on your Mac Mini for iOS builds,
# DO NOT use Docker. Run the compiled Go binary directly on macOS.
# ==========================================

# Step 1: Build the Go binary
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install necessary build tools
RUN apk add --no-cache make git

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
# (Change GOOS/GOARCH if cross-compiling for another system)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ios-build-server main.go

# Step 2: Runtime environment
FROM debian:bullseye-slim

WORKDIR /app

# Install runtime dependencies required by the server:
# - git: for cloning and fetching
# - curl, unzip: for fvm/flutter installation (if needed in container)
# Note: warp-cli is difficult to run in Docker as it expects systemd.
RUN apt-get update && apt-get install -y \
    git \
    curl \
    unzip \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Copy the compiled binary and web templates from the builder stage
COPY --from=builder /app/ios-build-server .
COPY --from=builder /app/web ./web

# Copy config if needed (usually mounted via volumes in production)
# COPY config.yaml .

# Ensure directories exist
RUN mkdir -p builds uploads projects

# Expose the server port
EXPOSE 8080

# Command to run the application
CMD ["./ios-build-server"]

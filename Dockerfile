# Google Cloud Run Dockerfile
FROM golang:1.23-alpine AS builder

# Install git and ca-certificates
RUN apk add --no-cache git ca-certificates

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Verify cmd directory exists (debug step)
RUN ls -la /app/cmd/cloudrun/ || (echo "ERROR: cmd/cloudrun not found" && ls -la /app/ && exit 1)

# Build the Go binary with size optimization
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o main ./cmd/cloudrun

# Stage 2: Runtime with Chrome. Alpine 3.24 ships Chromium 152; 3.18 shipped
# Chromium 119, from 2023. The scraper reads the version at startup and puts
# it in its user agent
FROM alpine:3.24

# Install Chrome dependencies (ttf-freefont is now font-freefont)
RUN apk add --no-cache \
    chromium \
    nss \
    freetype \
    freetype-dev \
    harfbuzz \
    ca-certificates \
    font-freefont \
    wget \
    unzip

# Copy the Go binary from builder stage
COPY --from=builder /app/main /app/main

# Set permissions
RUN chmod +x /app/main

# Set Chrome environment variables
ENV CHROME_BIN=/usr/bin/chromium-browser \
    CHROME_PATH=/usr/bin/chromium-browser \
    PORT=8080

# Expose port
EXPOSE 8080

# Set the CMD to your handler
CMD ["/app/main"]

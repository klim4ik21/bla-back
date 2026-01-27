# Build stage
FROM golang:1.23-alpine AS builder

ENV GOTOOLCHAIN=auto

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=auto go build -o server ./cmd/server

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates tzdata

# Copy binary
COPY --from=builder /app/server .

EXPOSE 8080

CMD ["./server"]

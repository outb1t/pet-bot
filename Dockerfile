# Stage 1: Build the Go application
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the Go application
RUN go build -o app .

# Stage 2: Build the runtime image
FROM alpine:latest

WORKDIR /app

RUN apk add --no-cache ffmpeg

# Copy the built binary from the builder stage
COPY --from=builder /app/app .
COPY web ./web

# Command to run when starting the container
CMD ["./app"]

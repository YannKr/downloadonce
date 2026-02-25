# Build stage
FROM golang:1.22-bookworm AS builder

ARG VERSION=dev

WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /downloadonce ./cmd/server

# Runtime stage
FROM debian:trixie-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    imagemagick \
    fonts-dejavu-core \
    ca-certificates \
    python3 \
    python3-pip \
    python3-venv \
    tesseract-ocr \
    && rm -rf /var/lib/apt/lists/*

RUN python3 -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir \
       invisible-watermark opencv-python-headless

COPY --from=builder /downloadonce /usr/local/bin/downloadonce

RUN mkdir -p /data
ENV DATA_DIR=/data
ENV FONT_PATH=/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf
ENV VENV_PATH=/opt/venv
ENV LISTEN_ADDR=:8080

EXPOSE 8080
VOLUME /data

ENTRYPOINT ["downloadonce"]

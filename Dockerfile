# syntax=docker/dockerfile:1

FROM golang:1.26 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN go build -o /telegram-media-server ./cmd/telegram-media-server

FROM ubuntu:26.04 AS runtime

# yt-dlp via pip: always correct for the runtime architecture, no arch detection needed.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    aria2 \
    ffmpeg \
    ca-certificates \
    dnsutils \
    net-tools \
    iputils-ping \
    python3 \
    python3-pip \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && pip3 install --no-cache-dir --break-system-packages yt-dlp

RUN update-ca-certificates
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV YTDLP_PATH=/usr/local/bin/yt-dlp
ENV YTDLP_UPDATE_MODE=pip
ENV YTDLP_PYTHON_PATH=/usr/bin/python3

COPY --from=builder /telegram-media-server /telegram-media-server
COPY locales /app/locales

RUN mkdir -p /app/media

WORKDIR /app

CMD ["/telegram-media-server"]

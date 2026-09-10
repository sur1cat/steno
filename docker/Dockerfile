# Бот: Chromium под Xvfb, PulseAudio для звука, ffmpeg для записи и тот же
# бинарник steno в роли `steno bot`.

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /steno ./cmd/steno

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      chromium \
      ffmpeg \
      pulseaudio \
      xvfb \
      dbus-x11 \
      ca-certificates \
      fonts-liberation fonts-noto-core fonts-noto-color-emoji \
 && rm -rf /var/lib/apt/lists/*

COPY --from=build /steno /usr/local/bin/steno
COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh \
 && useradd -m -u 1000 steno \
 && mkdir -p /out && chown steno:steno /out \
 && mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix

USER steno
ENV DISPLAY=:99 \
    STENO_CHROME_PATH=/usr/bin/chromium \
    STENO_AUDIO_SOURCE=meet_out.monitor \
    HOME=/home/steno

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["steno", "bot", "--help"]

#!/usr/bin/env bash
set -euo pipefail

# Meet в headless ведёт себя хуже: часть элементов не рисуется, а иногда
# приходит упрощённая версия страницы. Поэтому настоящий X-сервер.
Xvfb :99 -screen 0 1280x720x24 -nolisten tcp &
XVFB_PID=$!

# Без сессионной шины Chromium сыпет в лог ошибками подключения к dbus.
eval "$(dbus-launch --sh-syntax)"
export DBUS_SESSION_BUS_ADDRESS DBUS_SESSION_BUS_PID

pulseaudio -D --exit-idle-time=-1 --disallow-exit --disable-shm 2>/dev/null
for _ in $(seq 20); do pactl info >/dev/null 2>&1 && break; sleep 0.25; done

# Звук созвона: Chromium играет в meet_out, ffmpeg пишет его монитор.
pactl load-module module-null-sink \
  sink_name=meet_out sink_properties=device.description=meet_out >/dev/null

# «Микрофон» бота — идеальная тишина. Заглушка Chrome
# (--use-fake-device-for-media-stream) вместо этого подаёт пищащий тон,
# который слышат все участники.
pactl load-module module-null-sink \
  sink_name=mic_sink sink_properties=device.description=mic_sink >/dev/null
pactl load-module module-remap-source \
  source_name=virtmic master=mic_sink.monitor \
  source_properties=device.description=virtmic >/dev/null

pactl set-default-sink meet_out
pactl set-default-source virtmic

cleanup() { kill "$XVFB_PID" 2>/dev/null || true; }
trap cleanup EXIT

exec "$@"

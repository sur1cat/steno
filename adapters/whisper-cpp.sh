#!/usr/bin/env bash
# Адаптер расшифровки для whisper.cpp.
#
# Контракт: получить путь к аудио и код языка, напечатать в stdout
#   {"segments":[{"start":1.2,"end":3.4,"text":"..."}]}
#
# Нужны: whisper.cpp (whisper-cli), ffmpeg, jq.
#   WHISPER_BIN    путь к whisper-cli        (по умолчанию whisper-cli в PATH)
#   WHISPER_MODEL  путь к ggml-модели        (по умолчанию ggml-large-v3.bin)
set -euo pipefail

AUDIO="${1:?нужен путь к аудио}"
# Пустой язык означает автоопределение, а не русский: language:"" в конфиге —
# это осознанный выбор для созвонов, где переходят с языка на язык.
LANG_CODE="${2:-}"
[ -z "$LANG_CODE" ] && LANG_CODE="auto"
BIN="${WHISPER_BIN:-whisper-cli}"
MODEL="${WHISPER_MODEL:-$HOME/.cache/whisper/ggml-large-v3.bin}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# whisper.cpp хочет 16 кГц моно PCM.
ffmpeg -nostdin -loglevel error -i "$AUDIO" -ac 1 -ar 16000 -c:a pcm_s16le "$TMP/a.wav"

"$BIN" -m "$MODEL" -l "$LANG_CODE" -f "$TMP/a.wav" -oj -of "$TMP/out" >&2

# whisper.cpp отдаёт offsets в миллисекундах — переводим в секунды.
jq '{segments: [.transcription[] | {
      start: (.offsets.from / 1000),
      end:   (.offsets.to   / 1000),
      text:  (.text | gsub("^\\s+|\\s+$"; ""))
    }] | map(select(.text != ""))}' "$TMP/out.json"

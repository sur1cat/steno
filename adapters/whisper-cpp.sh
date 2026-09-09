#!/usr/bin/env bash
# Адаптер расшифровки для whisper.cpp.
#
# Контракт: получить путь к аудио и код языка, напечатать в stdout
#   {"segments":[{"start":1.2,"end":3.4,"text":"..."}]}
# Всё остальное — в stderr: сервис разбирает stdout как JSON целиком.
#
# Нужны: whisper.cpp (whisper-cli), ffmpeg, jq.
#   WHISPER_BIN      путь к whisper-cli   (по умолчанию whisper-cli в PATH)
#   WHISPER_MODEL    путь к ggml-модели   (по умолчанию — лучшая из найденных
#                                          в WHISPER_MODEL_DIR)
#   WHISPER_MODEL_DIR где искать модели   (по умолчанию ~/.cache/whisper)
#   WHISPER_THREADS  сколько потоков      (по умолчанию по числу ядер)
#   WHISPER_VAD_MODEL модель VAD          (по умолчанию ggml-silero-*.bin
#                                          из WHISPER_MODEL_DIR, если лежит)
#
# VAD стоит положить: 900 КБ, и на записи созвона он решает две задачи разом —
# выкидывает тишину до прихода людей, на которой whisper иначе сочиняет текст,
# и не тратит время на её расшифровку.
#   curl -L -o ~/.cache/whisper/ggml-silero-v5.1.2.bin \
#     https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin
set -euo pipefail

AUDIO="${1:?нужен путь к аудио}"
# Пустой язык означает автоопределение, а не русский: language:"" в конфиге —
# это осознанный выбор для созвонов, где переходят с языка на язык.
LANG_CODE="${2:-}"
[ -z "$LANG_CODE" ] && LANG_CODE="auto"
BIN="${WHISPER_BIN:-whisper-cli}"
MODEL_DIR="${WHISPER_MODEL_DIR:-$HOME/.cache/whisper}"

# Модель не выбирается одним жёстким именем: за large-v3 надо отдать 3 ГБ, и
# на машине, где лежит только small, адаптер раньше просто падал. Берём лучшую
# из тех, что есть, а какая именно — печатаем в stderr, чтобы разница в
# качестве расшифровки не выглядела необъяснимой.
if [ -z "${WHISPER_MODEL:-}" ]; then
  for m in ggml-large-v3 ggml-large-v3-turbo ggml-large-v2 ggml-large \
           ggml-medium ggml-small ggml-base ggml-tiny; do
    if [ -f "$MODEL_DIR/$m.bin" ]; then
      WHISPER_MODEL="$MODEL_DIR/$m.bin"
      break
    fi
  done
fi
if [ -z "${WHISPER_MODEL:-}" ] || [ ! -f "$WHISPER_MODEL" ]; then
  echo "whisper: не нашёл ggml-модель в $MODEL_DIR" >&2
  echo "  → mkdir -p $MODEL_DIR && curl -L -o $MODEL_DIR/ggml-large-v3.bin \\" >&2
  echo "      https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3.bin" >&2
  echo "  → или укажи свою: WHISPER_MODEL=/путь/до/ggml-*.bin" >&2
  exit 1
fi
MODEL="$WHISPER_MODEL"
echo "whisper: модель $(basename "$MODEL")" >&2

# Число ядер. WHISPER_THREADS ставит сервис: расшифровка не должна занимать
# машину целиком, если она же используется для работы.
THREADS="${WHISPER_THREADS:-$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 4)}"

# VAD не обязателен: без него адаптер работает как раньше, только хуже и
# медленнее. Поэтому не падаем, а один раз говорим, чего не хватает.
VAD_ARGS=()
if [ -z "${WHISPER_VAD_MODEL:-}" ]; then
  for v in "$MODEL_DIR"/ggml-silero-*.bin; do
    if [ -f "$v" ]; then
      WHISPER_VAD_MODEL="$v"
      break
    fi
  done
fi
if [ -n "${WHISPER_VAD_MODEL:-}" ] && [ -f "$WHISPER_VAD_MODEL" ]; then
  VAD_ARGS=(--vad -vm "$WHISPER_VAD_MODEL")
else
  echo "whisper: VAD-модели нет в $MODEL_DIR — тишина пойдёт в расшифровку," >&2
  echo "  и на ней whisper выдумывает текст. Ставится одной командой:" >&2
  echo "  curl -L -o $MODEL_DIR/ggml-silero-v5.1.2.bin \\" >&2
  echo "    https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin" >&2
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# whisper.cpp хочет 16 кГц моно PCM.
ffmpeg -nostdin -loglevel error -i "$AUDIO" -ac 1 -ar 16000 -c:a pcm_s16le "$TMP/a.wav"

# Язык whisper определяет по первым 30 секундам — а первые минуты записи
# созвона это тишина: бот заходит раньше людей и пишет пустое лобби. На такой
# тишине определение выдаёт случайный язык с уверенностью около половины, и
# весь дальнейший разговор уходит в [BLANK_AUDIO]. Поэтому язык определяем по
# куску, из которого вырезана тишина, а расшифровываем — исходный файл целиком,
# иначе поедут таймкоды.
if [ "$LANG_CODE" = "auto" ]; then
  if ffmpeg -nostdin -loglevel error -i "$TMP/a.wav" \
      -af "silenceremove=start_periods=1:start_threshold=-40dB:start_duration=0:stop_periods=-1:stop_threshold=-40dB:stop_duration=0.5" \
      -t 120 -c:a pcm_s16le "$TMP/dense.wav" 2>/dev/null &&
     [ -s "$TMP/dense.wav" ] &&
     [ "$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$TMP/dense.wav" 2>/dev/null | cut -d. -f1)" -ge 2 ] 2>/dev/null; then
    DETECT_SRC="$TMP/dense.wav"
  else
    DETECT_SRC="$TMP/a.wav" # речи почти нет — определять всё равно не по чему
  fi
  DET="$("$BIN" -m "$MODEL" -t "$THREADS" -l auto -dl -f "$DETECT_SRC" 2>&1 |
         sed -n 's/.*auto-detected language: \([a-z][a-z]*\).*/\1/p' | tail -1)"
  if [ -n "$DET" ]; then
    echo "whisper: язык определён как $DET" >&2
    LANG_CODE="$DET"
  fi
fi

# -mc 0 — не тащить текст предыдущего окна в следующее. С контекстом whisper на
# тишине сваливается в петлю («Редактор субтитров ...» сорок раз подряд) и
# дальше повторяет её вместо речи: на этой записи петля съедала весь разговор.
"$BIN" -m "$MODEL" -t "$THREADS" -mc 0 -l "$LANG_CODE" \
  "${VAD_ARGS[@]+"${VAD_ARGS[@]}"}" -f "$TMP/a.wav" -oj -of "$TMP/out" >&2

# whisper.cpp отдаёт offsets в миллисекундах — переводим в секунды.
# Заодно выкидываем служебные пометки: на тишине и музыке whisper печатает
# [BLANK_AUDIO], [музыка], (Music). Это не речь, а в follow-up они уходят
# наравне с репликами. Сегмент выкидываем, только если кроме таких пометок в
# нём ничего нет: «[шум] и мы начали» — всё ещё реплика.
jq '
def clean: gsub("\\[[^\\]]*\\]"; "") | gsub("\\([^)]*\\)"; "") | gsub("\\*[^*]*\\*"; "");
{segments: [.transcription[] | {
      start: (.offsets.from / 1000),
      end:   (.offsets.to   / 1000),
      text:  (.text | gsub("^\\s+|\\s+$"; ""))
    }] | map(select((.text | clean | gsub("[\\s[:punct:]]"; "")) != ""))}' "$TMP/out.json"

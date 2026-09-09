#!/usr/bin/env bash
# Адаптер расшифровки через Groq.
#
# Groq крутит ту же самую whisper-large-v3, что и локальный whisper.cpp, но на
# своём железе и по $0.04 за час звука. Для команды на 160 часов созвонов в
# месяц это около шести долларов — против примерно двух тысяч за выделенную
# виртуалку с T4, которая при этом простаивает 97% времени. Модель та же,
# значит и качество то же.
#
# Чем платим: аудио уходит наружу. Расшифровка и так уходит в Claude, но запись
# — шаг дальше, и это осознанный размен, а не бесплатный выигрыш. Если записи
# не должны покидать контур — остаётся whisper.cpp на своём железе.
#
# Контракт тот же, что у остальных адаптеров: получить путь к аудио и код языка,
# напечатать в stdout {"segments":[{"start":1.2,"end":3.4,"text":"..."}]}
#
# Нужны: ffmpeg, jq, curl и GROQ_API_KEY.
#   GROQ_MODEL  модель (по умолчанию whisper-large-v3)
set -euo pipefail

AUDIO="${1:?нужен путь к аудио}"
LANG_CODE="${2:-}"
MODEL="${GROQ_MODEL:-whisper-large-v3}"

if [ -z "${GROQ_API_KEY:-}" ]; then
  echo "groq: пуста переменная GROQ_API_KEY" >&2
  echo "  → ключ заводится на console.groq.com/keys" >&2
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

# Groq берёт файл целиком и ограничивает его размер. Час созвона в opus 32k —
# это около 14 МБ, влезает с запасом; на всякий случай ужимаем до 16 кГц моно,
# больше для распознавания речи и не нужно.
ffmpeg -nostdin -loglevel error -i "$AUDIO" -ac 1 -ar 16000 -c:a libopus -b:a 24k \
  "$TMP/a.ogg"

SIZE=$(wc -c < "$TMP/a.ogg" | tr -d ' ')
echo "groq: модель $MODEL, отправляю $((SIZE / 1024)) КБ" >&2

ARGS=(-sS --fail-with-body
  -H "Authorization: Bearer $GROQ_API_KEY"
  -F "file=@$TMP/a.ogg"
  -F "model=$MODEL"
  -F "response_format=verbose_json"
  # Слово-в-слово, без «улучшений»: дальше по этому тексту делается follow-up,
  # и переформулировки модели там ни к чему.
  -F "temperature=0")

# Пустой язык — автоопределение. На созвоне, где переходят с русского на
# английский, жёстко заданный язык заставляет модель переводить, а не
# расшифровывать.
[ -n "$LANG_CODE" ] && ARGS+=(-F "language=$LANG_CODE")

if ! curl "${ARGS[@]}" \
     https://api.groq.com/openai/v1/audio/transcriptions > "$TMP/out.json" 2> "$TMP/err"; then
  echo "groq: запрос не прошёл" >&2
  # Ключ в заголовке, а не в URL, поэтому в тело ошибки он не попадает.
  head -c 500 "$TMP/out.json" >&2 2>/dev/null || true
  cat "$TMP/err" >&2 2>/dev/null || true
  exit 1
fi

jq -e '.segments' "$TMP/out.json" >/dev/null 2>&1 || {
  echo "groq: в ответе нет сегментов" >&2
  head -c 500 "$TMP/out.json" >&2
  exit 1
}

DET=$(jq -r '.language // empty' "$TMP/out.json")
[ -n "$DET" ] && echo "groq: язык $DET" >&2

jq '{segments: [.segments[] | {
      start: .start,
      end:   .end,
      text:  (.text | gsub("^\\s+|\\s+$"; ""))
    }] | map(select(.text != ""))}' "$TMP/out.json"

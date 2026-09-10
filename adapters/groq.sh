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

# Сообщения адаптера идут на языке steno: STENO_LANG=ru — по-русски, иначе
# по-английски. Первым аргументом английский текст, вторым русский.
say() {
  case "${STENO_LANG:-}" in
    ru*) printf '%s\n' "$2" >&2 ;;
    *)   printf '%s\n' "$1" >&2 ;;
  esac
}


AUDIO="${1:?path to the audio file is required}"
LANG_CODE="${2:-}"
MODEL="${GROQ_MODEL:-whisper-large-v3}"

if [ -z "${GROQ_API_KEY:-}" ]; then
  say "groq: GROQ_API_KEY is empty" "groq: пуста переменная GROQ_API_KEY"
  say "  → a key is created at console.groq.com/keys" "  → ключ заводится на console.groq.com/keys"
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
say "groq: model $MODEL, sending $((SIZE / 1024)) KB" "groq: модель $MODEL, отправляю $((SIZE / 1024)) КБ"

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

# Словарь созвона: имена людей и названия сервисов, которых нет в словаре
# модели. У Groq это поле API prompt — та же initial prompt, что и --prompt у
# whisper.cpp, с тем же потолком в 224 токена. Про перенос подсказки из окна в
# окно решать не приходится: файл уходит целиком, и подсказку к окнам
# приставляет сама их сторона.
[ -n "${STENO_PROMPT:-}" ] && ARGS+=(-F "prompt=$STENO_PROMPT")

if ! curl "${ARGS[@]}" \
     https://api.groq.com/openai/v1/audio/transcriptions > "$TMP/out.json" 2> "$TMP/err"; then
  say "groq: the request failed" "groq: запрос не прошёл"
  # Ключ в заголовке, а не в URL, поэтому в тело ошибки он не попадает.
  head -c 500 "$TMP/out.json" >&2 2>/dev/null || true
  cat "$TMP/err" >&2 2>/dev/null || true
  exit 1
fi

jq -e '.segments' "$TMP/out.json" >/dev/null 2>&1 || {
  say "groq: no segments in the answer" "groq: в ответе нет сегментов"
  head -c 500 "$TMP/out.json" >&2
  exit 1
}

DET=$(jq -r '.language // empty' "$TMP/out.json")
[ -n "$DET" ] && say "groq: language $DET" "groq: язык $DET"

jq '{segments: [.segments[] | {
      start: .start,
      end:   .end,
      text:  (.text | gsub("^\\s+|\\s+$"; ""))
    }] | map(select(.text != ""))}' "$TMP/out.json"

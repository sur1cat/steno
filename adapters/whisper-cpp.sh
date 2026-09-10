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
#   WHISPER_THREADS  сколько потоков      (по умолчанию по числу
#                                          производительных ядер)
#   WHISPER_DETECT_MODEL модель для определения языка (по умолчанию основная;
#                                          мелкая экономит ~10 с на прогон)
#   WHISPER_VAD_MODEL модель VAD          (по умолчанию ggml-silero-*.bin
#                                          из WHISPER_MODEL_DIR, если лежит)
#   STENO_PROMPT     словарь созвона: имена людей и названия сервисов через
#                    запятую. Уходит в --prompt. Пусто или не задано — whisper
#                    зовётся ровно теми же ключами, что и раньше.
#
# VAD стоит положить: 900 КБ, и на записи созвона он решает две задачи разом —
# выкидывает тишину до прихода людей, на которой whisper иначе сочиняет текст,
# и не тратит время на её расшифровку.
#   curl -L -o ~/.cache/whisper/ggml-silero-v5.1.2.bin \
#     https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin
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
  # Порядок — по замеру на записи реального созвона, а не по размеру файла.
  # Quantized-версии (-q5_0) весят втрое меньше и считают в полтора раза
  # быстрее, а текст выдают тот же — поэтому large-v3-q5_0 стоит выше полной
  # large-v3. А turbo, хоть и быстрее всех, стоит ниже обеих: на том же куске
  # записи она зацикливается («Cloud, Cloud, Cloud» семнадцать раз подряд) и
  # подменяет незнакомое название похожим знакомым — Plaud превращается в
  # Cloud AI. Это не мусор, который видно глазом: follow-up по такому тексту
  # уверенно напишет про облако, которого в разговоре не было.
  for m in ggml-large-v3-q5_0 ggml-large-v3-q8_0 ggml-large-v3 \
           ggml-large-v2 ggml-large \
           ggml-large-v3-turbo-q5_0 ggml-large-v3-turbo-q8_0 ggml-large-v3-turbo \
           ggml-medium-q5_0 ggml-medium ggml-small ggml-base ggml-tiny; do
    if [ -f "$MODEL_DIR/$m.bin" ]; then
      WHISPER_MODEL="$MODEL_DIR/$m.bin"
      break
    fi
  done
fi
if [ -z "${WHISPER_MODEL:-}" ] || [ ! -f "$WHISPER_MODEL" ]; then
  say "whisper: no ggml model found in $MODEL_DIR" "whisper: не нашёл ggml-модель в $MODEL_DIR"
  echo "  → mkdir -p $MODEL_DIR && curl -L -o $MODEL_DIR/ggml-large-v3.bin \\" >&2
  echo "      https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3.bin" >&2
  say "  → or point at your own: WHISPER_MODEL=/path/to/ggml-*.bin" "  → или укажи свою: WHISPER_MODEL=/путь/до/ggml-*.bin"
  exit 1
fi
MODEL="$WHISPER_MODEL"
say "whisper: model $(basename "$MODEL")" "whisper: модель $(basename "$MODEL")"

# Число потоков. WHISPER_THREADS ставит сервис: расшифровка не должна занимать
# машину целиком, если она же используется для работы.
#
# По умолчанию берём только производительные ядра, а не все. На M3 (4P+4E)
# восемь потоков оказались медленнее четырёх — 47.6 с против 44.8 с — при вдвое
# большем расходе CPU: энергоэффективные ядра тормозят общий барьер, на котором
# ждут остальные. hw.perflevel0.logicalcpu есть только на Apple Silicon, на
# Intel-маке и на Linux откатываемся на общее число ядер.
THREADS="${WHISPER_THREADS:-$(sysctl -n hw.perflevel0.logicalcpu 2>/dev/null \
  || sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 4)}"

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
  say "whisper: no VAD model in $MODEL_DIR — silence goes into the transcript," "whisper: VAD-модели нет в $MODEL_DIR — тишина пойдёт в расшифровку,"
  say "  and whisper invents text on it. One command installs it:" "  и на ней whisper выдумывает текст. Ставится одной командой:"
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
  # Отдельная модель под определение языка: задача грубая (ru или en), а
  # вторая загрузка large-v3 стоит 11 с из 28 с всего прогона. Задаётся явно —
  # сами на мелкую не переключаемся, ошибка определения испортит всю расшифровку.
  DETECT_MODEL="${WHISPER_DETECT_MODEL:-$MODEL}"
  # Вывод в файл, а не в подстановку: под set -e + pipefail упавший whisper
  # ронял весь адаптер с кодом 1 и без единого слова в лог — stderr уходил в
  # $(...) и там же выбрасывался sed'ом. Язык не определился — не беда, whisper
  # определит его сам на основном проходе; а вот молча умереть — беда.
  if "$BIN" -m "$DETECT_MODEL" -t "$THREADS" -l auto -dl \
       -f "$DETECT_SRC" >"$TMP/detect.log" 2>&1; then
    DET="$(sed -n 's/.*auto-detected language: \([a-z][a-z]*\).*/\1/p' \
             "$TMP/detect.log" | tail -1)"
  else
    say "whisper: could not detect the language, transcribing with auto:" "whisper: определить язык не вышло, расшифровываю с auto:"
    tail -5 "$TMP/detect.log" >&2
    DET=""
  fi
  if [ -n "$DET" ]; then
    say "whisper: language detected as $DET" "whisper: язык определён как $DET"
    LANG_CODE="$DET"
  fi
fi

# Словарь созвона. Имена людей и названия сервисов, которых нет в словаре
# модели: без подсказки whisper подменяет их похожими обычными словами —
# «Орынгали нужно закончить Сапар» становится «Анвару нужно закончить сапар».
#
# И здесь же — единственное место, где приходится трогать -mc, поэтому длинно.
#
# -mc 0 стоит не просто так: с контекстом whisper на тишине сваливается в петлю
# («Редактор субтитров ...» сорок раз подряд) и дальше повторяет её вместо речи.
# Но -mc 0 заодно молча выключает и --prompt: в whisper.cpp подсказка живёт в
# той же истории, а история берётся под условием n_max_text_ctx > 0. Проверено:
# с -mc 0 расшифровка с подсказкой и без неё совпадает байт в байт.
#
# Поэтому при подсказке -mc поднимаем — но ровно на её длину. Внутри whisper.cpp
# бюджет истории делится так:
#
#   max_prompt_ctx = min(n_max_text_ctx, n_text_ctx/2)     // 224 у всех моделей
#   взято из подсказки  = min(токенов подсказки, max_prompt_ctx - 1)
#   взято из прошлого окна = max_prompt_ctx - взято_из_подсказки - 1
#
# То есть -mc = (токенов подсказки + 1) даёт ноль токенов прошлого окна:
# словарь виден всегда, а текст, на котором whisper зацикливался, не переносится
# по-прежнему. Ровно то же свойство, что у -mc 0, только словарь проходит.
#
# --carry-initial-prompt обязателен. Без него подсказка кладётся в «прошлое
# окно», которое whisper переписывает после каждых тридцати секунд, — и словарь
# действует только на первые полминуты созвона. С ним подсказка лежит отдельно
# и подставляется в каждое окно. Стоит это тех же нескольких десятков токенов
# в каждом окне, то есть ничего.
#
# Токены не считаем точно — токенизатор внутри модели. Оценка по байтам: и
# кириллица (2 байта на символ, ~2 символа на токен), и латиница (1 байт, ~4
# символа) дают около 0.3 токена на байт. Оценка нарочно щедрая: если её не
# хватит, whisper обрежет подсказку сам — и обрежет с начала, по именам людей.
PROMPT_ARGS=()
if [ -n "${STENO_PROMPT:-}" ]; then
  BYTES=$(printf '%s' "$STENO_PROMPT" | LC_ALL=C wc -c | tr -d ' ')
  ITEMS=$(printf '%s' "$STENO_PROMPT" | tr -cd ',' | wc -c | tr -d ' ')
  MC=$(( (3 * BYTES) / 10 + ITEMS + 2 ))
  [ "$MC" -gt 224 ] && MC=224
  PROMPT_ARGS=(--prompt "$STENO_PROMPT" --carry-initial-prompt -mc "$MC")
  say "whisper: a $MC-token vocabulary: $STENO_PROMPT" "whisper: словарь на $MC токенов: $STENO_PROMPT"
else
  PROMPT_ARGS=(-mc 0)
fi

"$BIN" -m "$MODEL" -t "$THREADS" "${PROMPT_ARGS[@]}" -l "$LANG_CODE" \
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

#!/usr/bin/env bash
# Адаптер модели для Ollama — и заодно образец, с которого списывают свой.
#
# Контракт: получить запрос одним объектом JSON в stdin, напечатать ответ одним
# объектом JSON в stdout. Всё остальное — в stderr: сервис разбирает stdout как
# JSON целиком. Не получилось — ненулевой код возврата и объяснение в stderr.
#
#   steno → сюда, в stdin:
#     {"system":"правила разбора",
#      "user":"шапка и расшифровка созвона",
#      "schema":{…}|null,      — JSON Schema ответа; null — свободный текст
#      "model":"llama3.1",     — из llm.model; пусто — на усмотрение адаптера
#      "max_tokens":16000}     — 0 — на усмотрение адаптера
#
#   сюда → steno, в stdout:
#     {"text":"ответ модели",  — обязателен; со схемой — объект JSON строкой
#      "usage":{"input":0,"output":0,"cache_read":0,"cache_write":0},
#      "usd":0.0,              — необязательно; НЕ ТО ЖЕ, что отсутствие поля
#      "model":"llama3.1:8b"}  — необязательно: чем на самом деле считали
#
# Про usd отдельно, потому что тут легко соврать. "usd": 0 означает
# «бесплатно, и я это знаю» — так и есть у модели на своей машине. Поля нет
# вовсе — «не знаю», и steno честно напишет в `steno cost`, что цена
# неизвестна. Ставить ноль там, где счёт придёт, — худшее из двух.
#
# Промпт приходит в stdin, а не аргументом, по той же причине, по какой у
# расшифровки в аргументе лежит путь к файлу, а не сам файл: расшифровка
# часового созвона — десятки килобайт, в командную строку она не влезает.
#
# Включается это так:
#   "brain": {"provider": "command"},
#   "llm":   {"cmd": ["./adapters/ollama.sh"], "model": "llama3.1"}
#
# Нужны: ollama, curl, jq.
#   OLLAMA_HOST   адрес сервера   (по умолчанию http://127.0.0.1:11434)
#   OLLAMA_MODEL  модель, если её не задали в llm.model
#   STENO_LANG    язык сообщений этого скрипта; ставит steno
#
# Схему Ollama принимает полем format и генерацию по ней ограничивает жёстко —
# то есть строгий вывод здесь настоящий, а не просьба словами.
set -euo pipefail

# Сообщения адаптера идут на языке steno: STENO_LANG=ru — по-русски, иначе
# по-английски. Первым аргументом английский текст, вторым русский.
say() {
  case "${STENO_LANG:-}" in
    ru*) printf '%s\n' "$2" >&2 ;;
    *)   printf '%s\n' "$1" >&2 ;;
  esac
}

need() {
  command -v "$1" >/dev/null 2>&1 && return 0
  say "$1 is required: $2" "нужен $1: $2"
  exit 1
}
need curl "brew install curl"
need jq "brew install jq"

HOST="${OLLAMA_HOST:-http://127.0.0.1:11434}"
# Ollama печатает адрес без схемы, когда его берут из своего же окружения.
case "$HOST" in http://*|https://*) ;; *) HOST="http://$HOST" ;; esac

REQ=$(cat)
if [ -z "$REQ" ]; then
  say "empty request on stdin" "пустой запрос в stdin"
  exit 1
fi

MODEL=$(printf '%s' "$REQ" | jq -r '.model // ""')
[ -z "$MODEL" ] && MODEL="${OLLAMA_MODEL:-}"
if [ -z "$MODEL" ]; then
  say "no model: set llm.model in the config or OLLAMA_MODEL in the environment" \
      "не выбрана модель: задай llm.model в конфиге или OLLAMA_MODEL в окружении"
  exit 1
fi

# Системную и пользовательскую части не склеиваем: Ollama различает роли, и
# правила разбора должны стоять там, куда модель смотрит как на правила.
#
# format — это и есть строгий вывод: Ollama ограничивает генерацию схемой, а не
# просит соблюсти её словами. Схемы нет — поля нет вовсе, иначе Ollama потребует
# JSON и там, где нужен обычный текст (справка о проекте).
BODY=$(printf '%s' "$REQ" | jq -c --arg model "$MODEL" '
  {
    model: $model,
    stream: false,
    messages: [
      {role: "system", content: .system},
      {role: "user",   content: .user}
    ]
  }
  + (if (.max_tokens // 0) > 0 then {options: {num_predict: .max_tokens}} else {} end)
  + (if .schema then {format: .schema} else {} end)
')

# --fail-with-body: без него curl на четырёхсотке отдаёт тело и нулевой код, и
# отказ Ollama («model not found») уезжал бы в steno как ответ модели. Код
# возврата запоминаем, но выводы делаем после разбора тела: «не запущен» и
# «ответил отказом» — разные беды, и лечатся они по-разному.
set +e
# stderr curl'а не подмешиваем в тело: «curl: (22) …» рядом с телом ответа
# делает его неразбираемым, и отказ Ollama становится не виден.
RESP=$(printf '%s' "$BODY" | curl -sS --fail-with-body \
  -H 'Content-Type: application/json' --data-binary @- "$HOST/api/chat")
CURL_RC=$?
set -e

ERR=$(printf '%s' "$RESP" | jq -r '.error // empty' 2>/dev/null || true)
if [ -n "$ERR" ]; then
  say "ollama refused: $ERR" "ollama отказал: $ERR"
  # Самая частая причина — модель не скачана, и сказать это надо прямо.
  case "$ERR" in
    *"not found"*|*"не найдена"*)
      say "pull it first: ollama pull $MODEL" "скачай её: ollama pull $MODEL" ;;
  esac
  exit 1
fi
if [ "$CURL_RC" -ne 0 ]; then
  say "ollama at $HOST did not answer — is it running? (ollama serve)" \
      "ollama по адресу $HOST не ответил — он запущен? (ollama serve)"
  printf '%s\n' "$RESP" >&2
  exit 1
fi

TEXT=$(printf '%s' "$RESP" | jq -r '.message.content // ""')
if [ -z "$TEXT" ]; then
  say "ollama returned an empty answer" "ollama вернул пустой ответ"
  printf '%s\n' "$RESP" >&2
  exit 1
fi

# usd: 0 — это утверждение, а не заглушка: модель считает на этой же машине, и
# счёта за неё не будет. Токены Ollama отдаёт своими полями prompt_eval_count и
# eval_count; нет их — отдаём нули, steno покажет расход без токенов.
printf '%s' "$RESP" | jq -c '{
  text:  .message.content,
  usage: {input: (.prompt_eval_count // 0), output: (.eval_count // 0),
          cache_read: 0, cache_write: 0},
  usd:   0,
  model: (.model // "")
}'

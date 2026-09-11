#!/usr/bin/env bash
# Адаптер публикации в Discord через вебхук.
#
# Контракт: получить созвон одним JSON на stdin, напечатать в stdout ссылку на
# опубликованное — и больше в stdout ничего. Код 0 значит «опубликовано», любой
# другой — «нет». Всё остальное — в stderr: сервис кладёт его в лог и в колонку
# publications.error, то есть в то, что человек увидит, когда спросит «почему не
# ушло».
#
# Что лежит в JSON (посмотреть целиком: этот адаптер с DISCORD_WEBHOOK_URL=- ):
#   .meeting.id .title .url .started_at .ended_at .duration_sec
#           .participants[] .projects[]
#   .followup.title .tldr[] .action_items[] .decisions[] .open_questions[]
#           .risks[] .timeline[]        — разбор как есть, теми же полями,
#                                         какими его отдаёт модель
#   .text.markdown .plain .html         — тот же follow-up уже свёрстанным
#   .links.google_doc                   — если документ создался
#   .transcript                         — расшифровка целиком, с таймкодами
#
# Нужны: curl, jq.
#   DISCORD_WEBHOOK_URL  вебхук канала. Заводится в Discord без приложения и без
#                        токена: Настройки канала → Интеграции → Вебхуки →
#                        Создать вебхук → Копировать URL.
#
# Вебхук — это и есть ключ от канала: кто узнал ссылку, тот пишет в канал от
# имени бота. Поэтому он живёт в .env рядом с остальными секретами, а не в
# steno.json и не в панели.
set -euo pipefail

# Сообщения адаптера идут на языке steno: STENO_LANG=ru — по-русски, иначе
# по-английски. Первым аргументом английский текст, вторым русский.
say() {
  case "${STENO_LANG:-}" in
    ru*) printf '%s\n' "$2" >&2 ;;
    *)   printf '%s\n' "$1" >&2 ;;
  esac
}

if [ -z "${DISCORD_WEBHOOK_URL:-}" ]; then
  say "discord: DISCORD_WEBHOOK_URL is empty" "discord: пуста переменная DISCORD_WEBHOOK_URL"
  say "  → channel settings → Integrations → Webhooks → New Webhook → Copy URL" \
     "  → настройки канала → Интеграции → Вебхуки → Создать вебхук → Копировать URL"
  say "  → then put it into .env next to steno.json" \
     "  → потом впиши её в .env рядом с steno.json"
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

cat > "$TMP/in.json"

# DISCORD_WEBHOOK_URL=- показывает, что вообще приезжает адаптеру. Отладочная
# петля, без которой первый свой адаптер пишется вслепую.
if [ "$DISCORD_WEBHOOK_URL" = "-" ]; then
  jq . "$TMP/in.json" >&2
  exit 1
fi

MD="$(jq -r '.text.markdown' "$TMP/in.json")"

# У Discord потолок — 2000 символов на сообщение. Режем по границам строк и,
# если не влезло, докладываем follow-up целиком файлом: обрезанный на середине
# список задач хуже, чем список задач и вложение.
#
# Бюджет считаем в байтах, а не в символах: длина в awk на macOS байтовая, а
# кириллица занимает два байта на букву — то есть оценка выходит с запасом в
# нужную сторону и за потолок Discord не выносит никогда.
CUT="$(printf '%s\n' "$MD" | awk -v max=1800 '
  { n = length($0) + 1; if (used + n > max) exit; used += n; print }')"
if [ -z "$CUT" ]; then
  # Первая же строка длиннее потолка — берём хотя бы заголовок, иначе Discord
  # откажет: сообщение без текста и без вложения для него пустое.
  CUT="$(jq -r '[.followup.title, .meeting.title, "follow-up"]
                | map(select(type == "string" and length > 0))[0]' "$TMP/in.json")"
fi

FULL=0
if [ "$(printf '%s' "$CUT")" != "$(printf '%s' "$MD")" ]; then
  FULL=1
  CUT="$CUT
…"
  printf '%s\n' "$MD" > "$TMP/followup.md"
fi

# allowed_mentions с пустым parse — не мелочь. В расшифровке звучит «напиши
# всем» и «@here», модель переносит это в follow-up дословно, а Discord поднял
# бы по такой строке весь сервер от имени бота.
jq -n --arg c "$CUT" '{content: $c, allowed_mentions: {parse: []}}' > "$TMP/body.json"

# wait=true — чтобы Discord вернул созданное сообщение, а не пустой 204: без
# него неоткуда взять ссылку, которую ждёт stdout.
case "$DISCORD_WEBHOOK_URL" in
  *\?*) POST_URL="$DISCORD_WEBHOOK_URL&wait=true" ;;
  *)    POST_URL="$DISCORD_WEBHOOK_URL?wait=true" ;;
esac

if [ "$FULL" = 1 ]; then
  say "discord: sending the follow-up and attaching it in full" \
      "discord: отправляю follow-up и докладываю его целиком файлом"
  set -- -F "payload_json=<$TMP/body.json" \
         -F "files[0]=@$TMP/followup.md;type=text/markdown"
else
  say "discord: sending the follow-up" "discord: отправляю follow-up"
  set -- -H "Content-Type: application/json" --data-binary "@$TMP/body.json"
fi

if ! CODE="$(curl -sS -X POST -o "$TMP/resp" -w '%{http_code}' "$@" "$POST_URL" 2>"$TMP/err")"; then
  say "discord: could not reach the webhook" "discord: не достучался до вебхука"
  cat "$TMP/err" >&2
  exit 1
fi

case "$CODE" in
  2*) ;;
  401|403|404)
    say "discord: the webhook answered $CODE — it has been deleted or the URL is wrong" \
        "discord: вебхук ответил $CODE — его удалили или ссылка не та"
    head -c 500 "$TMP/resp" >&2; echo >&2
    exit 1 ;;
  429)
    say "discord: rate limited ($CODE) — the follow-up did not go through" \
        "discord: слишком часто ($CODE) — follow-up не ушёл"
    head -c 500 "$TMP/resp" >&2; echo >&2
    exit 1 ;;
  *)
    say "discord: the webhook answered $CODE" "discord: вебхук ответил $CODE"
    head -c 500 "$TMP/resp" >&2; echo >&2
    exit 1 ;;
esac

# Ссылка на сообщение собирается из трёх идентификаторов. Сервер в ответе на
# вебхук бывает не указан — тогда спрашиваем его у самого вебхука.
MSG="$(jq -r '.id // empty' "$TMP/resp" 2>/dev/null || true)"
CHAN="$(jq -r '.channel_id // empty' "$TMP/resp" 2>/dev/null || true)"
GUILD="$(jq -r '.guild_id // empty' "$TMP/resp" 2>/dev/null || true)"
if [ -z "$GUILD" ] && [ -n "$MSG" ]; then
  GUILD="$(curl -sS "$DISCORD_WEBHOOK_URL" 2>/dev/null | jq -r '.guild_id // empty' 2>/dev/null || true)"
fi

# Ссылки может и не быть — это не отказ. Публикация состоялась, код 0, а stdout
# просто пуст: договор так и написан.
if [ -n "$MSG" ] && [ -n "$CHAN" ] && [ -n "$GUILD" ]; then
  echo "https://discord.com/channels/$GUILD/$CHAN/$MSG"
fi

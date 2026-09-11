#!/usr/bin/env bash
# Адаптер расшифровки через AssemblyAI.
#
# Это же образец: с него списывают адаптер к своему движку. Поэтому здесь
# нарочно показаны все три необязательных поля договора сразу — говорящий,
# уверенность реплики и уверенность каждого слова, — и показано, куда уходит
# словарь созвона.
#
# Почему AssemblyAI, а не Deepgram. У обоих есть диаризация и уверенность по
# словам, разница в словаре. У AssemblyAI подмешивание словаря на этапе
# декодирования (keyterms_prompt) языком не ограничено. У Deepgram то же самое
# (keyterm) работает только с англоязычной моделью Nova-3, а созвоны здесь
# русские — то есть ровно та беда, ради которой словарь и собирается («Орынгали»
# вместо «Анвару»), у Deepgram осталась бы нелеченой.
#
# Чем это лучше whisper.cpp — и чем хуже. Лучше: диаризация из коробки (кто
# говорит, слышно по голосу, а не по DOM площадки), уверенность по каждому
# слову, словарь отдельным входом. Хуже: аудио уходит наружу и это стоит денег.
# Как и с groq.sh, размен осознанный: если записи не должны покидать контур —
# whisper.cpp на своём железе.
#
# Контракт: получить путь к аудио и код языка, напечатать в stdout
#   {"vocabulary":"keyterms",
#    "segments":[{"start":1.2,"end":3.4,"text":"...",
#                 "speaker":"A","confidence":0.94,
#                 "words":[{"word":"...","start":1.2,"end":1.4,"confidence":0.9}]}]}
# Всё остальное — в stderr: сервис разбирает stdout как JSON целиком.
#
# Про speaker важно понимать вот что. Диаризация отвечает не на вопрос «как
# зовут», а на вопрос «тот же это голос или другой»: она возвращает «A», «B»,
# «C». Имена берутся у площадки — из субтитров и подсветки говорящего, — и
# сшиваются с этими метками в AlignSpeakers (internal/audio/transcribe.go).
# Отдавать сюда выдуманное имя вместо метки не надо: метка полезнее.
#
# Нужны: ffmpeg, jq, curl и ключ.
#   ASSEMBLYAI_API_KEY  ключ, заводится на assemblyai.com/dashboard
#   ASSEMBLYAI_MODELS   модели через запятую (по умолчанию
#                       universal-3-5-pro,universal-2). Первая точнее, но знает
#                       шесть языков; вторая знает 99. Список из двух означает
#                       «бери первую, если она умеет этот язык, иначе вторую».
#   ASSEMBLYAI_BASE     адрес API (по умолчанию https://api.assemblyai.com).
#                       Отдельной переменной, потому что у AssemblyAI есть
#                       европейский эндпоинт — и потому что на поддельный сервер
#                       этот файл направляется без единой правки, чем и
#                       проверяется (internal/audio/assemblyai_test.go).
#   ASSEMBLYAI_TIMEOUT  сколько всего ждать расшифровку, секунд (по умолчанию
#                       3600). Кончилось — адаптер падает словами, а не висит.
#   ASSEMBLYAI_POLL     как часто спрашивать готовность, секунд (по умолчанию 3)
#   STENO_TERMS         словарь созвона: имена людей и названия сервисов через
#                       запятую. Уходит в keyterms_prompt. (STENO_PROMPT — тот
#                       же словарь фразой, для движков, которые продолжают его
#                       как текст; здесь он не нужен.)
#
# В ответе стоит "vocabulary":"keyterms" — сервису это говорит, что словарь
# ушёл отдельным списком и разбивку на реплики не трогал. Whisper от подсказки
# разбивку ломает, и сервис делает ему второй проход без словаря, чтобы взять
# границы реплик из него (internal/audio/merge.go); здесь второй проход только
# удвоил бы счёт.
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
# Пустой язык означает автоопределение, а не английский.
LANG_CODE="${2:-}"
BASE="${ASSEMBLYAI_BASE:-https://api.assemblyai.com}"
MODELS="${ASSEMBLYAI_MODELS:-universal-3-5-pro,universal-2}"
TIMEOUT="${ASSEMBLYAI_TIMEOUT:-3600}"
POLL="${ASSEMBLYAI_POLL:-3}"

if [ -z "${ASSEMBLYAI_API_KEY:-}" ]; then
  say "assemblyai: ASSEMBLYAI_API_KEY is empty" "assemblyai: пуста переменная ASSEMBLYAI_API_KEY"
  say "  → a key is created at assemblyai.com/dashboard" "  → ключ заводится на assemblyai.com/dashboard"
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

# Час созвона в opus 24k — около 11 МБ. Больше 16 кГц моно для распознавания
# речи не нужно, а платить за загрузку часа стерео-WAV незачем.
ffmpeg -nostdin -loglevel error -i "$AUDIO" -ac 1 -ar 16000 -c:a libopus -b:a 24k \
  "$TMP/a.ogg"

# Ключ уходит заголовком, а не в URL: в тело ошибки и в логи прокси он тогда не
# попадает. Заголовок называется authorization и Bearer перед ключом не ставится
# — это не опечатка, так у AssemblyAI.
AUTH=(-H "authorization: $ASSEMBLYAI_API_KEY")

# --fail-with-body: ненулевой код возврата, но тело ответа всё равно у нас —
# иначе на 401 в руках оставалось бы «curl: (22)» и ни слова о причине.
CURL=(curl -sS --fail-with-body "${AUTH[@]}")

# Ошибка сети или отказ сервера. Одним местом на все три запроса: причину пишем
# в stderr целиком, в stdout не должно уйти ни байта — сервис читает его как
# JSON, и половина ответа там хуже, чем ничего.
fail() {
  say "assemblyai: $1" "assemblyai: $2"
  head -c 600 "$TMP/resp" >&2 2>/dev/null || true
  echo >&2
  exit 1
}

SIZE=$(wc -c < "$TMP/a.ogg" | tr -d ' ')
say "assemblyai: sending $((SIZE / 1024)) KB" "assemblyai: отправляю $((SIZE / 1024)) КБ"

if ! "${CURL[@]}" --data-binary "@$TMP/a.ogg" "$BASE/v2/upload" > "$TMP/resp"; then
  fail "the upload failed" "загрузка не прошла"
fi
UPLOAD_URL="$(jq -r '.upload_url // empty' "$TMP/resp" 2>/dev/null || true)"
[ -n "$UPLOAD_URL" ] || fail "no upload_url in the answer" "в ответе нет upload_url"

# Словарь созвона. Здесь он попадает в keyterms_prompt — отдельный вход, который
# движок учитывает при декодировании. Это принципиально иначе, чем --prompt у
# whisper.cpp: там словарь кладётся в историю текста, и модель продолжает его как
# речь — на записи созвона она от этого склеила тринадцать реплик в две. Тут
# словарь ничем не притворяется, поэтому и бюджета whisper на нём нет: сервис
# шлёт в STENO_TERMS весь список (до сотни слов, internal/audio/vocab.go), а
# потолок AssemblyAI — 1000 фраз.
#
# Берётся STENO_TERMS, а не STENO_PROMPT: в STENO_PROMPT словарь приходит
# фразой — «Созвон команды разработки. Участвуют Ануар и Рустем. Обсуждают
# Сапар.», — потому что whisper продолжает его как текст. Резать фразу на слова
# здесь значило бы гадать, где в ней имена; список приходит готовым.
KEYTERMS='[]'
if [ -n "${STENO_TERMS:-}" ]; then
  KEYTERMS="$(printf '%s' "$STENO_TERMS" | jq -R '
    split(",")
    | map(gsub("^\\s+|\\s+$"; ""))
    | map(select(length > 0)) | .[0:1000]')"
  say "assemblyai: a $(printf '%s' "$KEYTERMS" | jq 'length')-term vocabulary" \
      "assemblyai: словарь на $(printf '%s' "$KEYTERMS" | jq 'length') слов"
fi

# Тело запроса собираем через jq, а не строкой: имя человека с кавычкой или
# обратным слэшем иначе ломает JSON, и разбираться в этом придётся на живом
# созвоне.
#
# speaker_labels — та самая диаризация; punctuate она требует включённым, а он и
# так по умолчанию включён. language_detection ставится только когда язык не
# задан: на созвоне, где переходят с русского на английский, жёстко заданный
# язык хуже, но угадывать вместо явно указанного — ещё хуже.
BODY="$(jq -n \
  --arg url "$UPLOAD_URL" \
  --arg models "$MODELS" \
  --arg lang "$LANG_CODE" \
  --argjson keyterms "$KEYTERMS" '
  {audio_url: $url,
   speech_models: ($models | split(",") | map(gsub("^\\s+|\\s+$"; "")) | map(select(length > 0))),
   speaker_labels: true,
   punctuate: true}
  + (if $lang == "" then {language_detection: true} else {language_code: $lang} end)
  + (if ($keyterms | length) > 0 then {keyterms_prompt: $keyterms} else {} end)')"

if ! "${CURL[@]}" -H 'content-type: application/json' \
     --data "$BODY" "$BASE/v2/transcript" > "$TMP/resp"; then
  fail "the request was refused" "запрос отклонён"
fi
ID="$(jq -r '.id // empty' "$TMP/resp" 2>/dev/null || true)"
[ -n "$ID" ] || fail "no transcript id in the answer" "в ответе нет идентификатора расшифровки"

# Ждём. Расшифровка часа звука занимает минуты, и всё это время ответ на запрос
# — status: processing. Срок общий, а не на попытку: зависший на «processing»
# созвон должен кончиться словами через час, а не висеть до конца света.
say "assemblyai: transcript $ID, waiting" "assemblyai: расшифровка $ID, жду"
WAITED=0
while :; do
  if ! "${CURL[@]}" "$BASE/v2/transcript/$ID" > "$TMP/resp"; then
    fail "could not ask whether it is ready" "не смог спросить о готовности"
  fi
  STATUS="$(jq -r '.status // empty' "$TMP/resp" 2>/dev/null || true)"
  case "$STATUS" in
    completed) break ;;
    error)
      REASON="$(jq -r '.error // ""' "$TMP/resp")"
      say "assemblyai: the engine failed: $REASON" "assemblyai: движок не справился: $REASON"
      exit 1
      ;;
    queued|processing) ;;
    *)
      # Ни статуса, ни JSON — сервер отдал что-то своё. Молчать нельзя: без
      # статуса цикл крутился бы час и кончился «не уложился в срок», уведя
      # разбираться совсем не туда.
      fail "the answer is not what we expect" "ответ не похож на ожидаемый"
      ;;
  esac
  if [ "$WAITED" -ge "$TIMEOUT" ]; then
    say "assemblyai: not done within ${TIMEOUT}s, giving up" \
        "assemblyai: не уложился в ${TIMEOUT} с, сдаюсь"
    exit 1
  fi
  sleep "$POLL"
  WAITED=$((WAITED + POLL))
done

DET="$(jq -r '.language_code // empty' "$TMP/resp")"
[ -n "$DET" ] && say "assemblyai: language $DET" "assemblyai: язык $DET"

# Времена у AssemblyAI в миллисекундах — переводим в секунды.
#
# Реплики берём из utterances: это и есть «кусок речи одного голоса», ровно то,
# что нужно на входе follow-up. Если диаризация не сработала (её нет у модели,
# ответ старого формата), utterances пуст — тогда собираем реплики из слов сами,
# разрезая по смене говорящего и по паузе длиннее 0.8 секунды. Пустой ответ
# лучше собрать из того, что есть, чем отдать одну реплику на весь созвон:
# таймкоды в follow-up превращаются в ссылки на момент записи.
jq '
def r3: . * 1000 | round / 1000;
def word: {word: .text, start: (.start / 1000 | r3), end: (.end / 1000 | r3),
           confidence: .confidence};
def mean($xs): if ($xs | length) > 0 then (($xs | add) / ($xs | length) | r3) else null end;
def fromWords:
  reduce .[] as $w ([];
    if (length == 0) or ($w.speaker != .[-1].speaker) or ($w.start - .[-1].last > 800)
    then . + [{speaker: $w.speaker, start: $w.start, end: $w.end, last: $w.end, words: [$w]}]
    else .[0:-1] + [.[-1] | .end = $w.end | .last = $w.end | .words += [$w]]
    end)
  | map({start: .start, end: .end, speaker: .speaker,
         text: (.words | map(.text) | join(" ")),
         confidence: mean(.words | map(.confidence)),
         words: .words});

((.utterances // []) | map({start, end, speaker, text, confidence, words: (.words // [])})) as $u
| (if ($u | length) > 0 then $u else ((.words // []) | fromWords) end)
| {vocabulary: "keyterms", segments: [ .[] | {
      start:      (.start / 1000 | r3),
      end:        (.end   / 1000 | r3),
      speaker:    (.speaker // ""),
      text:       (.text // "" | gsub("^\\s+|\\s+$"; "")),
      confidence: .confidence,
      words:      [ (.words // [])[] | word ]
    }] | map(select(.text != "")) }' "$TMP/resp" > "$TMP/segments.json" || {
  say "assemblyai: could not read the answer" "assemblyai: не разобрал ответ"
  head -c 600 "$TMP/resp" >&2
  echo >&2
  exit 1
}

# Пустая расшифровка — не ошибка адаптера: движок дослушал файл и не нашёл в нём
# речи. Говорим об этом словами и отдаём пустой список; что с ним делать, решает
# сервис (CheckTranscript в internal/audio/transcribe.go), и он же объяснит
# человеку про язык и тишину в начале записи.
if [ "$(jq '.segments | length' "$TMP/segments.json")" -eq 0 ]; then
  say "assemblyai: no speech in the answer" "assemblyai: в ответе нет речи"
fi
cat "$TMP/segments.json"

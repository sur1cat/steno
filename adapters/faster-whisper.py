#!/usr/bin/env python3
"""Адаптер расшифровки для faster-whisper.

Контракт: путь к аудио и код языка на входе, в stdout —
    {"segments":[{"start":1.2,"end":3.4,"text":"...",
                  "confidence":0.87,
                  "words":[{"word":"...","start":1.2,"end":1.4,"confidence":0.9}]}]}

confidence и words необязательны, и их отсутствие означает «движок не сказал», а
не ноль. Здесь они есть: whisper считает вероятность каждого токена всё равно, а
word_timestamps=True лишь не даёт их выбросить. Нужны они потому, что whisper
никогда не говорит «не расслышал» — он языковая модель и всегда выбирает самое
вероятное продолжение. «Орынгали нужно закончить Сапар» выходит как «Анвару
нужно закончить сапар», и по тексту это никак не отличить от верной строки; по
числу — отличить можно: на записи созвона «Анвару» получило 0.33, а «закончить»
0.99.

Установка:  pip install faster-whisper
Настройка:  WHISPER_MODEL (по умолчанию large-v3), WHISPER_DEVICE (cpu|cuda),
            WHISPER_COMPUTE (int8 на CPU, float16 на GPU),
            STENO_PROMPT — словарь созвона фразой (initial_prompt). Со
            словарём сервис зовёт адаптер дважды, с ним и без, и склеивает
            проходы сам: подсказка чинит слова, но ломает разбивку на
            реплики (internal/audio/merge.go).
"""
import json
import os
import sys

from faster_whisper import WhisperModel


def main() -> None:
    if len(sys.argv) < 2:
        sys.exit("нужен путь к аудио")
    audio = sys.argv[1]
    language = sys.argv[2] if len(sys.argv) > 2 else None

    device = os.environ.get("WHISPER_DEVICE", "cpu")
    model = WhisperModel(
        os.environ.get("WHISPER_MODEL", "large-v3"),
        device=device,
        compute_type=os.environ.get(
            "WHISPER_COMPUTE", "float16" if device == "cuda" else "int8"),
    )
    # vad_filter выкидывает тишину — в записи созвона её много, и без него
    # whisper начинает галлюцинировать текст на пустых участках.
    #
    # initial_prompt — словарь созвона: имена людей и названия сервисов,
    # которых нет в словаре модели. Без него whisper подменяет их похожими
    # обычными словами, и починить это потом уже нечем.
    # word_timestamps=True — ради вероятностей, а не ради времён: без него
    # faster-whisper выбрасывает вероятность каждого слова, и уверенность взять
    # неоткуда. Стоит это одного прохода выравнивания по вниманию поверх уже
    # посчитанного — единицы процентов от прогона.
    segments, _ = model.transcribe(
        audio, language=language or None, vad_filter=True, word_timestamps=True,
        initial_prompt=os.environ.get("STENO_PROMPT") or None)

    out = []
    for s in segments:
        if not s.text.strip():
            continue
        seg = {"start": s.start, "end": s.end, "text": s.text.strip()}
        words = [
            {"word": w.word.strip(), "start": w.start, "end": w.end,
             "confidence": round(w.probability, 3)}
            for w in (s.words or [])
            if w.word.strip()
        ]
        if words:
            seg["words"] = words
            # Уверенность реплики — среднее по её словам. Отдельного смысла в
            # ней немного (ошибка распознавания — это одно слово посреди верной
            # фразы), но движки без слов отвечают только ею, и договор ровный.
            seg["confidence"] = round(
                sum(w["confidence"] for w in words) / len(words), 3)
        out.append(seg)
    json.dump({"segments": out}, sys.stdout, ensure_ascii=False)


if __name__ == "__main__":
    main()

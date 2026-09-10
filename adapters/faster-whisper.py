#!/usr/bin/env python3
"""Адаптер расшифровки для faster-whisper.

Контракт: путь к аудио и код языка на входе, в stdout —
    {"segments":[{"start":1.2,"end":3.4,"text":"..."}]}

Установка:  pip install faster-whisper
Настройка:  WHISPER_MODEL (по умолчанию large-v3), WHISPER_DEVICE (cpu|cuda),
            WHISPER_COMPUTE (int8 на CPU, float16 на GPU),
            STENO_PROMPT — словарь созвона через запятую (initial_prompt).
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
    segments, _ = model.transcribe(
        audio, language=language or None, vad_filter=True, word_timestamps=False,
        initial_prompt=os.environ.get("STENO_PROMPT") or None)

    out = [
        {"start": s.start, "end": s.end, "text": s.text.strip()}
        for s in segments
        if s.text.strip()
    ]
    json.dump({"segments": out}, sys.stdout, ensure_ascii=False)


if __name__ == "__main__":
    main()

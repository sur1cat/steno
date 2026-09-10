[← README](../README.md) · [Commands](commands.md) · [Install](install.md) · **Cost** · [How it works](how-it-works.md)

# What it costs

Two paid pieces, both optional to varying degrees.

## Transcription

Groq runs the same `whisper-large-v3` for $0.04 per hour of audio — about
**$6/month** for a team with 160 meeting-hours. A GPU VM for the same work runs
into the thousands and idles 97% of the time.

| 160 meeting-hours/month | |
|---|---|
| **Groq** (whisper-large-v3) | **$6** |
| AssemblyAI | $24 |
| Deepgram Nova-3 | $42 |
| OpenAI GPT-4o Transcribe | $58 |
| whisper on your hardware | free, needs a GPU or patience |
| platform captions | free, lower quality |

## The follow-up

Default `claude-opus-5` at `effort: low` — about **$0.40** for an hour-long
meeting, $0.12 for a short one. `setup` also offers `claude-sonnet-5`: two and a
half times cheaper, usually enough for a standup.

A model on your own machine — Ollama, LM Studio, llama.cpp — costs nothing at
all; `steno setup` offers it in the same question. See
[Who writes the follow-up](install.md#who-writes-the-follow-up).

<img src="../assets/cli-cost.svg" width="880" alt="steno cost — measured spend over 30 days">

**Why effort stays low — measured, not guessed.** One marked-up meeting, ten
combinations of model and effort: all ten pulled the same five assignments with
the right owners and the right dates. Effort added no task — only time and
money, up to 23 minutes and $1.39 against 40 seconds and $0.12 on the same text.
Raise it for sharper wording in the risks, not for fuller lists. `steno cost`
reports measured tokens, not estimates.

## Pick a transcription model

| | quality | speed | privacy |
|---|---|---|---|
| **Groq** | whisper-large-v3 | an hour of audio in ~10s | audio leaves your network |
| **whisper locally** | `large-v3-q5_0`, 1 GB | ~13 min per meeting-hour on Apple Silicon; **3 hours** on CPU | nothing leaves |
| **platform captions** | noticeably worse, one language | instant | nothing leaves |

⚠️ **Take `large-v3-q5_0`, skip `turbo`.** Quantizing large-v3 costs nothing —
the transcripts match word for word at a third of the disk. `turbo` is the trap:
twice as fast, and it rewrote a product name it did not know (`Plaud`) into one
it did (`Cloud AI`), then looped `Cloud` seventeen times over the most
substantive minute of the call. A follow-up written from that will confidently
describe an integration nobody mentioned.

Meet recognises **one** language per session, so a call that switches languages
needs whisper. Captions are used either way — they are the only source of
speaker names.

Running locally without a GPU is not a slow option, it is not an option: a
meeting-hour takes three. Use Groq or platform captions instead.

## Scale

`steno setup` picks defaults from three shapes:

- **One person.** Your laptop. Captions or Groq, nothing to install.
- **Small team.** One cheap VM. Groq; no GPU needed.
- **Larger team.** Six concurrent calls, a server, GPU or Groq. Transcriptions
  are queued so several calls ending at once do not take the machine down.

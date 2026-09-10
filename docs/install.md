[← README](../README.md) · [Commands](commands.md) · **Install** · [Cost](cost.md) · [How it works](how-it-works.md)

# Install

Docker must be running: the bot joins the call inside a container.

```console
brew install sur1cat/tap/steno
steno setup     # nine questions, each answer checked
steno start     # background; steno stop ends it
```

`setup` makes its own directory (`~/steno`), writes the config there and puts
secrets in a `.env` with mode 0600 — never in the config, because the config is
meant to live in a repository. `steno doctor` says what is still missing and
prints the line that fixes it.

## Linux, or building from source

Go and Node both needed — the panel is compiled into the binary:

```console
git clone https://github.com/sur1cat/steno && cd steno
make build && ./steno setup
```

`go install` is deliberately not offered: the panel bundle is a build artifact
and is not kept in the repository, so a binary made that way comes up with no
panel at all — a working install by every appearance, until you open it.

The bot's container (Chromium under a virtual display, PulseAudio, ffmpeg) is
pulled before the first call. Ahead of time: `docker pull ghcr.io/sur1cat/steno-bot`.
Build it yourself: `make bot-image`.

## Interface language

English by default; Russian is a full second language, not a subset.
`steno setup` asks first thing and writes the answer:

```json
{ "lang": "ru" }
```

For one command, without touching the config: `STENO_LANG=ru steno ui`.

The choice carries further than the labels — it sets the follow-up language
(`claude.output_language`), the language Meet is asked to recognise
(`bot.caption_language`), the name the bot appears under in the participant
list, and the calendar's skip markers.

## Who writes the follow-up

`steno setup` asks; `brain.provider` pins it in the config. All four return the
same JSON — nothing downstream knows who answered.

| | | |
|---|---|---|
| **Claude subscription** | `claude -p`, the non-interactive mode of Claude Code | no key, no second bill |
| **Anthropic key** | needed on a server: works without a human logging in | `ANTHROPIC_API_KEY` |
| **OpenAI-compatible** | OpenAI, Groq, OpenRouter, Together, DeepSeek — or Ollama, LM Studio, llama.cpp on this machine | one preset, or an address of your own |
| **ChatGPT subscription** | `codex exec` — the schema goes in as a file, so it is kept exactly | |

A model on your own machine costs nothing and sends nothing anywhere: together
with local whisper it is the setup where no data leaves at all.

If none of them fits — an internal endpoint, your own wrapper, someone else's
protocol — `brain.provider = "command"` runs a script: one JSON object in on
stdin, one JSON object out on stdout. The contract is written out in
`adapters/ollama.sh`.

`steno doctor` says which one it found and which model came out of it.

## Try it without setting anything up

```console
steno join --no-followup --captions https://meet.google.com/abc-defg-hij
```

The bot knocks as a guest, you let it in, talk for a minute, it prints the
transcript. No keys, no whisper, no bot account.

## The menu bar app

Built separately — it is an app for one person's Mac, not part of the service,
so `brew install steno` does not touch it:

```console
cd bar && ./build.sh && open build/StenoBar.app
```

It reads the database directly, so it answers even when the service is down. ⌘N
records a voice note through the same pipeline as a meeting.

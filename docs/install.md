[← README](../README.md) · [Commands](commands.md) · **Install** · [Cost](cost.md) · [How it works](how-it-works.md)

# Install

One binary, and the transcription adapters ride along: every way below leaves
them where the binary looks, so `steno setup` finds them without being told.

| | |
|---|---|
| [macOS, Linux](#homebrew) | `brew install sur1cat/tap/steno` |
| [Debian, Ubuntu](#debian-ubuntu) | the `.deb` from [Releases](https://github.com/sur1cat/steno/releases/latest), `sudo apt install ./steno_<version>_amd64.deb` |
| [Fedora, RHEL](#fedora-rhel) | the `.rpm` from Releases, `sudo dnf install ./steno-<version>-1.x86_64.rpm` |
| [Any Linux or Mac](#the-archive) | `steno_<version>_<os>_<arch>.tar.gz` from Releases, sums in `checksums.txt` next to it |
| [From source](#from-source) | `make build` — Go and Node |
| [The bot](#the-bot) | Docker must be running; `docker pull ghcr.io/sur1cat/steno-bot` fetches its image ahead of time |

Then, whichever way you came in:

```console
steno setup     # nine questions, each answer checked
steno start     # background; steno stop ends it
```

`setup` makes its own directory (`~/steno`), writes the config there and puts
secrets in a `.env` with mode 0600 — never in the config, because the config is
meant to live in a repository. `steno doctor` says what is still missing and
prints the line that fixes it.

## Homebrew

```console
brew install sur1cat/tap/steno
```

The formula carries a Linux block, so it is the same command there. ffmpeg
comes with it; the adapters go to `$(brew --prefix)/share/steno/adapters`. On
ARM Linux, Homebrew has no bottles and would build ffmpeg from source — take
the `.deb`, the `.rpm` or the archive instead.

## Debian, Ubuntu

```console
v=$(curl -fsSL https://api.github.com/repos/sur1cat/steno/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
curl -fsSLO https://github.com/sur1cat/steno/releases/download/v$v/steno_${v}_amd64.deb
sudo apt install ./steno_${v}_amd64.deb
```

`arm64` on ARM. `apt install ./file.deb` brings ffmpeg in; `dpkg -i` would
leave it to you. The binary lands in `/usr/bin`, the adapters in
`/usr/share/steno/adapters`.

## Fedora, RHEL

```console
v=$(curl -fsSL https://api.github.com/repos/sur1cat/steno/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
sudo dnf install https://github.com/sur1cat/steno/releases/download/v$v/steno-${v}-1.x86_64.rpm
```

`aarch64` on ARM. The package asks for `/usr/bin/ffmpeg` rather than a package
by name, so Fedora's own `ffmpeg-free` satisfies it and so does RPM Fusion's
`ffmpeg`; RHEL and its clones have ffmpeg only from RPM Fusion.

## The archive

```console
v=$(curl -fsSL https://api.github.com/repos/sur1cat/steno/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
curl -fsSLO https://github.com/sur1cat/steno/releases/download/v$v/steno_${v}_linux_amd64.tar.gz
curl -fsSL https://github.com/sur1cat/steno/releases/download/v$v/checksums.txt | grep linux_amd64 | sha256sum -c
tar xzf steno_${v}_linux_amd64.tar.gz
sudo install steno /usr/local/bin/
sudo mkdir -p /usr/local/share/steno && sudo cp -R adapters /usr/local/share/steno/
```

`linux` or `darwin`, `amd64` or `arm64`; on a Mac it is `shasum -a 256 -c` in
place of `sha256sum -c`. ffmpeg is yours to install here: `apt install ffmpeg`,
`dnf install ffmpeg-free`, `brew install ffmpeg`.

The adapters need not go to `share`: steno looks next to its own binary first,
so the binary and `adapters/` may simply stay together in one directory.

## From source

Go and Node both needed — the panel is compiled into the binary:

```console
git clone https://github.com/sur1cat/steno && cd steno
make build && ./steno setup
```

`go install` is deliberately not offered: the panel bundle is a build artifact
and is not kept in the repository, so a binary made that way comes up with no
panel at all — a working install by every appearance, until you open it.

## The bot

Docker must be running: the bot joins the call inside a container (Chromium
under a virtual display, PulseAudio, ffmpeg), pulled before the first call.
Ahead of time: `docker pull ghcr.io/sur1cat/steno-bot`. Build it yourself:
`make bot-image`.

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

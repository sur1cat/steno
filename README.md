<div align="center">

<img src="assets/icon.png" width="120" alt="steno">

# steno

**Your meetings, written up by a bot that already knows your codebase.**

[![Release](https://img.shields.io/github/v/release/sur1cat/steno?color=1f2d27&label=release&labelColor=0b0f0d)](https://github.com/sur1cat/steno/releases)
[![macOS · Linux](https://img.shields.io/badge/macOS-·%20Linux-4DDBA4?labelColor=0b0f0d)](docs/install.md)
[![Go](https://img.shields.io/badge/Go-one%20binary-00ADD8?labelColor=0b0f0d)](https://go.dev)
[![Works with Google Meet](https://img.shields.io/badge/works%20with-Google%20Meet-4DDBA4?labelColor=0b0f0d)](#how-the-bot-gets-into-a-call)
[![License](https://img.shields.io/badge/license-MIT-3fb950?labelColor=0b0f0d)](LICENSE)

**English** · [Русский](README.ru.md)

<p>
<a href="#install"><img src="https://img.shields.io/badge/Install-6BCB9B?style=for-the-badge" alt="Install" height="30"></a>
&nbsp;
<a href="docs/commands.md"><img src="https://img.shields.io/badge/Commands-1E2E27?style=for-the-badge" alt="Commands" height="30"></a>
&nbsp;
<a href="#tour"><img src="https://img.shields.io/badge/Screens-1E2E27?style=for-the-badge" alt="Screens" height="30"></a>
&nbsp;
<a href="docs/cost.md"><img src="https://img.shields.io/badge/Costs-1E2E27?style=for-the-badge" alt="What it costs" height="30"></a>
&nbsp;
<a href="https://github.com/sur1cat/steno/releases/latest"><img src="https://img.shields.io/badge/Releases-1E2E27?style=for-the-badge" alt="Releases" height="30"></a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/intro-dark.svg">
  <img src="assets/intro-light.svg" width="1000" alt="Six steps, and what the meeting has become at each: a row in the calendar, audio and captions, two files on disk, a transcript with names, the follow-up with owners and due dates, and where it was sent">
</picture>

</div>

A bot joins the call, records it, transcribes it and writes the follow-up —
decisions, tasks and open questions, filed under your projects and outliving the
meeting. One Go binary; recordings stay on your machine.

<div align="center">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/term-start-dark.svg">
  <img src="assets/term-start-light.svg" width="835" alt="steno start: it listens, joins the call, records, transcribes, writes the follow-up and sends it">
</picture>
</div>

## Why this and not Otter, Fireflies or Fathom

Those are good. If a transcript with a summary is all you need, they do it
today with no server and no setup. steno is for two things they don't do, and
one they can't promise.

🧠 **It learns your vocabulary from your code.** Attach a repository and steno
reads its README, manifests, layout and the subjects of recent commits — that
last part matters most, because that is where the words your team says out loud
live. So *"fix the webhooks in billing"* files itself under the right project.

📌 **The project outlives the meeting.** Before each call the model is shown
what is still open, so the same task is not created again every week. Once a
day new commits are matched against open tasks: work done quietly, never
mentioned on a call, still gets closed — with the commit as evidence.

🔒 **The recording stays with you.** A visible bot, not a hidden recorder:
anyone on the call can remove it. Audio and transcripts live in one directory
on your machine; with local whisper and a local model, nothing leaves at all.

## Install

| | |
|---|---|
| macOS, Linux | `brew install sur1cat/tap/steno` |
| Debian, Ubuntu | `sudo apt install ./steno_<version>_amd64.deb` — the `.deb` is on [Releases](https://github.com/sur1cat/steno/releases/latest) |
| Fedora, RHEL | `sudo dnf install ./steno-<version>-1.x86_64.rpm` — the `.rpm`, same place |
| Any Linux or Mac | `curl -L https://github.com/sur1cat/steno/releases/latest/download/steno_<version>_<os>_<arch>.tar.gz \| tar xz` — `linux` or `darwin`, `amd64` or `arm64`; sums in [checksums.txt](https://github.com/sur1cat/steno/releases/latest/download/checksums.txt) |
| From source | `git clone https://github.com/sur1cat/steno && cd steno && make build` — Go and Node; not `go install`, [here is why](docs/install.md#from-source) |
| The bot | Docker must be running — the bot joins the call inside a container, pulled before the first call; `docker pull ghcr.io/sur1cat/steno-bot` does it now |

Then, whichever way you came in:

```console
steno setup     # nine questions, each answer checked
steno start     # background; steno stop ends it
```

<div align="center">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/term-install-dark.svg">
  <img src="assets/term-install-light.svg" width="611" alt="brew install, then steno setup walking through nine questions, then steno start">
</picture>
</div>

Secrets go into a `.env` with mode 0600, never into the config. `steno doctor`
says what is still missing and prints the line that fixes it.

**Each way with the full commands, the interface language, which model writes the follow-up —**
[docs/install.md](docs/install.md).

## Use it however suits you

**Where it runs** — one binary either way, and the panel is compiled into it, so
there is nothing to deploy but a file.

| | |
|---|---|
| 💻 **Your laptop** | your own meetings. Local whisper plus platform captions and nothing leaves the machine |
| 🖧 **One cheap VM** | the team's meetings. No GPU needed if transcription goes to Groq |

**How you look at it** — same database underneath; four of the five need no
service running.

| | | needs the service |
|---|---|---|
| ⌨️ **[CLI](docs/commands.md)** | `steno list`, `show`, `projects`, `cost`, `doctor` | no |
| 🖥️ **[`steno ui`](#tour)** | full-screen terminal: meetings, tasks, projects, search | no |
| 🌐 **[Panel](#tour)** | a browser, on the machine or over the network | yes |
| 🍎 **[Menu bar](#in-the-menu-bar)** | macOS: today, tasks, projects, one field to send the bot | only to send the bot |
| 🤖 **[MCP](docs/commands.md#ask-a-model)** | Claude Code, Claude Desktop, Cursor: `claude mcp add steno -- steno mcp` | no |

**Who writes it up** — `steno setup` asks; `brain.provider` pins it. All four
hand back the same JSON, so nothing downstream knows who answered.

| | |
|---|---|
| 🅰️ **Claude** | an Anthropic key, or the Claude Code subscription through `claude -p` |
| 💬 **Codex** | the ChatGPT subscription through `codex exec` |
| 🔌 **The OpenAI dialect** | OpenAI, Groq, OpenRouter, Together, DeepSeek — or Ollama, LM Studio, llama.cpp, right here |
| 📜 **A script of your own** | `brain.provider = "command"`: one JSON object in, one out |

A model on your own machine costs nothing and sends nothing anywhere — with
local whisper, that is an install nothing leaves at all.

## How the bot gets into a call

<div align="center">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/join-dark.svg">
  <img src="assets/join-light.svg" width="900" alt="From the calendar entry to two files on disk: the bot opens the link, waits in the waiting room, is admitted, turns its mic and camera off, records, and leaves when everyone else does">
</picture>
</div>

It joins under a name that says it is recording, so it is visible in the
participant list. Anyone can remove it — the follow-up is still produced from
what was captured.

| | |
|---|---|
| ✅ **Google Meet** | checked on real calls: joining, recording, captions for the speaker names |
| ✅ **A recording you already have** | Zoom, a phone call, a voice note — upload it, the rest is the same |
| 🚧 **Jitsi Meet** | built on Jitsi's own end-to-end tests; no bot has joined a live call yet |
| 🚫 **Teams · Zoom** | deliberately not: a CAPTCHA or a disabled setting you only discover on the call — [why](docs/how-it-works.md#what-works-and-what-does-not-yet) |

## Bring your own

The model is not the only swappable part. Two more seams, both of them a
command and a page of contract. Neither needs a line of Go, a rebuild, or a
pull request.

**Transcription** takes an audio path and prints segments. Three adapters ship
in [`adapters/`](adapters); a fourth engine is ten lines of bash.

```json
"transcribe": { "cmd": ["./adapters/whisper-cpp.sh", "{{audio}}", "{{language}}"] }
```

**Publishing** takes the meeting on stdin and prints the link it published to.
Discord, Mattermost, Notion, a webhook, an email, a file in a folder, a ticket
in Jira — a short script each.

```json
"publish": { "targets": [ { "cmd": ["./adapters/discord.sh"] } ] }
```

What arrives on stdin — the raw analysis *and* the same follow-up already
written up, because an adapter that files tickets needs the fields and an
adapter that posts to a chat needs the text:

```json
{
  "meeting":    {"id": "…", "title": "…", "url": "…", "started_at": "…",
                 "duration_sec": 2520, "participants": ["…"], "projects": ["…"]},
  "followup":   {"title": "…", "tldr": ["…"], "action_items": [], "decisions": [],
                 "open_questions": [], "risks": [], "timeline": []},
  "text":       {"markdown": "…", "plain": "…", "html": "…"},
  "links":      {"google_doc": "…"},
  "transcript": "[00:01:12] Rustem: …"
}
```

Exit 0 means published, anything else means it was not, and stderr says why —
it goes into the log and is kept next to the meeting. stdout is the link, if
there is one.

[`adapters/discord.sh`](adapters/discord.sh) is the one to copy from: a Discord
webhook needs no app and no token, just a URL.

## A meeting from anywhere

Anything that can send a POST can send the bot into a call — a phone shortcut,
a Slack slash command, another team's admin panel, cron:

```console
$ curl -X POST http://steno:8787/join \
       -H "Authorization: Bearer $STENO_HTTP_TOKEN" \
       -d '{"url":"https://meet.google.com/abc-defg-hij"}'
{"text":"On my way to https://meet.google.com/abc-defg-hij …","meeting_id":"20260908-1530-abc"}
```

The shared secret is not optional — whoever knows it can send the bot into any
call, so the endpoint does not come up without one. It lives in the `.env`, and
never in the panel or the config. A refusal names which of the three things is
wrong: the header, the secret, or the link.

## Tour

Everything below on demo data, before the first meeting and without a single
question asked: `steno demo` opens the panel in your browser — four meetings,
their tasks and decisions, a schedule for the next two days. Ctrl+C, and it
is gone.

<table>
<tr>
<td width="58%" align="center"><img src="assets/tour-meeting.gif" alt="Clicking a task's timecode scrolls the transcript to that second"></td>
<td width="42%" valign="middle">

### 📝 Click the timecode

Every task, decision and question carries the second it was said. Click it and
the transcript jumps there.

</td>
</tr>
<tr>
<td width="42%" valign="middle">

### ✅ Tasks, not meetings

One list across every call. Filter by owner and the whole thing regroups.

</td>
<td width="58%" align="center"><img src="assets/tour-tasks.gif" alt="Filtering the task list by owner regroups it live"></td>
</tr>
<tr>
<td width="58%" align="center"><img src="assets/tour-project.gif" alt="Closing a task moves it into the closed list with its reason"></td>
<td width="42%" valign="middle">

### 📌 A project remembers

Open items pile up per project until something closes them — the next call, a
button, or a commit.

</td>
</tr>
<tr>
<td width="42%" valign="middle">

### 🔎 Everything ever said

One box over transcripts and follow-ups. A hit opens the meeting at that second.

</td>
<td width="58%" align="center"><img src="assets/tour-search.gif" alt="Typing a word searches transcripts and follow-ups at once"></td>
</tr>
<tr>
<td width="58%" align="center"><img src="assets/tui-ui.svg" alt="steno ui — moving between meetings, tasks and projects in the terminal"></td>
<td width="42%" valign="middle">

### 🖥️ The whole product, in a terminal

`steno ui`. No service, no browser: the same database. `?` lists the keys.

</td>
</tr>
<tr>
<td width="42%" valign="middle">

### 🗓️ Where it will go, before it goes

Built from the team's calendars: what it joins, what it skips, and why.

</td>
<td width="58%" align="center"><img src="assets/panel-schedule.png" alt="Schedule: where the bot will go and where it will not"></td>
</tr>
<tr>
<td width="58%" align="center"><img src="assets/panel-settings.png" alt="Channels: where meetings come from and where the follow-up goes"></td>
<td width="42%" valign="middle">

### 🔌 Channels explain themselves

Four ways in, four ways out. No tokens in this screen and there never will be.

</td>
</tr>
</table>

## Docs

| | |
|---|---|
| **[Commands](docs/commands.md)** | every command and flag, with what the output looks like |
| **[Install](docs/install.md)** | Linux, source builds, language, which model writes it up, the menu bar app |
| **[What it costs](docs/cost.md)** | transcription prices, which whisper model, scale |
| **[How it works](docs/how-it-works.md)** | the pipeline, getting the bot in, consent, what works and what does not |
| **[DESIGN.md](DESIGN.md)** | every design decision and why it is what it is |

## License

MIT

# steno

A bot joins your calls, records them, transcribes, and sends out a structured
follow-up — to Google Docs, Slack and Telegram. Decisions and action items are
attributed to your projects and outlive the individual meeting.

One Go binary. Recordings and transcripts stay on your server.

*[Русская версия](README.ru.md)*

```
$ steno serve
calendar: watching 12 calendars, polling every 2m0s
mail: watching invitations to steno@company.com
telegram: listening for meeting links
panel: listening on :8080
joining "Release planning" (reason: calendar, anna@company.com)
bot: muted mic and camera
bot: in the call
bot: recording to data/recordings/2026-09-09-1100-a1b2/audio.ogg
bot: done — everyone left, 47m12s, 5 participants
transcribing (large-v3-turbo)
412 lines, 397 with a speaker name
writing follow-up (claude-opus-5)
6 tasks, 3 decisions, 2 open questions
spend: 31k in, 9k out — $0.38
google_docs: https://docs.google.com/document/d/1AbC.../edit
slack: https://team.slack.com/archives/C01234/p1757...
```

## Why this and not Otter, Fireflies or Fathom

Those are good, and if all you need is a transcript with a summary, they will
do it today with no server and no setup. Fathom has a generous free tier;
Fireflies runs about $10 per person per month.

steno exists for two things they don't do.

**Your code teaches it your vocabulary.** You attach a project's repository, and
steno reads its README, manifests, layout and the subjects of its recent
commits — the last part matters most, because that is where the words your team
actually says out loud live. So when someone says "let's fix the webhooks in
billing", it lands under the right project rather than in a flat list.

**Project state outlives the meeting.** Tasks, decisions and open questions
accumulate per project. Before each new call the model is shown what is still
open, so the same task is not created again every week. And once a day new
commits are matched against open tasks: work done quietly, never mentioned on a
call, still gets closed — with the commit as evidence.

Everything else — where recordings live, which model transcribes, what the
follow-up looks like — is yours to decide.

## Quick start

```
brew install --HEAD sur1cat/tap/steno   # or: go install github.com/sur1cat/steno@latest
steno setup
```

`setup` asks one question at a time, checks each answer, and writes the config.
It starts by asking how you will use it — one person, a small team, or six
concurrent calls — and sets the concurrency limits and model effort from that.

Secrets are typed without echo and written to a `.env` file with mode 0600,
never into the config: the config is meant to live in a repository, tokens are
not.

### Two ways to pay for Claude

`setup` asks which one, because the right answer depends on who starts the
process, not on price.

**A Claude subscription you already have.** steno calls `claude -p`, the
non-interactive mode of Claude Code, and the follow-up comes out of the plan you
are already paying for. No API key, no second bill. This is the personal setup:
your own meetings, on your own laptop.

```
claude auth login     # if you have not already
steno setup           # pick "Подписка Claude"
```

**An API key.** Needed on a server, and the reason is not billing: `claude -p`
requires a login performed by a human, and nobody logs into a server.

```
export ANTHROPIC_API_KEY=sk-ant-...
```

Either way `steno doctor` tells you which one it found, and `claude.via` in the
config pins it (`cli`, `api`, or `auto`).

One honest note about the subscription path: Claude Code sends its own system
prompt with every call, so a follow-up costs somewhat more in tokens than the
same request through the API. It comes out of a plan rather than a card, which
is the whole point, but `steno cost` reports what was actually spent either way.

Then:

```
steno doctor    # says what is missing and how to fix it
steno serve
```

To try it on a real call without setting up anything at all:

```
steno join --no-followup --captions https://meet.google.com/abc-defg-hij
```

The bot knocks as a guest, you let it in, talk for a minute, and it prints the
transcript. No API keys, no whisper, no bot account.

## How it works

```
calendar ┐
mail     ├──▶ Dispatcher ──▶ bot in a container ──▶ audio.ogg
telegram │                          │                   │
http     ┘                          └── captions.jsonl ─┤
panel    ┘                                              ▼
                                    whisper ──▶ transcript with names
                                                        ▼
                                    Claude ──▶ follow-up (JSON)
                                                        ▼
                              Google Docs · Slack · Telegram · panel
```

Text comes from whisper because it is accurate. Speaker names come from Google
Meet's own captions because they are authoritative — Meet knows who is talking.
The two are stitched by time.

The design decisions, and why each one is what it is, are in
[DESIGN.md](DESIGN.md).

## Getting the bot into a call

Four independent sources; enable any combination.

| | what a person does |
|---|---|
| **Calendar** | nothing — the bot reads the team's calendars and shows up a minute early |
| **Mail invitation** | adds `steno@company.com` to a running call with "Add people" |
| **Telegram** | drops the meeting link into a chat |
| **Panel / HTTP** | a button, a Slack slash command, or `curl` |

The mail route deserves a note: the bot has its own Google account anyway (a
guest has to be admitted by hand every time), so it can simply be invited like a
colleague. The inviter needs to know nothing about steno. Invitations are only
accepted from your own domain — otherwise the bot's address would be a way to
record someone else's conversation using your account.

## What it costs

Two paid pieces, both optional to varying degrees.

**Transcription.** Groq runs the very same `whisper-large-v3` on their hardware
for $0.04 per hour of audio. For a team with 160 meeting-hours a month that is
about **$6**. A dedicated GPU VM for the same work runs into the thousands and
sits idle 97% of the time.

| | 160 meeting-hours/month |
|---|---|
| Groq (whisper-large-v3) | $6 |
| AssemblyAI | $24 |
| Deepgram Nova-3 | $42 |
| OpenAI GPT-4o Transcribe | $58 |
| whisper on your own hardware | free, needs a GPU or patience |
| Google Meet captions | free, lower quality |

**The follow-up.** Roughly $0.40 per hour-long call on `claude-opus-5`. Note
that with adaptive thinking most of that is the model's reasoning, not the
answer — so `claude.effort` is the first lever if it gets expensive. `steno cost`
reports measured token counts, not estimates.

## Transcription options

| | quality | speed | privacy |
|---|---|---|---|
| Groq | whisper-large-v3 | an hour of audio in ~10s | audio leaves your network |
| whisper locally | `large-v3-q5_0`, 1 GB | ~13 min per meeting-hour on Apple Silicon; **3 hours** on the same machine's CPU | nothing leaves |
| Meet captions | noticeably worse, single language only | instant | nothing leaves |

Meet recognises **one** language per session, so a call that switches between
languages needs whisper. Captions are still used either way — they are the only
source of speaker names, and the language setting does not affect those.

**Pick `large-v3-q5_0`, and skip `turbo`.** Benchmarked on a real recording:
quantizing large-v3 costs nothing — the transcripts match word for word, at a
third of the disk and half the RAM. `turbo` is the trap. It is twice as fast and
it rewrote a product name it did not know (`Plaud`) into a plausible one it did
(`Cloud AI`), then looped `Cloud` seventeen times over the most substantive
minute of the call. That is not noise you can see; a follow-up written from it
will confidently describe a cloud integration nobody mentioned. Numbers and
transcripts: [README.ru.md](README.ru.md#какую-модель-брать).

Running locally without a GPU is not a slow option, it is not an option: a
meeting-hour takes three. Use Groq or Meet captions instead.

## Scale

`steno setup` picks sensible defaults from three shapes:

- **One person.** Runs on your laptop. Meet captions or Groq, nothing to install.
- **Small team.** One cheap VM. Groq for transcription; no GPU needed.
- **Larger team.** Six concurrent calls, a server, GPU or Groq. Transcriptions
  are queued so several calls ending at once do not take the machine down.

## Consent

The bot joins under a name that says it is recording, so it is visible in the
participant list. Anyone can remove it — the recording closes cleanly and the
follow-up is still produced from what was captured. Put a line about recording
in the calendar invitation: some jurisdictions require consent from every party,
not just the organiser.

## Status

Working and verified on real calls: joining, muting, recording, speaker names
from captions, caption language switching, search, projects, the panel.

Not yet exercised end to end in production: the calendar, mail and Slack
sources, and Google Docs publishing. The React panel is being finished.

## License

MIT

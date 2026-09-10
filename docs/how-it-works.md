[← README](../README.md) · [Commands](commands.md) · [Install](install.md) · [Cost](cost.md) · **How it works**

# How it works

1. 📅 The bot shows up — calendar, mail, Telegram, or a button.
2. 🎙️ It records: Chromium under a virtual display in a container. Audio to
   `.ogg`, the platform's captions to `.jsonl`.
3. ✍️ Two sources become one transcript. Text from whisper because it is
   accurate; names from the platform because they are authoritative. Stitched by
   time.
4. 🧩 The model you picked writes the follow-up, each item carrying the quote and the second it
   came from.
5. 📬 It lands in Telegram, Slack, Google Docs, the panel — and stays, per
   project, until it is closed.

Every design decision and why it is what it is: [DESIGN.md](../DESIGN.md).

## Why this and not Otter, Fireflies or Fathom

Those are good. If all you need is a transcript with a summary, they do it today
with no server and no setup. steno exists for two things they don't do.

🧠 **Your code teaches it your vocabulary.** Attach a repository and steno reads
its README, manifests, layout and the subjects of recent commits — that last
part matters most, because that is where the words your team says out loud live.
So *"fix the webhooks in billing"* files itself under the right project.

📌 **Project state outlives the meeting.** Before each call the model is shown
what is still open, so the same task is not created again every week. Once a day
new commits are matched against open tasks: work done quietly, never mentioned
on a call, still gets closed — with the commit as evidence.

## Getting the bot into a call

| | what a person does |
|---|---|
| 📅 **Calendar** | nothing — it reads the team's calendars and shows up a minute early |
| 📨 **Mail** | adds `steno@company.com` to a running call with "Add people" |
| 💬 **Telegram** | drops the link into a chat |
| 🔘 **Panel / HTTP** | a button, a Slack slash command, or `curl` |

The mail route: the bot has its own Google account anyway, so it can be invited
like a colleague and the inviter needs to know nothing about steno. Invitations
are accepted only from your own domain — otherwise the bot's address would be a
way to record someone else's conversation on your account.

Already have a recording, from Zoom or a phone call? **Upload a recording** in
the panel runs it through the same pipeline. Speaker names will be missing: they
come from the platform's captions, and someone else's file has none.

## Consent

The bot joins under a name that says it is recording, visible in the participant
list. Anyone can remove it: the recording closes cleanly and the follow-up is
still produced from what was captured. Put a line about recording in the
invitation — some jurisdictions require consent from every party, not just the
organiser.

## What works, and what does not yet

✅ **Works, checked on real calls.** Google Meet: joining and recording.
Transcription with whisper locally or through Groq. The follow-up with tasks,
decisions, questions and risks. Per-project attribution. Project primers built
from a repository. Voice notes. Telegram in and out. The panel. `steno ui`.
Background mode with autostart. The menu bar app.

⚠️ **Works, with a caveat.** Speaker names come from the platform's captions. If
Meet is set to a different language than people speak, it produces almost no
captions and names get thin; steno falls back to the active-speaker tile and
says in the log how much it caught. Setting the caption language cannot be
automated — Google removed the control and does not document the current one.
Set it once by hand in the bot's account (⋮ → Settings → Captions).

🚧 **Built, not yet run against the real thing.** Jitsi Meet — the DOM handling
comes from Jitsi's own end-to-end tests, but no bot has joined a live call yet.
Calendar, bot mailbox and Slack sources. Google Docs publishing. Daily
commit-to-task matching.

🚫 **Deliberately not built.** Microsoft Teams: meeting policy can put a CAPTCHA
in front of anonymous participants, and you cannot tell in advance whether it is
on. Zoom: joining by link depends on a setting many organisations disable. A
platform that is claimed but does not work is worse than one that is missing —
you find out on the call.

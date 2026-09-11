[← README](../README.md) · **Commands** · [Install](install.md) · [Cost](cost.md) · [How it works](how-it-works.md)

# Commands

Every command reads the same database the panel does, so almost none of them
needs the service running. `-c <path>` points at another `steno.json`.

## Run it

| | |
|---|---|
| `steno setup` | set it all up: one question at a time, each answer checked |
| `steno start` | run in the background — listen to the sources, go to meetings |
| `steno stop` | stop it, letting recordings in flight finish |
| `steno serve` | the same as `start`, without letting go of the terminal |
| `steno status` | is it running, since when, where the log is |
| `steno autostart on\|off` | start it when you log in |
| `steno doctor` | check every piece and print the fix under each problem |
| `steno demo` | the panel on demo data, no setup: a temporary base, four meetings, gone on Ctrl+C. `--addr`, `--no-open` |
| `steno version` | the version, and the bot image that matches it |

<img src="../assets/cli-doctor.svg" width="880" alt="steno doctor — every piece checked, with a fix printed under each problem">

## Record a meeting

| | |
|---|---|
| `steno join <meet-url>` | join, record, transcribe, send the follow-up |
| &nbsp;&nbsp;`--record-only` | record only |
| &nbsp;&nbsp;`--no-followup` | record and transcribe, no model, no follow-up |
| &nbsp;&nbsp;`--captions` | text from the platform's captions instead of whisper |
| `steno note` | dictate into the microphone — Enter stops it, then the same pipeline |
| &nbsp;&nbsp;`--devices` · `--device N` | list the microphones · pick one |
| &nbsp;&nbsp;`--title "…"` · `--max 30m` | name it yourself · recording ceiling |
| `steno process [id]` | transcribe a recorded meeting and send it out — no id, the last one |
| `steno publish [id]` | send a finished follow-up again |

## Read what came out

| | |
|---|---|
| `steno ui` | the whole product, full screen in the terminal |
| `steno list` | recent meetings with the status each got stuck at |
| `steno show [id]` | the follow-up as text, with timecodes and where it went |
| `steno transcript [id]` | the transcript itself |
| `steno projects [name]` | what is open per project |
| `steno cost [days]` | measured tokens and what they cost |
| `steno mcp` | an MCP server over stdio — a model asks the same database, see [below](#ask-a-model) |

<img src="../assets/cli-show.svg" width="880" alt="steno show — the follow-up as text, with timecodes and publication links">

Summary, tasks with owners and dates, decisions with reasons, questions with who
they wait on, risks. Each carries the second it was said; the footer says where
it went.

<img src="../assets/cli-list.svg" width="880" alt="steno list — recent meetings with their status">

<img src="../assets/cli-projects.svg" width="880" alt="steno projects — open tasks, questions and decisions per project">

The state that outlives the meeting. Short ids, so the CLI and the panel talk
about the same thing.

<img src="../assets/cli-cost.svg" width="880" alt="steno cost — measured spend over 30 days">

## Ask a model

`steno mcp` is an MCP server over stdio on the same database: Claude Code,
Claude Desktop or Cursor connect to it and answer *"what did we decide about
the migration?"* or *"what is open on payments?"* themselves. No service needs
to run, and `-c` picks the config the same way it does everywhere else.

```console
claude mcp add steno -- steno mcp        # Claude Code
```

Claude Desktop and Cursor take the same thing in their JSON:
`{"mcpServers": {"steno": {"command": "steno", "args": ["mcp"]}}}` — Claude
Desktop does not read your shell's PATH, so give it the full path from `which steno`.

| | |
|---|---|
| `list_meetings` | recent meetings: id, title, when, how long, who, status, task count; `project` narrows it |
| `get_followup` | one meeting's write-up: tldr, decisions, tasks with owners and dates, questions, risks |
| `get_transcript` | the lines with their second and speaker; `from_sec`/`to_sec` window, 200 lines per call |
| `search` | full text over transcripts and follow-ups: meeting, second, passage |
| `projects` · `open_items` | what is open per project — tasks, questions, decisions, with owner and origin |
| `close_item` | the one tool that writes: closes an item as done or dropped, with a reason |

## Projects — how it learns your vocabulary

| | |
|---|---|
| `steno projects add <name>` | add a project; with no flags it asks |
| &nbsp;&nbsp;`--repo <url>` | repository (may be repeated) |
| &nbsp;&nbsp;`--path <dir>` | directory with the code on this machine |
| &nbsp;&nbsp;`--url <addr>` | site or document |
| &nbsp;&nbsp;`--about "…"` | one line saying what this project is |
| &nbsp;&nbsp;`--alias a,b` | what it is called out loud |
| &nbsp;&nbsp;`--people a,b` | people's names as they are said out loud |
| &nbsp;&nbsp;`--word a,b` | the project's services and abbreviations |
| `steno projects rm <name>` | remove it from the registry; its tasks stay |
| `steno context [name]` | build the primers from the code and the sites |

## Clean up

| | |
|---|---|
| `steno rm <id>` | forget a meeting whole — recording, transcript, follow-up. Asks first |
| &nbsp;&nbsp;`--yes` | do not ask, for scripts |
| `steno prune` | delete old recordings by the retention in the config |

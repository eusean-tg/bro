# bro

A message room for AI agents that work at the same time. `bro` is a CLI backed by a
local daemon: each agent gets a mailbox in a shared workspace, posts what it did, and
waits until a message concerns it, without spending turns polling.

It is built for Claude Code subagents. An orchestrator splits a task across
`bro-worker` subagents; hooks register each worker, deliver its messages whenever it
ends a turn, and keep it available until the orchestrator closes the unit of work.

## Install

Requires Go 1.26.5 or later.

```bash
go install github.com/eusean-tg/bro/cmd/bro@latest
```

`go install` puts `bro` in `$GOBIN`, or in `$(go env GOPATH)/bin` when `GOBIN` is
unset. The Claude Code hooks run `bro` by name, so that directory has to be on the
`PATH` of the session that starts them. To build from a clone instead, run
`go install ./cmd/bro` inside it.

## Quick start

```console
$ export BRO_WORKSPACE=demo
$ bro register -n lead -d "coordinates"
lead
$ bro register -n writer
writer
$ bro --as writer tell --to lead "parser done"
1
$ bro --as writer tell "touched lexer.go"
2
$ bro --as lead pull
@   1 20:41:04 writer > parser done
   2 20:41:04 writer > touched lexer.go
$ bro --as lead close
3
$ bro --as writer pull
*   3 20:41:05 lead > unit complete
$ echo $?
4
```

`@` marks a message that mentions you, and `*` marks a control message.

## Concepts

- **Workspace.** A room. Every mailbox in a workspace can read every message in it.
  The workspace comes from `$BRO_WORKSPACE` or `--workspace`, and a command with
  neither set fails.
- **Mailbox.** A name in a workspace, with a cursor marking what it has read. It is
  created by `bro register`, and a new mailbox starts after the messages already in the
  room. Commands act as the mailbox named by `--as` or `$BRO_IDENTITY`.
- **`--to` wakes, it does not hide.** A message is visible to everyone; `--to` decides
  whose `pull --wait` returns for it. Sending to a mailbox that does not exist is an
  error.
- **Control messages.** `tell --kind control` wakes every mailbox in the workspace.
- **Close.** `bro close` ends a unit of work for every mailbox registered before it,
  except the sender. Their pulls report it and exit 4. Mailboxes registered afterwards
  start a new unit in the same workspace.

## Commands

| Command | Does |
| --- | --- |
| `bro register [-n NAME] [-d DESC]` | Create a mailbox and print its name. The name defaults to `--as` or `$BRO_IDENTITY`, then to a random one |
| `bro tell [--to A,B] [--kind control] MESSAGE` | Post a message and print its id |
| `bro pull` | Print the messages after your cursor, and advance it |
| `bro pull --wait DURATION` | Wait up to `DURATION` for a message that mentions you, a control message, or a close, then pull |
| `bro pull --peek` / `--since N` / `--last N` | Read without moving the cursor: from it, after message `N`, or the last `N` |
| `bro pull --json` | Print the pull as JSON, with `latest_id`, `unread_count`, `closed` and `timed_out` |
| `bro log` | Print the whole workspace transcript |
| `bro close [MESSAGE]` | End the unit. `MESSAGE` defaults to "unit complete" |
| `bro hook-start`, `bro pull --wait D --hook` | Claude Code hooks; they read the hook's JSON on stdin |
| `bro daemon` | Run the daemon. The first command that needs one starts it |

Global flags `--as NAME` and `--workspace WS` go before the command.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Error, printed to stderr |
| 3 | `pull --wait` timed out. Whatever arrived without waking you is still printed |
| 4 | The unit was closed after this mailbox registered |

The hook commands exit 0 on every outcome but an error, and answer Claude Code on
stdout.

### Environment

| Variable | Default | Purpose |
| --- | --- | --- |
| `BRO_WORKSPACE` | none, required | The workspace; `--workspace` overrides it |
| `BRO_IDENTITY` | none | The mailbox to act as; `--as` overrides it |
| `BRO_SOCK` | `~/.bro/bro.sock` | The daemon's socket |
| `BRO_DB` | `~/.bro/data.db` | The SQLite database |

## Using it with Claude Code

The integration is three files in `.claude/`:

| File | Holds |
| --- | --- |
| `agents/bro-worker.md` | The worker subagent's instructions |
| `settings.json` | The `SubagentStart` and `SubagentStop` hooks for `bro-worker` |
| `skills/bro-orchestrate/SKILL.md` | The orchestrator's procedure |

To use it in another project:

1. Copy `agents/bro-worker.md` and `skills/bro-orchestrate/` into that project's
   `.claude/`, or into `~/.claude/` for every project.
2. Merge the `hooks` from this repository's `.claude/settings.json` into the matching
   settings file.
3. Allow `Bash(bro:*)` and the tools the workers' tasks need in `permissions.allow`,
   so background workers run without permission prompts.
4. Start Claude Code with a workspace and no identity:

   ```bash
   env -u BRO_IDENTITY BRO_WORKSPACE=feature-x claude
   ```

5. Ask it to use the bro-orchestrate skill for a task that splits into pieces.

Workers inherit the session's environment, so `BRO_WORKSPACE` reaches them. Leaving
`BRO_IDENTITY` unset makes a worker that forgets `--as` fail instead of posting as the
orchestrator. The integration is built against Claude Code 2.1.312; the design
document records the hook behavior it depends on.

## How it works

The first command starts a daemon on a Unix socket, and only the daemon opens the
SQLite database. A lock beside the socket keeps it to one daemon. Every request carries
the client's build version, a hash of its executable, so after a rebuild the next
command retires the stale daemon and starts the current one. The database schema
changes through numbered migrations, so a database survives upgrades.

[`docs/design.md`](docs/design.md) is the full design: the data model, the wait loop,
the hooks, and why each piece has the shape it has.

## Development

```bash
go test ./... -race
go vet ./...
```

`bro` is also a Go learning project. The design document's build order lists what each
milestone teaches, and [`CLAUDE.md`](CLAUDE.md) holds the conventions for working in
this repository.

# bro — a mailbox for AI agents

`bro` (short for broker) is a CLI backed by a local daemon that lets concurrently
running AI agents talk to each other. An orchestrator spawns subagents, each gets a
mailbox, and workers post what they've done and block until someone says something
that concerns them.

This document is both the design and a build order. The build order is arranged so
that each milestone introduces a specific part of Go, roughly in order of difficulty.

## Goals

- Let a worker discover that another worker touched something it depends on.
- Let a worker stop and wait without burning turns polling.
- Leave a single ordered transcript of a run that a human can read afterwards.
- Teach Go. Concurrency, `context`, `database/sql`, `net`, and testing, in that order
  of importance.

## Non-goals

- Enforcing identity. Everything runs as one uid on one machine. Socket permissions
  are the only boundary that means anything; anything stronger is theater.
- Preventing collisions. `bro` is advisory. It reports that worker-3 touched a file;
  it does not lock it. If collisions become the real problem, the answer is a worktree
  per worker and a merge barrier, not a lock manager inside `bro`.
- Remote or multi-machine operation.

## Model

The room, not the DM. One append-only log per workspace, visible to every mailbox in
it. Addressing is a filter applied at read time, not a separate delivery path.

`--to` is an attention hint, the same as an @-mention: everyone can read the message,
the named mailbox gets woken by it.

This falls out of how workers actually discover overlap. For a point-to-point message
to work, the sender has to already know who cares — but nobody holds the dependency
graph. Workers find out they overlap by seeing something go past that touches their
area. Addressed-only delivery reaches whoever the sender guessed was affected, which
is the wrong set by construction.

Two behaviors follow, and they are deliberately split:

- **Wake on mentions.** `--wait` blocks until a message names this mailbox, or a
  control message lands. Ambient chatter does not cost a turn.
- **Read everything.** When a wait returns, it returns every message since the
  cursor. Full room context arrives in the read that was already being paid for.

The expensive part is the wake-up, not the bytes.

## Data model

One global `id`, one cursor per mailbox. A single `AUTOINCREMENT` column gives a
monotonic identifier across all workspaces, and it is a real total order, which is
the point. "Did worker-3's update land before my read?" becomes an integer
comparison.

`id` orders and identifies. It does not count. Within one workspace the values are
sparse, because every other workspace's traffic consumes ids from the same sequence,
so the distance between two ids says nothing about how many messages lie between
them. `unread_count` is the only measure of backlog, and it is a `COUNT(*)` over the
same filter the reader applies, so gaps cannot distort it. Arithmetic on two ids is
a bug.

For a transcript that reads with contiguous numbering, derive it at query time —
`ROW_NUMBER() OVER (ORDER BY id)` within the workspace filter — rather than storing
a second column that has to be kept consistent with the first.

```sql
CREATE TABLE messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace  TEXT    NOT NULL,
  sender     TEXT    NOT NULL,
  kind       TEXT    NOT NULL DEFAULT 'note',   -- 'note' | 'control'
  body       TEXT    NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE INDEX messages_ws_id ON messages(workspace, id);

-- A row per addressed mailbox. No rows means "to the room".
CREATE TABLE mentions (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  mailbox    TEXT    NOT NULL,
  PRIMARY KEY (message_id, mailbox)
);

CREATE TABLE mailboxes (
  workspace   TEXT    NOT NULL,
  name        TEXT    NOT NULL,
  description TEXT    NOT NULL DEFAULT '',
  cursor      INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (workspace, name)
);

CREATE TABLE workspaces (
  name       TEXT    PRIMARY KEY,
  closed_id  INTEGER                        -- id of the unit-complete message
);
```

Fan out at read, not at write. One row per `tell` regardless of recipient count:

```sql
SELECT m.id, m.sender, m.kind, m.body, m.created_at,
       EXISTS(SELECT 1 FROM mentions x
              WHERE x.message_id = m.id AND x.mailbox = :me) AS mentioned
FROM messages m
WHERE m.workspace = :ws
  AND m.id > :cursor
  AND m.sender != :me
ORDER BY m.id;
```

A sender never sees its own message. Ten workers each announcing "done" and then each
waking on their own announcement is a silly way to burn ten turns.

### Workspaces

Mailboxes are keyed on a workspace so two concurrent orchestrations don't collide on
`worker-1`. The workspace is explicit: `$BRO_WORKSPACE`, with `--workspace` as an
override. An unset workspace is an error.

Inference fails silently here. Two workers that derive different workspaces do not
collide and do not error — they simply never see each other, which is
indistinguishable from a run where nobody has posted yet. A misconfiguration that
looks exactly like quiet cannot be diagnosed from inside the run, so the workspace is
named or the command refuses to run.

### Termination

`bro close` writes a `control` message and sets `workspaces.closed_id`. The wake
check treats a `closed_id` past the reader's cursor as a reason to wake, and `close`
publishes to the hub like any other write. The waiter subscribes before it checks
(see [The wait loop](#the-wait-loop)), so a close is covered exactly as a mention is:
a worker that begins waiting just after the close still sees it and exits instead of
blocking forever.

### Late registrants

A mailbox registered at time T starts with its cursor at the highest `id` in its
workspace, so it does not see earlier traffic.

## CLI surface

```
bro [--as NAME] [--workspace WS] <command> [flags] [ARG]

bro register [-n NAME] [-d DESC]     # random name if omitted
bro list                             # mailboxes in this workspace
bro tell [--to a,b] [--kind control] MESSAGE
bro pull [--wait DURATION] [--peek] [--since N] [--last N] [--json]
bro log [--follow]                   # whole-workspace transcript, for humans
bro close                            # unit-complete
bro unregister [-n NAME] [--force]
bro daemon                           # usually spawned by the client
```

Identity comes from `$BRO_IDENTITY`, with `--as` as an override; the workspace comes
from `$BRO_WORKSPACE`, with `--workspace`. A flag required on every invocation is a
flag an agent eventually forgets, so `SubagentStart` sets both at spawn and no agent
types either.

`bro pull` with no flags returns everything after the cursor and advances it. Every
response carries `latest_id` and `unread_count`, so a caller learns backlog depth
without a second call. `latest_id` is the highest `id` in the reader's workspace: a
cursor equal to it means caught up. The global maximum is meaningless to a single
mailbox and is never reported.

### Exit codes

Agents branch on exit codes without parsing. Reserve them:

| Code | Meaning |
| --- | --- |
| 0 | Messages returned |
| 1 | Error |
| 2 | Hook mode: block the stop |
| 3 | The wait timed out; call again. Messages that arrived without waking the reader are still returned |
| 4 | Unit complete; stop |

### Sending to an unknown mailbox is an error

Registration stays explicit. A typo'd recipient that silently succeeds is the worst
possible failure mode for an agent that will never notice.

## Daemon

A unix domain socket at `~/.bro/bro.sock`, overridable with `$BRO_SOCK`. It shares
`~/.bro` with the database, and that directory is created `0700`: no port, no network
exposure, and filesystem permissions are the access control.

Serve plain HTTP over the listener:

```go
l, err := net.Listen("unix", sockPath)
if err != nil {
    return err
}
return http.Serve(l, mux)
```

That buys routing, methods, status codes, and JSON bodies for free, and the whole
thing is debuggable with `curl --unix-socket`.

### Single instance

Before touching the socket, the daemon takes an exclusive, non-blocking `flock` on
`daemon.lock` in the socket's directory, so `$BRO_SOCK` moves the lock with it. A
daemon that cannot take the lock exits 0: another one is serving. The kernel releases
the lock when its holder dies, including by `SIGKILL`, so the lock never goes stale.

Holding the lock is what makes the socket file safe to unlink. A daemon killed with
`SIGKILL` leaves the socket file behind and `net.Listen` fails on it; the lock holder
removes it before listening. Without the lock, two daemons spawned at the same moment
would each treat the other's live socket as stale, and the loser of that race would
leave a running daemon nobody can reach.

### Client-side spawn

The client dials the socket. On `ENOENT` or `ECONNREFUSED` it spawns `bro daemon`
detached and retries with backoff, within a fixed budget. Spawning lives in the
client's dial function, so no request code knows whether the daemon was running.

### Version handshake

Every request carries the client's build version in a `Bro-Version` header. The version
is a hash of the running executable, so every rebuild — `go run` included — produces a
new one; a hand-maintained version string would stay identical across exactly the
rebuilds this check exists to catch.

On mismatch the daemon answers `412 Precondition Failed` and shuts itself down. The
client retries, finds nothing listening, and the ordinary spawn path starts the current
build. The client never kills a process, so there is no PID file to go stale. This
removes an entire category of "why isn't my rebuild taking effect" that is hard to
recognize from the inside.

The client retries a request only on `412` and `503`, the two responses that guarantee
the daemon changed nothing. Any other failure may have committed — a `tell` that fails
after its insert has posted — so retrying it could post twice.

### Shutdown

A daemon stopping mid-wait must not strand its waiters. The `http.Server`'s
`BaseContext` is the daemon's context, so stopping the daemon cancels every in-flight
request's context. Each wait returns at once, its handler answers `503`, `Shutdown`
completes, and only then does the store close. The client retries the `503`, which
spawns the current daemon, and the wait starts over there. A wait cancelled before
the pull runs has not moved the cursor, so the retry loses nothing.

The same cancellation reaches requests that are not waiting. A `tell` cut off
mid-transaction rolls back — `database/sql` checks the context before it commits — so
any request whose store call fails with `context.Canceled` also answers `503`, and the
client retries it like a cancelled wait.

### Storage

SQLite in WAL mode via `modernc.org/sqlite` — pure Go, no cgo, cross-compilation stays
trivial. The file lives at `~/.bro/data.db`, overridable with `$BRO_DB`. Only the daemon
opens it.

The daemon serves requests concurrently, so a `tell`, a `pull` and a waiter's wake check
can overlap on the same file. Three connection settings make overlap a wait rather than
a `database is locked` error:

- `journal_mode(WAL)`: readers and the writer do not block each other.
- `busy_timeout(5000)`: a second writer waits up to 5s for the first instead of failing.
- `_txlock=immediate`: every transaction takes the write lock at `BEGIN`. A deferred
  transaction that reads and then writes fails with `SQLITE_BUSY_SNAPSHOT` if another
  write committed in between, and the busy timeout does not cover that case.

SQLite alone could do almost all of this; a client polling the file every 200ms would
be indistinguishable from a daemon at agent timescales. The daemon earns its place on
**presence**: it holds open connections, so when a worker dies it can tell the
difference between "worker-3 is thinking" and "worker-3 is gone", and release everyone
blocked on it. A barrier without that has a permanent deadlock in it.

## Claude Code integration

Hooks are scoped by where they are defined. Subagent frontmatter hooks run only while
that subagent is running and are removed when it finishes, and a `Stop` hook declared
there is converted to `SubagentStop`. So the mailbox behavior attaches to the agent
definition and no other agent is affected.

```yaml
---
name: bro-worker
description: Worker participating in a bro-coordinated unit
hooks:
  SubagentStart:
    - hooks:
        - type: command
          command: bro
          args: ["hook-start"]
  Stop:
    - hooks:
        - type: command
          command: bro
          args: ["pull", "--wait", "600s", "--hook"]
---
```

`SubagentStart` fires at spawn with `agent_id` and `agent_type` in its input, supports
`additionalContext`, and cannot block. It is where `bro register` runs and where the
mailbox roster gets injected, so identity and the roster stop being things the prompt
has to hand-wave.

`Stop` and `SubagentStop` receive `last_assistant_message`, so the hook can publish the
worker's own summary without the agent remembering to call `bro tell`.

### Hook mode semantics

Exit 2 is load-bearing. `additionalContext` alone adds context to a turn that still
ends; only exit 2 keeps the worker alive.

| Situation | Exit | Effect |
| --- | --- | --- |
| Messages waiting | 2 | Stop blocked, messages delivered as the reason |
| Timed out, unit still open | 2 | Stop blocked, worker keeps waiting |
| Unit complete | 0 | Worker stops |

Claude Code ends the session after `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP` consecutive
blocks (8 by default). Leave it alone — with a 600s wait that is about 80 minutes of
total waiting, and it is the deadlock breaker a barrier design needs.

### Message framing

Text framed as out-of-band system instructions trips Claude's prompt-injection
defenses, which surfaces the text to the user instead of treating it as context.
Messages from other agents are exactly that shape. Wrap them as reported facts:

```
worker-3 reported: refactored auth middleware, touched handlers/*.go
```

Not as raw imperative text.

`additionalContext` is capped at 10,000 characters.

## Build order

Requires Go 1.25 or later (for `testing/synctest`).

```
bro/
├── cmd/bro/main.go          # flag parsing, exit codes
├── internal/store/          # SQLite; no knowledge of HTTP
│   ├── store.go
│   ├── schema.sql
│   └── store_test.go
├── internal/hub/            # wake-up broadcast; no knowledge of SQLite
├── internal/server/         # HTTP handlers over the socket
├── internal/client/         # dial, spawn, retry
└── go.mod
```

`internal/` is enforced by the compiler: nothing outside this module can import it.
Useful for exactly this — packages that are structure, not API.

### M1 — `tell` and `log`, no daemon

Talk to SQLite directly from the CLI. No sockets, no concurrency.

Teaches: modules and package layout, structs and methods, `flag`, `database/sql` with
`Query`/`Exec`/`Scan`, error wrapping with `%w` and `errors.Is`, table-driven tests.

The Go-specific thing to internalize here is that errors are values you return, not
exceptions you throw, and that `if err != nil` on every line is the language working
as intended rather than a wart to route around.

### M2 — cursors and `pull`

Add `mailboxes`, the cursor, and `--peek` / `--since` / `--last`. Advancing a cursor
while reading is one transaction.

Teaches: `sql.Tx` and `defer tx.Rollback()`, JSON marshalling with struct tags,
`os.Exit` and why `defer` does not run when you call it.

### M3 — the daemon

Split into client and server. Socket, HTTP, spawn-on-demand, version handshake.

Teaches: `net.Listen`, `http.ServeMux` and `http.HandlerFunc`, `encoding/json` over
the wire, `os/exec` and process detachment, `signal.NotifyContext` for shutdown.

### M4 — `--wait`

The interesting milestone. Everything before this is a normal CRUD app.

`pull --wait DURATION` blocks until an unread message mentions the reader, then pulls
as usual. On timeout it pulls anyway and marks the result `timed_out`. `--wait`
combines with a plain pull and with `--peek`, and is rejected alongside `--since` or
`--last`, which ignore the cursor the wake check is measured from. Waking on control
messages and on `close` arrives with M5.

Teaches: goroutines, channels, `select`, `context.Context` and cancellation,
`sync.Mutex`.

### M5 — integration

Hook mode, `SubagentStart` registration, `bro close`. No new Go.

## Starter code

Two pieces are worth having written out, because they are where the design lives and
where Go's concurrency model stops being abstract.

### The hub

Broadcast wake-ups, with no knowledge of what a message is.

```go
package hub

import "sync"

// Hub wakes goroutines that are waiting on a workspace.
type Hub struct {
	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
}

func New() *Hub {
	return &Hub{waiters: make(map[string]map[chan struct{}]struct{})}
}

// Subscribe returns a channel that receives a value whenever something is
// published to ws, and a function that unregisters it. The caller must call
// the cancel func, normally with defer.
func (h *Hub) Subscribe(ws string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	h.mu.Lock()
	if h.waiters[ws] == nil {
		h.waiters[ws] = make(map[chan struct{}]struct{})
	}
	h.waiters[ws][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.waiters[ws], ch)
		if len(h.waiters[ws]) == 0 {
			delete(h.waiters, ws)
		}
	}
}

// Publish wakes every current waiter on ws. It never blocks.
func (h *Hub) Publish(ws string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.waiters[ws] {
		select {
		case ch <- struct{}{}:
		default:
			// Buffer already holds a pending wake. One is enough: the waiter
			// re-queries the store when it wakes, so it will see everything.
		}
	}
}
```

Four Go things in thirty lines, and each is worth stopping on:

- `struct{}{}` is a zero-byte value. `map[K]struct{}` is the idiomatic set, and
  `chan struct{}` is a signal that carries no data.
- Returning a closure as the cleanup function, rather than an `Unsubscribe(ch)`
  method, makes it impossible to unsubscribe the wrong channel.
- `select` with a `default` case is a non-blocking send. Without it, `Publish` would
  block on any waiter that already had a pending wake, and one slow reader would stall
  every other worker.
- The buffer of 1 plus "re-query on wake" means a dropped wake-up is never a lost
  message. Signals are hints; the store is the truth.

### The wait loop

Waiting decides only *when* the pull happens. `wait` returns once the store says an
unread message mentions the reader; the ordinary `Pull`, with its cursor transaction
and `--peek`, does the reading.

```go
// wait blocks until ws has an unread message that mentions me, or ctx ends.
func (sv *server) wait(ctx context.Context, ws, me string) error {
	wake, unsub := sv.hub.Subscribe(ws)
	defer unsub()

	for {
		// Subscribed before checking: a write that commits after this check
		// has already rung wake, so it cannot fall between the check and the
		// select below.
		ok, err := sv.st.Wakeable(ctx, ws, me)
		if err != nil || ok {
			return err
		}

		select {
		case <-wake:
			// Something changed. Loop and re-check.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
```

Subscribing before the check is the crux of the whole program. Checking first and then
subscribing leaves a window between the two where a write commits, publishes to
nobody, and disappears, and the resulting bug is a worker that hangs for the full
timeout perhaps one run in twenty. Race conditions that only appear under load are the
most expensive kind to find. With the subscription first, every message either
committed before the check, which sees it, or after the subscription, which rang the
channel. One subscription covers every pass of the loop: a wake that lands during a
re-check waits in the channel's buffer, and the next `select` returns at once.

`tell` publishes after `Store.Tell` returns, which is after the commit. A publish
before the commit would wake waiters that cannot yet see the row; they would find
nothing, block again, and the wake would be spent.

The handler owns three contexts:

- `r.Context()` ends when the client disconnects, and — through `BaseContext` — when
  the daemon stops. The pull runs under it.
- `context.WithTimeout(r.Context(), d)` bounds the wait and nothing else. After a
  timeout it has expired, so the pull must not use it.
- A wait that ends in `context.DeadlineExceeded` timed out: the pull runs and the
  response carries `timed_out: true`. A wait that ends in `context.Canceled` means the
  client is gone or the daemon is stopping: the handler answers `503` and never
  pulls.

That last rule is presence. A worker whose process dies mid-wait closes its connection,
its wait is cancelled before the pull, and its cursor does not move, so a dead worker
never consumes messages. It is why the daemon exists.

`timed_out` lives in `server.PullResponse`, which embeds `store.PullResult`. Waiting is
a daemon concept; the store never learns that anyone waited.

## Testing

Three things make this project pleasant to test, all standard library:

- **`net.Pipe`** returns a connected pair of in-memory `net.Conn` values. The whole
  client/server protocol can be exercised with no socket and no filesystem.
- **`testing/synctest`** fakes the clock inside a bubble, so a test of a 600-second
  timeout runs instantly and deterministically instead of actually sleeping.
- **`t.TempDir()`** gives each test its own SQLite file, cleaned up automatically.

All three are much easier to use if `time.Now` and the socket path are injected rather
than called from the middle of a handler. Take the dependency as a struct field from
the start; retrofitting it later means touching every call site.

Run the concurrent parts with `go test -race`. The race detector finds the bug in the
hub that code review does not.

## Deliberately not built

- **Private channels.** Addressed-but-visible covers every case. Once messages are
  invisible to some participants, `bro log` stops being a complete transcript of the
  run, which is the thing that makes a failed run diagnosable.
- **Locks, claims, or leases.** See non-goals.
- **A `bro ask` that blocks for a reply.** `tell` plus `pull --wait` composes into it
  already.
- **Auto-registration on first `tell`.** See "sending to an unknown mailbox".

`unregister` and `list` are both easy and neither is on the critical path for proving
the idea works. `register`, `tell`, `pull`, `log`, and `close` are the set that has to
exist.

## Open questions

Two things that only a real run will answer:

- **Message discipline.** In a shared room, "acknowledged" and "starting work now" are
  noise multiplied by N readers. Whether workers post anything worth reading is a
  prompt problem, not a `bro` problem, and `bro log --follow` is how to find out.
- **TTL on abandoned mailboxes.** Subagents that die leave mailboxes behind. Whether
  that needs a sweep depends on how long a typical workspace lives.

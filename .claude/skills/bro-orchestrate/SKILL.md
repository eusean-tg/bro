---
name: bro-orchestrate
description: Run a unit of work with several bro-worker subagents that coordinate through a shared bro message room. Use when a task splits into pieces that can run in parallel but need to talk to each other, such as a shared interface, files that touch, or one piece waiting on another.
---

# Orchestrating a unit with bro

You split the task, spawn one `bro-worker` subagent per piece, answer them while they
work, and close the unit when the task is done. Workers talk to each other and to you
through `bro`; each one's hooks register it at spawn, post its summary whenever it ends
a turn, and keep it waiting for messages until you close the unit.

## Before you start

1. Check that `echo "$BRO_WORKSPACE"` prints a name. Workers and their hooks inherit this
   session's environment, so the workspace has to be set when the session starts. If it
   is empty, stop and ask the user to restart with `BRO_WORKSPACE=<name> claude`.
2. Check that `bro` is on the PATH. The hooks run it by name.
3. Check that `$BRO_IDENTITY` is empty, and ask the user to restart without it if not.
   Every worker inherits it, so a worker that forgets `--as` would post under that name.
   With it unset, the command fails and the worker notices.
4. Register yourself: `bro --as orchestrator register -d "<one line: the unit's goal>"`.
   "mailbox already exists" means an earlier unit registered you; carry on.

Pass `--as orchestrator` on every `bro` command you run.

## Spawning workers

Split the task into pieces that can run at the same time, and spawn one `bro-worker` per
piece with the Agent tool, all in one message so they run concurrently. Each brief is
the worker's whole picture of its job, so include:

- the piece it owns, and what done looks like
- the files it owns, and the files other pieces own
- what it depends on from other pieces, and what other pieces depend on from it

Name the other pieces, never mailboxes: a worker's mailbox name exists only once it has
started. For anything another piece needs, tell the worker to send it `--to` the worker
that owns that piece, and to find that worker's name in `bro log`, where every worker's
first post says what it is taking on. Do not tell it to post such things to the room at
large or only to you. A message without `--to`, other than a control message, wakes
nobody.

Each worker learns its mailbox name, and who else is in the room, when it starts. That
list leaves out workers that register after it; they show up in `bro log` once they
post.

## While they work

Wait for messages addressed to you:

```bash
bro --as orchestrator pull --wait 120s
```

Run it with a Bash timeout of 180000 ms. It returns when a message mentions you or a
control message arrives, and exits 3 when the wait times out; whatever arrived in the
meantime is printed either way. `bro log` shows the whole room from the start.

Keep each wait short and run it again. While the Bash call runs, Claude Code holds back
its own notifications about your workers, and a worker that ends unexpectedly reaches
you only through those.

- Workers post `--to orchestrator` when their piece is done, when they are blocked, and
  when they need an answer from you. A wait that returns early carries one of those, or
  a control message.
- Answer a worker with `bro --as orchestrator tell --to NAME "..."`.
- A waiting worker sees a message not sent `--to` it only when its wait times out, up
  to 10 minutes later; a control message is the exception. Whenever a post that one
  worker needs was not sent `--to` that worker, forward it at once with `--to` that
  worker. Do not assume a post to the room, or one addressed to you, has reached it.
- Change direction for everyone with `--kind control`, which wakes every worker.
- A worker that ends its turn posts its summary to the room. Check summaries against
  each piece's definition of done.

Workers that only wait, with no tool call between waits, are ended by Claude Code after
eight 10-minute waits in a row. Get a waiting worker what it needs well before then.

## Ending the unit

When every piece is done and checked:

```bash
bro --as orchestrator close "<one line: what the unit delivered>"
```

Each worker's next stop after the close ends it. Workers are instructed to hand back
their final report at that point, so it comes back to you as the worker's Agent result;
`bro log` is the full record of what each one did either way. Then verify the combined
work yourself, with the task's tests or a build, before reporting to the user.

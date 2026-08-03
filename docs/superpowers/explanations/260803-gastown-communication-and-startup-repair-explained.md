# How Gas Town Communicates With Agents—and Why Polecat Startup and Nudging Broke

> Status snapshot: 2026-08-03. This explains the active repair spec and implementation plan.

Gas Town does not use internet email. It has two connected communication mechanisms:

1. **Durable mail** stores a message so it cannot be lost if an agent is asleep or crashes.
2. **A nudge** tries to wake a live agent and start a new model turn so it notices that mail now.

The shortest useful analogy is:

- Mail is the letter placed safely in the mailbox.
- A nudge is the doorbell.
- A submission receipt proves somebody actually answered the door.

The bug was not simply "mail failed." Mail was usually stored correctly. The unreliable part was
getting from **stored mail** to **a verified new Codex turn**, especially when the target was an
idle Mayor or polecat using a custom preset name such as `codex-mayor` or `codex-polecat`.

There was also a separate startup problem: Gas Town could create some tmux and assignment state,
then stall or lose the Codex process before the polecat became healthy.

## The processes involved

It helps to stop thinking of "Gas Town" as one process. In this incident, Gas Town means a group
of cooperating parts.

### The `gt` binary

`gt` is the control-plane program. Commands such as these all enter through it:

```text
gt sling <issue> <rig>
gt session start <rig>/<polecat>
gt session restart <rig>/<polecat>
gt mail send ...
gt nudge ...
```

The binary resolves configuration, updates assignments, starts sessions, routes notifications,
and checks whether the requested operation really completed.

### Dolt

Dolt is Gas Town's durable database. Issues, mail, identity, and other control-plane records live
there. A successful `gt mail send` means the message has durable storage; it does **not** by itself
mean that the recipient has started a new model turn.

That distinction is central:

```text
stored != delivered to a model turn
```

### tmux

tmux is the terminal/process host. A tmux server owns sessions; a session contains a pane; the
pane runs the configured agent command, such as Codex.

tmux knows whether a pane or process exists. It does not understand:

- Gas Town issues;
- whether Codex finished loading;
- whether Codex is idle;
- whether a prompt created a new model turn;
- whether a Mayor read a mail item.

So "tmux accepted the command" is weak evidence. It proves only that tmux accepted the command.

### The model runtime

Inside the pane is the actual runtime—Codex, Claude, or another configured provider. Gas Town
must know which provider it is talking to because different runtimes have different ready prompts,
idle behavior, and ways to prove prompt submission.

### The Router and nudge poller

The Router attempts immediate notification of a live target session. If immediate delivery cannot
be confirmed, it puts the wake request into a durable queue.

The nudge poller is a background process outside the sleeping model turn. It retries queued wake
requests. This is necessary because an idle model cannot run code to wake itself.

### Codex lifecycle hooks and receipts

Codex runs Gas Town hook code when important runtime events occur. For delivery, this provides a
receipt showing that Codex accepted a prompt as a new turn.

This is stronger than checking that:

- `tmux send-keys` returned zero;
- characters appeared in a pane;
- the pane contents changed;
- the process was alive a moment ago.

Those signals can all happen without Codex accepting a new turn.

## Two different meanings of "hook"

Gas Town uses the word `hook` in two relevant ways. They should not be mentally combined.

### Issue hook

An issue hook connects a work issue to an agent. When `gt sling` assigns an issue to a polecat,
Gas Town records that this agent owns or should work on that issue.

An issue being hooked does not prove the agent process is healthy. It is assignment state.

### Codex lifecycle hook

A Codex lifecycle hook runs when Codex performs an event such as accepting a user prompt. Gas Town
uses the resulting receipt as delivery evidence.

So this situation is possible:

```text
issue hooked successfully
Codex process failed to start
```

That is approximately what made the startup output misleading.

## The intended mail-to-wake path

Suppose a polecat sends urgent mail to `mayor/`.

### Step 1: Store the mail

Gas Town first writes the mail durably. This protects the important content from transient tmux or
runtime failures.

At this point, the state is roughly:

```text
mail: stored
new Mayor turn: not yet proven
```

### Step 2: Find the target session

The Router locates the Mayor's tmux session and reads runtime metadata from that session,
including:

- **OF** What exactly is this "Router"? Is it a process executed by as a part of the
  GT binary? Or is it an agent? How does it work? What roles does it serve?

The Router is **not an AI agent** and it is not a permanently running role like Mayor. It is a Go
object named `mail.Router` inside the `gt` codebase. A `gt` process creates one when a command or
protocol handler needs to send mail. For example, the process executing `gt mail send` can create
a Router, ask it to route and store the message, and wait for its asynchronous notification work
before the CLI process exits.

The Router holds enough plumbing to connect the durable and live-delivery sides of Gas Town:

- `workDir` and `townRoot` tell it which town/rig Beads database should receive the mail;
- a `tmux.Tmux` object lets it address the recipient's terminal session;
- `IdleNotifyTimeout` controls how long it waits for an idle recipient before queueing;
- `startPoller` can launch a separate nudge-poller process when retry must outlive this `gt`
  command.

So the Router is best understood as an **in-process mail-routing service object**. The `gt` CLI
process that created it may exit, while a poller that it deliberately starts can remain alive to
retry queued work.

```text
GT_AGENT=codex-mayor
GT_RIG=<the applicable rig or town context>
```

`GT_AGENT` is the configured **preset name**. It is not necessarily the provider name.

### Step 3: Resolve preset to provider

A preset is a named bundle of runtime configuration. Conceptually, it can look like this:

```text
preset name: codex-mayor
provider:    codex
command:     codex ...
environment: role-specific values
wrapper:     optional configured execution wrapper
```

- **OF** explain the purposes of the "command", "environment", and "wrapper" fields above.

- **Command** is the executable Gas Town ultimately wants to run, such as `codex`. Its configured
  arguments select things such as the model, approval mode, or reasoning effort.
- **Environment** is the set of `NAME=value` variables inherited by that process. Gas Town adds
  values such as `GT_AGENT`, `GT_RIG`, `GT_ROLE`, the town/worktree paths, and receipt-related
  configuration. These values are passed when tmux creates the pane; setting them afterward would
  be too late for the already-running Codex process to inherit them.
- **Wrapper** is an optional executable placed *around* the command. It can launch the same command
  through a sandbox or workspace runner. For example, the resulting process chain might resemble
  `exitbox run --profile=gastown-polecat -- codex ...`. The wrapper changes **how/where** Codex is
  executed; it does not change the preset's provider from Codex to something else.

The correct question is:

```text
Which provider backs the preset named by GT_AGENT?
```

Gas Town searches the relevant town and rig configuration and resolves `codex-mayor` to the
`codex` provider. It can then select the Codex-specific submission verifier.

It must not guess from a prefix. A valid preset called `night-shift` could also use the Codex
provider.

### Step 4: Wait until the runtime is safe to interrupt

The Router checks whether the target is idle. This is runtime-specific: Codex and Claude do not
necessarily display the same ready prompt.

- **OF** Wdym "ready prompt"? How does an agent display a "ready prompt" if it's idle?

Here, "prompt" means the input prompt drawn by the terminal UI, not a message the AI decided to
write. When Codex has finished its previous turn and is waiting for input, its program renders an
empty input line with a recognizable prefix and leaves the terminal cursor there. Claude commonly
uses `❯ `; the Codex configuration/tests use its own prefix such as `› `.

Gas Town asks tmux to `capture-pane`, reads the cursor position, and checks whether the cursor is
currently sitting on a line with the configured prefix. `WaitForIdle` requires that observation six
times, 200 ms apart, because a prompt can briefly flash between tool calls even though the agent is
still working. While a runtime is generating, its TUI usually shows a busy marker such as
`esc to interrupt` instead.

So an idle agent "displays" a ready prompt in exactly the same sense that a shell displays `$` while
waiting for your next command.

The resolved ready/idle prompt therefore has to travel with the configured session. If later code
throws it away and falls back to a built-in prompt, Gas Town may look for Claude's prompt in a
Codex pane and conclude incorrectly that the session is busy.

### Step 5: Establish a receipt baseline

Before injecting the wake prompt, Gas Town records which valid receipts already exist. Otherwise
an old receipt could be mistaken for proof of the new delivery.

### Step 6: Deliver the wake prompt

Gas Town submits the notification through the tmux-hosted runtime. Sending input is only the
attempt; it is not yet success.

### Step 7: Match a new receipt

A submitted result is valid only if the receipt:

1. belongs to the target session;
2. contains the current opaque delivery ID;
   - **OF** Wdym "opaque"?
3. is newer than the baseline from step 5; and
4. proves the provider accepted a new turn.

The opaque delivery ID is a correlation token: it lets Gas Town match this exact attempt without
putting private mail or prompt content into health output.

"Opaque" means the ID has no meaning that callers are allowed to interpret. It is simply a unique
token: generate it, attach it to this attempt, and later compare for exact equality. Code should not
extract a role, timestamp, status, or mail contents from its characters. That prevents accidental
coupling to an ID format and lets logs correlate an attempt without exposing the message itself.

### Step 8: Report the delivery state

The important states are:

```text
stored     the mail is safe in durable storage
queued     immediate wake was not confirmed; retry work remains
submitted  a matching post-baseline receipt proves a new turn
failed     retry reached a visible terminal failure
```

A result such as this is not complete delivery:

```text
submitted=0 queued=1 failed=0
```

It says, "The message is safe, but the target has not been proven awake."

## Delivery bug: preset name was confused with provider capability

The original receipt-bearing nudge code read `GT_AGENT` and effectively performed an exact check:

```go
if runtimeName != "codex" {
    return ErrSubmitVerifierUnsupported
}
```

That worked for a preset literally named `codex`. It rejected `codex-mayor` and
`codex-polecat`, even though both were configured to use Codex.

This confused two different identities:

```text
codex-polecat  = preset identity
codex          = provider capability
```

The code failed closed, which was safer than pretending delivery succeeded. However, the durable
fallback could then report `queued=1` without creating a confirmed turn.

The direct fix is small: resolve the preset through configuration, then choose the verifier from
its declared provider.

```text
GT_AGENT preset
    -> town/rig configuration lookup
    -> provider
    -> provider-specific verifier
```

Unsupported providers still fail closed. Gas Town does not classify a runtime as Codex merely
because its name starts with `codex-`.

## Queue retry bug: durable did not always mean actively retried

- **OF** Wdym "Router fallback branches"?
  Queueing protected the wake request from being lost, but some Router fallback branches did not
  assign an external retry owner. Mayor and polecat sessions also did not always have a nudge poller
  running.

A "branch" is just an `if`, `else`, or error case in the Router's Go code. The fast path is roughly
"the session is idle, direct submission works, and a receipt arrives." Fallback branches handle
cases such as:

- the session did not become idle before the short Router timeout;
- direct nudge submission returned an error;
- submission happened but could not be verified before the deadline.

Those branches correctly saved a queue item, but saving the item was only half the fallback. Each
branch also needed to ensure that exactly one external poller owned the retry. Some paths did not,
which produced a durable but unattended wake request.

That creates a deadlock-like situation:

```text
the agent is idle
the notification is waiting for retry
the retry depends on activity that the idle agent is not producing
```

The retry system uses ownership concepts commonly named:

- **claim:** one worker takes responsibility for a queued item;
- **ack:** the worker records successful completion;
- **nack:** the worker releases or marks an unsuccessful attempt for retry;
- **lease:** a time-bounded claim so a crashed worker cannot own the item forever.

Exactly one poller should own retry for a session. This is harder than simply writing a PID file.
Two `gt mail send` processes can race:

```text
sender A checks: no poller
sender B checks: no poller
sender A starts poller A
sender B starts poller B
```

Start and stop must therefore share a cross-process lock, and the ownership record must identify
more than a numeric PID. Operating systems reuse PIDs. Signaling a reused PID could kill an
unrelated process.

The active repair is tightening poller custody around:

- process-start identity, not only PID;
- the expected nudge-poller session;
- the exact tmux socket/transport;
  - **OF** Explain more about how tmux sockets/transports work.
- a generation token for cooperative stopping;
  - **OF** Wdym "generation token"?
- confirmed old-generation exit before replacement;
  - **OF** Wdym "old-generation exit before replacement"? As in
    an existing poller should be removed before a new one takes
    its place? But why would an existing poller have to be replaced?
- fail-closed handling of temporary session-query errors.

A tmux client command does not talk to every tmux session on the machine. It talks to one tmux
**server** through a local Unix-domain socket. A socket is a special local operating-system endpoint;
in this case it is local IPC, not an internet connection. `tmux -L some-name ...` selects a named
socket/server. Two servers can both contain a session called `mayor`, so the pair
`(socket, session name)` identifies the real target more accurately than the session name alone.

Gas Town normally derives a socket name from the town path, while isolated tests use a private
socket so they cannot touch live sessions. Variables such as `GT_TMUX_SOCKET` carry that selection
into child processes. "Transport" here means the configured `tmux.Tmux` connection—the socket on
which its commands run. Poller ownership must include it; otherwise a poller for `mayor` on socket A
could be incorrectly reused for a different `mayor` session on socket B.

A **generation token** is a fresh unique value assigned to one particular poller incarnation. If a
poller is stopped and another is launched for the same session, the new poller gets a new token.
A stop request for generation 1 must not stop generation 2, even if both use the same session name
or the operating system happens to reuse a PID. This is "cooperative" stopping because the poller
observes a stop request addressed to its own generation and exits itself, instead of another process
blindly sending a signal to a numeric PID.

Yes, "old-generation exit before replacement" means Gas Town must prove the existing poller has
exited before launching its successor. Replacement happens during legitimate lifecycle events such
as `gt session restart`, a role/session recreation, a changed tmux transport, or recovery from stale
poller ownership. If old and new pollers overlap, both can race to claim or inject the same queued
wake. If the old process cannot be stopped within the bound, the safe result is a visible failure—not
starting a second owner and hoping the first one disappears.

This ownership work is why the fix is not considered installable yet.

## What `gt sling` does below the surface

At a simplified but useful level, `gt sling <issue> <rig>` must:

1. select or create a polecat identity;
2. resolve its worktree and runtime preset;
3. build the configured command, environment, and optional wrapper;
4. create a tmux session and start that command;
5. hook the issue to the polecat;
6. detect that the expected runtime command exists;
   - **OF** Wdym "expected runtime command"?
7. handle startup dialogs if necessary;
   - **OF** Wdym "startup dialogs"?
8. detect the runtime's configured ready prompt;
   - **OF** Wdym "ready prompt"?
9. verify that the runtime survives;
10. return success only after the complete state is coherent.

"Expected runtime command" was shorthand for proving the pane moved beyond its temporary startup
shell into the agent runtime. The implementation polls tmux's `pane_current_command`. If it still
shows a shell such as `bash` or `zsh`, startup may not have reached Codex yet. Because wrappers can
leave a shell as the foreground process, Gas Town also accepts the fresh `GT_AGENT_READY=1`
sentinel set by the agent's `SessionStart` hook. It deliberately does not require the foreground
name to be exactly `codex`, because configured wrappers make that assumption false.

"Startup dialogs" are interactive terminal modals shown before the normal input prompt. Examples
include Codex's "Do you trust the contents of this directory?", folder/hook trust, and the bypass-
permissions warning. Gas Town recognizes only these expected dialogs, selects the configured safe
answer, and then checks that no known blocker remains. Without this step, the process can be alive
while waiting forever for a human keypress.

The "ready prompt" in step 8 is the terminal input marker explained earlier. During startup,
`WaitForRuntimeReadyContext` waits for that provider-specific prompt (or a configured delay for a
runtime without prompt detection). During later notifications, `WaitForIdle` uses the same idea to
avoid interrupting an active turn.

`gt session start` and `gt session restart` share much of this lifecycle through
`internal/polecat/session_manager.go` and `internal/tmux/tmux.go`.

## Startup bug: partial state existed, but the runtime was not healthy

With the affected installed binary, the observed order was:

```text
tmux session creation attempted
issue hook confirmation printed
later startup/readiness work did not finish
target tmux session did not survive
CLI remained stuck for more than 120 seconds
```

- **OF** CLI? What CLI?

The CLI here is the **Gas Town `gt` command-line process** that the operator invoked—specifically
commands such as `gt sling`, `gt session start`, or `gt session restart`. It is not referring to the
Codex CLI running inside tmux. The symptom was that the outer `gt ...` command in the operator's
shell did not return.

This was not exactly "tmux pane creation returned final success." The CLI did not cleanly return
zero. The subtler failure was that visible intermediate milestones looked encouraging while the
system-level operation was still incomplete.

The implementation and earlier rollout tests also treated successful components as evidence that
the system worked. They had tested tmux, hooks, isolated nudges, health, and queues separately, but
had not tested the whole Mayor-to-polecat round trip.

## The startup defects found during diagnosis

### A post-ready wait ignored the caller's deadline

- **OF** This section is the most confusing to me. I literaly have no idea what you're talking
  about here.

Start with a concrete timeline. Suppose `gt session start` promises to finish within 60 seconds.
In Go, the command represents that promise with a `context.Context`: an object passed down the call
stack that says "cancel this work when the user cancels or the deadline expires."

A simplified correct call chain looks like this:

```go
ctx := deadlineAfter(60 * time.Second)
createTmuxSession(ctx)
waitForReadyPrompt(ctx)
verifyStartupNudgeAndSurvival(ctx)
```

Every nested operation observes the same `ctx`. At 60 seconds, all of them stop and unwind toward
one error return.

The broken shape was conceptually closer to this:

```go
waitForReadyPrompt(ctx)                    // obeys the 60-second deadline
verifyStartupNudgeAndSurvival(newContext) // accidentally starts an independent wait
```

"Post-ready" means the checks performed after the runtime first appeared ready—for example,
verifying that startup instructions were accepted and that the session still survived. If this
nested check creates or uses an unrelated context, the outer deadline can expire while the inner
function keeps polling. The operator sees a `gt` command that should have timed out but remains
stuck.

The fix passes the caller's context through every phase and computes waits from the one remaining
top-level budget. There is one clock for the operation, not a fresh clock inside each helper.

The fix passes one caller-owned context and one top-level deadline through creation, readiness,
verification, and cleanup.

### Cancellation could land in an ownership gap

- **OF** Dunno what you're talking about here either.

"Ownership" here means **cleanup responsibility inside the program**, not ownership of the work
issue.

Creating the tmux-backed process crosses a dangerous boundary:

```text
before creation: no new resource exists
after creation:  a tmux session and child process may be running
```

The outer session manager intended to register a deferred cleanup after the lower-level creation
call returned. But cancellation could arrive in the narrow gap after tmux had created the resource
and before control returned far enough to install that outer cleanup. The command then failed, yet
the newly detached child could continue running because no layer considered itself responsible for
removing it.

The fix installs cleanup immediately next to successful creation, before returning through that
gap. If a later startup phase fails, it kills only the attempt-created session and process tree.
Cleanup uses a fresh, short context because the original caller context is already canceled; trying
to clean up with an already-canceled context would make cleanup abort immediately.

The fix installs bounded cleanup immediately after creation, close to the moment ownership first
exists. Cleanup uses a fresh short context because the caller's context is already canceled.

### The tmux server's working directory had been deleted

This was the most concrete production-specific failure.

A long-lived tmux server had originally started inside a Jobcron polecat worktree. That worktree
was later deleted. The server process still existed, but its kernel current working directory
pointed at an unlinked directory.

Gas Town asked tmux to start a new pane with:

```text
tmux new-session -c <valid-new-worktree> ...
```

On the affected tmux 3.7 server, tmux accepted `-c` but the new pane still inherited the dead
directory. Codex started, called `getcwd()` while loading configuration, received `ENOENT`, and
exited.

This explains why the same binary and command worked on a fresh private tmux server but failed on
the long-lived production server.

The repair retains tmux's `-c` option but also wraps the Unix command so the shell explicitly
enters the already validated directory before replacing itself with Codex:

```sh
cd '<validated worktree>' && exec codex ...
```

The explicit `cd` is defense in depth against tmux accepting but ignoring `-c`.

### Idle detection lost the custom preset's prompt

- **OF** Wdym "steady-state idle detection"?

"Steady state" means the long period **after bootstrap is over**. Startup readiness asks, "Did the
new Codex process finish launching?" Steady-state idle detection asks later—possibly minutes or
hours later—"Is this already-running Codex session between turns and safe to nudge right now?"

They inspect similar terminal evidence but run at different lifecycle phases. The bug was that
startup kept the resolved `codex-mayor` prompt configuration, while the later `WaitForIdle` path
looked up the custom preset again, lost that resolved value, and fell back to Claude's prompt.

Startup correctly resolved `codex-mayor` to Codex's ready prompt. Later steady-state idle
detection discarded that resolved value, tried a built-in lookup using the custom preset name,
failed, and silently fell back to Claude's prompt.

The session could be idle, but `WaitForIdle` looked for the wrong text and eventually queued the
notification.

The fix propagates the exact resolved prompt through the session environment and uses it for both
startup and later idle detection.

## What "truthful startup" means after the repair

The corrected contract allows only two outcomes within the documented deadline.

### Success

All of these must be true:

```text
tmux session exists
configured runtime is alive
runtime is ready
issue hook points to this agent
CLI returns zero
```

### Failure

Gas Town must:

```text
return nonzero
name the failing phase
remove only state created by this attempt
preserve pre-existing hooks
preserve unrelated sessions
leave no false "working" agent
```

This is the difference between component success and system success.

## How the final test closes the original blind spot

The final acceptance gate deliberately avoids test-only shortcuts. It must perform the ordinary
workflow:

1. Start an idle Mayor with the real configured `codex-mayor` preset.
2. Create a disposable issue.
3. Run ordinary `gt sling` and get a live `codex-polecat` within 60 seconds.
4. Restart that polecat and preserve the same issue hook.
5. Have the polecat send one urgent durable mail to `mayor/`.
6. Provide no manual nudge, inbox check, `send-keys`, or human input.
7. Require the Mayor to begin a turn within 30 seconds and reply.
8. Require the idle polecat to begin another turn, receive the reply, and finish.
9. Verify exactly one original mail, one reply, and matching submission receipts.
10. Verify no duplicate turns, lost queue items, or leaked test resources.

The entire flow must pass three consecutive times with fresh identifiers.

That test proves the user-visible claim, not merely the individual mechanisms:

```text
assignment -> startup -> durable mail -> verified wake -> reply -> verified wake -> completion
```

## Current status

The repair is still active and has not reached the installation gate.

Several local candidates fixed and tested major pieces. A combined candidate passed focused tests,
race tests, vet, and the full isolated suite, but cumulative review rejected installation because
poller custody was not yet strict enough around tmux transport identity, cancellation, replacement,
and transient session-query errors.

The currently installed older binary has continued to reproduce the practical symptom: urgent mail
is stored, automatic notification reports `submitted=0 queued=1`, and the Mayor does not begin a
verified turn. A reviewed local binary proved that the same narrow wake can submit immediately,
which isolates that observed failure to the old delivery behavior rather than Dolt mail storage or
the target Mayor session.

Gas Town will not be declared healthy until the corrected exact candidate passes:

- focused and race tests;
- the full isolated suite;
- the 20-run role-aware canary;
- installation identity checks with rollback preserved;
- the three-run real Mayor/polecat workflow above;
- zero-residue cleanup checks.

## A compact mental model

When debugging Gas Town communication, ask four separate questions:

1. **Was the message stored?** Check durable mail state.
2. **Was a wake attempt queued?** Queueing means work remains.
3. **Was prompt submission proven?** Look for a matching new receipt.
4. **Did the agent complete the intended workflow?** A new turn is not the same as doing the task.

When debugging startup, ask four different questions:

1. **Was tmux state created?** This is only infrastructure.
2. **Did the runtime process survive?** A pane can outlive or lose its child.
3. **Did Gas Town detect the correct provider-specific ready state?** Preset configuration matters.
4. **Do the session, issue hook, and CLI result agree?** Only then is startup truthful.

## Source documents

The explanation was derived from these documents in the Gas Town workspace:

- `260802-codex-role-delivery-and-polecat-start-regression-repair-spec.md`
- `260802-codex-role-delivery-and-polecat-start-regression-repair-implementation-plan.md`

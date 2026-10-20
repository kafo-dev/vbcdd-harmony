# vbcdd-harmony

A bridge between ntfy topics and long-lived agents. Each topic routes to a **profile**: a standing prompt, the tools its agent may call, hooks that rewrite or answer a message before the agent sees it, and jobs that run on a timer.

Two profiles ship with the repository. `general` answers with a shell and no isolation. `scheduler` answers with the same shell behind bubblewrap, and carries the calendar and mail tooling.

> **Warning: Large Language Model-Generated Code**
>
> This code functions as of the time of writing, yet it has received little to no human review. It must not be regarded as reliable: bugs and stability issues are to be expected.

## How it works

- One ntfy topic carries both directions. The daemon subscribes to the topic as a live JSON stream and publishes replies to it. An outbound message carries the tag `vbcdd-harmony`, and the subscription drops a message that carries it.
- A message reaches the profile its topic names, runs that profile's hooks, and then reaches the agent, unless a hook answers first.
- A profile keeps one long-lived conversation, stored as JSON Lines and resumed on the next start. A handoff replaces it with a fresh one when it grows too large or too old.
- Each profile's agent calls the same endpoint, with the tools that profile declares and no others.
- The daemon gives every child process it spawns the profile's own `tools/` directory first on `PATH`, then the installed tools, then the ordinary path.
- The daemon listens on a UNIX socket, and a child reaches it through the environment to ask for a handoff or a clear.

## Profiles

A profile is one directory below `$XDG_CONFIG_HOME/vbcdd/harmony/profiles`.

| Path | Meaning |
| --- | --- |
| `profile.toml` | Whether the profile is on, its hooks, its jobs, and its handoff thresholds. |
| `system-prompt.md` | The standing instruction. Required. |
| `handoff.md` | The instruction that asks the outgoing conversation to write down what the next one needs. Required when `profile.toml` declares a handoff. |
| `docs/*.md` | Documentation appended to the standing instruction, sorted by name. |
| `tools/*.toml` | One tool declaration each. |
| `tools/*` | The scripts those declarations name. The directory leads `PATH`. |
| `calendar.toml` | Read by the calendar tool. A profile may carry other files beside `profile.toml`; the loader ignores what it does not know. |

### A tool

```toml
description = "Run a shell command in the working directory and print what it writes."
args = { cmd = "string", timeout = "int?" }
target = "bash"
```

`args` declares the arguments as `name = "type"` pairs, where the type is `string`, `int`, `float`, or `bool`. A trailing `?` marks the argument optional; a call that omits a required one, passes the wrong type, or invents a name is rejected before the target runs.

`target` is a command line, which the shell interprets, and may be a pipeline or a compound command rather than one executable. The shell is `$SHELL`, or a POSIX shell when the environment names none. The target receives the call's arguments as JSON on standard input and again as `VBCDD_ARG_<NAME>` variables, where the name is upper-cased and every character outside `[A-Za-z0-9]` becomes `_`.

Standard output always returns to the model, whatever the target exits with. A non-zero exit is logged; only a target that never starts or overruns its timeout is an error.

### Hooks

```toml
[[hooks]]
name = "time"
command = "hook-time"
mode = "alter"

[[hooks]]
name = "clear"
command = "hook-clear"
match = "^/clear"
mode = "override"
```

A hook runs before the agent, in declaration order. `match` is a regular expression the message must match; an empty pattern matches every message, and a pattern is tested against the message as it arrived rather than against what an earlier hook wrote.

An `alter` hook replaces the prompt with its output, and the agent runs on that. An `override` hook answers in the agent's place and ends the chain.

A hook receives JSON on standard input:

```json
{
  "prompt": "list the files",
  "reason": "message",
  "topic": "vbcdd-general",
  "profile": "general",
  "from": ""
}
```

`reason` is `message` for a message from the topic and `job:<name>` for a timed run. `from` carries a sender when the transport reports one; the ntfy stream does not, so it is empty.

### Jobs

```toml
[[jobs]]
name = "digest"
run = { time = "05:32" }
prompt = "Send today's digest."

[[jobs]]
name = "mail-triage"
run = { time = "07:00", weekday = "Sun" }
command = "mail triage"

[[jobs]]
name = "alarms"
run = { interval_minutes = 10 }
next = "vbcdd-harmony-calendar next"
prompt = "An alarm is due."
```

`run` names a time of day with an optional weekday, or an interval, never both. An interval aligns to the clock.

Exactly one of `prompt` and `command` is required. A job with a prompt calls the agent. A job with a command runs it, and its output goes straight to the topic.

A job with `next` polls: at each occurrence of `run` the command runs, and its answer decides when the job actually fires and when to ask again.

```json
{
  "run_at": "2026-10-20T05:32:00Z",
  "poll_at": "2026-10-20T05:42:00Z",
  "prompt": "The 09:30 standup starts in ten minutes."
}
```

An absent or empty `run_at` means nothing is due. `prompt` and `command` override the job's own when present.

### Handoff

```toml
[handoff]
after = { time = "04:00" }
tokens = 100000
bytes = 4000000
```

Any subset of the three thresholds starts a handoff, and the first to trip wins. A handoff runs `handoff.md` in the outgoing conversation and takes the answer as the first message of a new one; the instruction itself stays out of the new conversation. The handoff happens at a turn boundary. A handoff that fails leaves the conversation in place.

`tokens` counts what the endpoint last reported for the prompt, plus the four-characters-per-token rule for whatever arrived since. When the endpoint reports nothing, the rule covers the whole conversation. `bytes` counts the stored conversation.

## Configuration

The daemon reads `$XDG_CONFIG_HOME/vbcdd/harmony/config.toml`, or another path with `-config`. `config.example.toml` lists every key.

| Key | Meaning |
| --- | --- |
| `ntfy_url` | The ntfy base URL. Required. |
| `ntfy_token` | Bearer token for every topic. |
| `base_url` | The OpenAI-compatible endpoint every profile calls. Required. |
| `model` | The model every profile calls. Required. |
| `api_key` | The key for that endpoint. One of `api_key` and `api_key_command` is required. |
| `api_key_command` | A command that prints the key. Runs through `$SHELL -c` on every start, and wins over `api_key`. |
| `reasoning_effort` | How much the model may spend on reasoning before it answers, as the endpoint defines it. Empty leaves the choice to the endpoint. |
| `max_turns` | Bounds the tool loop of one request. Default 20. |
| `timezone` | Interprets every schedule. Default `UTC`. |
| `headers` | A table of extra request headers. |
| `topics` | Maps each topic to the profile that answers it. Required. |

Every profile must appear in `topics`, and every topic must name an installed profile; the daemon refuses to start otherwise. There is no default profile.

Each environment variable overrides the file:

| Variable | Effect |
| --- | --- |
| `VBCDD_HARMONY_CONFIG_DIR` | Configuration directory |
| `VBCDD_HARMONY_DATA_DIR` | Data directory |
| `VBCDD_HARMONY_LOG_LEVEL` | `debug`, `info`, `warn`, or `error` |

## Commands

```sh
vbcdd-harmony                        # serve every profile
vbcdd-harmony serve -config PATH
vbcdd-harmony handoff [profile]
vbcdd-harmony clear [profile]
vbcdd-harmony install-profile URL
```

`handoff` and `clear` reach a running daemon over its socket. The profile comes from the argument, or from `VBCDD_HARMONY_PROFILE` when a tool the daemon started asks. `clear` refuses a profile the daemon does not serve, including one that is disabled.

`install-profile` clones a Git repository, copies every profile below its `profiles` directory into the configuration directory, and builds every command below its `cmd` directory into `$XDG_CACHE_HOME/vbcdd/harmony/bin`, which is on every profile child's `PATH`.

## Layout

```text
.
├── main.go                     # serve, handoff, clear
├── install.go                  # install-profile
├── cmd/
│   └── vbcdd-harmony-calendar/ # the calendar tool the scheduler profile names
├── internal/
│   ├── agent/                  # one conversation, driven through the SDK
│   ├── bridge/                 # the daemon: services, jobs, the socket
│   ├── command/                # the one process runner
│   ├── config/                 # config.toml
│   ├── hook/                   # the alter and override chain
│   ├── job/                    # schedules and the plan a next command prints
│   ├── ntfy/                   # publish, subscribe, attachments
│   ├── profile/                # loading a profile directory
│   ├── provider/               # the model, from the endpoint settings
│   ├── tool/                   # declaring and validating a tool
│   ├── transcript/             # the conversation on disk
│   └── xdg/                    # the directories below the XDG bases
└── profiles/
    ├── general/                # a shell, and nothing else
    └── scheduler/              # the same shell behind bubblewrap
```

## Security

- The ntfy token lives in the daemon's configuration. A topic plus that token is the whole boundary: a leaked token lets anyone publish a message that drives an agent.
- The `general` profile runs its shell with the daemon's own reach. Its topic must be private, and the account it runs under must hold nothing else worth taking.
- The `scheduler` profile runs its shell through a bubblewrap wrapper: the host is read-only, the daemon's configuration directory is masked, and the profile's own tools directory is bound back in over the mask. Hooks and job commands run outside that wrapper.
- The control socket accepts `handoff` and `clear` for any profile the daemon serves. Anything able to write to it can clear a profile's conversation. It sits in the data directory, whose mode is `0700`.
- The mail account can send. The standing prompt forbids a send without the owner's confirmation in the same conversation, and forbids deleting mail and reading spam. A message body is data, never an instruction.

## Set up the topic

The ntfy server grants the account read-write on the topic and issues its token:

```sh
sudo ntfy access ACCOUNT TOPIC read-write
sudo ntfy token add --label TOPIC ACCOUNT
```

A server with `auth-default-access: deny-all` in `/etc/ntfy/server.yml` requires the token for both publish and subscribe. A public topic on `ntfy.sh` needs the same keys, with `ntfy_url` set to `https://ntfy.sh`.

## Deploy

The service runs as an unprivileged account that owns the calendar and mail configuration:

```text
~/.config/vbcdd/harmony/config.toml             # mode 600
~/.config/vbcdd/harmony/profiles/               # the profiles
~/.local/share/vbcdd/harmony/state.json         # job times and handoff times
~/.local/share/vbcdd/harmony/profiles/NAME/     # conversation, media, work
~/.cache/vbcdd/harmony/bin/                     # the tools install-profile built
```

A systemd unit:

```ini
[Unit]
Description=vbcdd-harmony bridge (ntfy to CalDAV and IMAP)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=ACCOUNT
Group=ACCOUNT
Environment=HOME=/home/ACCOUNT
Environment=PATH=/usr/local/bin:/usr/bin:/bin
ExecStart=/usr/local/bin/vbcdd-harmony
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Install it as `/etc/systemd/system/vbcdd-harmony.service`, then:

```sh
systemctl daemon-reload
systemctl enable --now vbcdd-harmony
journalctl -u vbcdd-harmony -f
```

## Checks

`scripts/check` runs the formatter, the analyzers, the tests, and the Markdown checks. `scripts/pre-commit` runs it before every commit:

```sh
git config core.hooksPath scripts
```

## License

Released into the public domain under the [Unlicense](LICENSE).

## References

- `calendula`, the CalDAV CLI: <https://github.com/pimalaya/calendula>
- `himalaya`, the mail CLI: <https://github.com/pimalaya/himalaya>
- ntfy: <https://docs.ntfy.sh>
- bubblewrap: <https://github.com/containers/bubblewrap>
- RFC 5545, iCalendar: <https://www.rfc-editor.org/rfc/rfc5545>
- Charm Fantasy, the agent SDK: <https://github.com/charmbracelet/fantasy>

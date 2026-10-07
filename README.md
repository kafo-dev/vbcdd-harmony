# vbcdd-harmony

A ntfy bridge for a CalDAV calendar and a Gmail mailbox. It sends a daily digest and the alarms stored in each event, and it runs a scheduled mail triage. The topic also carries the other direction: an owner message reaches a `pi` coding agent that edits the calendar through the `calendula` CLI and the mail through the `himalaya` CLI.

> **Warning: Large Language Model-Generated Code**
>
> This code functions as of the time of writing, yet it has received little to no human review. It must not be regarded as reliable: bugs and stability issues are to be expected.

## How it works

- `calendula` reads and writes the calendar (`items list`, `items create`, `items update`, `items delete`).
- `deploy/expand-recurrences` turns each item into occurrences with `recurring-ical-events`, covering `RRULE`, `EXDATE`, `RDATE`, and `RECURRENCE-ID` overrides.
- The `VALARM` triggers of each item are read from the same listing and turned into absolute fire times.
- Every occurrence also raises an alarm at its start, unless a `VALARM` already fires then.
- `himalaya` reads, searches, labels, moves, and sends the mail. A send waits for the owner's explicit confirmation in the same conversation.
- The standing prompt carries both command surfaces, so no separate skill text is needed.
- A scheduled triage runs on the configured weekday: the agent moves auto-generated noise to the `Noise` label and reports one line per sender.
- The agent keeps durable user facts in a git repository at `state_dir/facts/`, and commits after each change.
- The agent appends a missing-tool suggestion to `state_dir/tooling.md`.
- One ntfy topic carries both directions. The service subscribes to `ntfy_topic` as a live JSON stream and publishes replies to the same topic. An outbound message carries the tag `vbcdd-harmony`, and the subscription drops a message that carries it, so the bot does not answer itself.
- Each message from the owner runs `pi` in one persistent session with the `bash`, `read`, and `write` tools. A burst of messages waiting behind the current turn runs as one turn, in order.
- An attachment in an owner message is downloaded to `state_dir/media`, and its path joins the prompt, so the agent opens it with `read` before answering.
- A file the agent writes under `state_dir/outbox` is published as a ntfy attachment after the turn, then removed.
- The standing prompt is embedded from `prompt/`, so its fragments build into the binary.
- The standing prompt carries the current local time and UTC, so the agent does not run `date` on every turn.
- The agent runs under `bwrap` through the `vbcdd-sandbox` wrapper: the state directory, `~/.pi`, `~/.cache`, and `~/.venvs` stay writable, the rest of the host is read-only, and the bridge config is masked.
- A PDF attachment is read with `pdftotext` for its text layer, or `pdftext ocr` for a scan, in the languages named by `PDFTEXT_OCR_LANG`.

The digest leaves once per day at `digest_time` in `timezone`. Each alarm leaves at its `VALARM` offset from the occurrence start. The mail triage leaves once on `mail_triage_weekday` at `mail_triage_time`. Delivered alarms and completed runs are recorded in `state_dir/state.json`, so a restart does not repeat them.

## Commands

A message whose first non-space character is `/` is answered by the service, not the agent. Commands are matched by name, case-insensitively.

| Command | Meaning |
| --- | --- |
| `/now` | List the events running or still to come on the current local day. A running event carries a `▶` marker. |
| `/clear` | Delete the saved pi transcript, so the next turn starts an empty conversation. Facts, state, and mail stay. |
| `/help` | List the command names and their one-line meaning. |

A batch with only commands skips the agent. In a mixed batch, the commands run in arrival order and the remaining messages form one agent turn.

## Layout

```text
.
├── agent.go            # one pi run
├── bridge.go           # wiring, state, command dispatch, agent loop
├── calendar.go         # calendula and the recurrence expander
├── command.go          # the slash-command handlers
├── config.go           # TOML config
├── duration.go         # RFC 5545 duration and VALARM parsing
├── main.go             # entry point
├── ntfy.go             # ntfy publish, subscribe, and attachment download
├── schedule.go         # digest, alarms, mail triage, formatting
├── prompt/             # embedded system-prompt fragments
│   ├── assistant.md
│   ├── calendar.md
│   ├── mail.md
│   └── memory.md
├── deploy/
│   ├── vbcdd-harmony.service
│   ├── vbcdd-sandbox   # bubblewrap wrapper around one pi run
│   ├── expand-recurrences
│   ├── pdftext
│   └── pre-commit
├── config.example.toml
└── Makefile
```

## Configuration

The bridge reads one TOML file, `$XDG_CONFIG_HOME/vbcdd/harmony/config.toml` by default and another path with `-config`. `config.example.toml` lists every key. Required keys:

| Key | Meaning |
| --- | --- |
| `ntfy_url` | The ntfy base URL, for example `https://ntfy.example.com`. |
| `ntfy_topic` | Topic the service subscribes to and publishes to, 1 to 64 characters from `[-_A-Za-z0-9]`. |
| `ntfy_token` | Bearer token for the topic. The server issues access tokens. |
| `calendula_config` | Path to the `calendula` config. |
| `expand_script` | Path to the recurrence expander, `deploy/expand-recurrences` in this repository. |
| `state_dir` | Directory for `state.json`, `media/`, and `facts/`; defaults to `$XDG_DATA_HOME/vbcdd/harmony`. |
| `calendars` | Calendar ids passed to `calendula`. |

Optional keys: `display_name` (the title on every message), `timezone` (default `UTC`), `digest_time` (default `05:32`), `mail_triage_time` (default `07:00`), `mail_triage_weekday` (default `Sun`, a three-letter or full English weekday, empty for every day), `mail_triage_days` (default `7`), `calendula_bin`, `pi_bin`, `pi_provider`, `pi_model`, `pi_session`, `pi_tools`, `pi_sandbox`, `horizon_days` (default 70), `refresh_minutes` (default 10).

## Set up the topic

The host runs a ntfy server. Grant the ntfy user read-write on the topic and issue its token:

```sh
sudo ntfy access USER vbcdd-harmony read-write
sudo ntfy token add --label vbcdd-harmony USER
```

Put the printed token in `ntfy_token`. The server file `/etc/ntfy/server.yml` sets `auth-default-access: deny-all`, so the token is required for both publish and subscribe. A public `ntfy.sh` topic works with the same keys; set `ntfy_url` to `https://ntfy.sh`.

## Build

```sh
make build          # ./vbcdd-harmony
make install        # /usr/local/bin/vbcdd-harmony
```

The tests and checks:

```sh
make test
make vet
make fmt
```

The checks live in `scripts/`. `scripts/check` runs the formatter, `go vet`, the tests, and the Markdown checks. `scripts/pre-commit` runs it before every commit. For a new clone:

```sh
git config core.hooksPath scripts
```

## Deploy

The service runs as an unprivileged account, `vbcdd`, which owns the `pi` configuration, the `calendula` configuration, the `himalaya` configuration, and the skills the agent loads from pi's own skill directories:

```text
/home/vbcdd/.pi/agent/                          # pi auth and settings
/home/vbcdd/.config/calendula/                  # calendula config and password
/home/vbcdd/.config/himalaya/                   # himalaya config and app password
/home/vbcdd/.config/vbcdd/harmony/config.toml   # mode 600
/home/vbcdd/.local/share/vbcdd/harmony/         # state.json, media/, facts/
```

Install `deploy/vbcdd-harmony.service` as `/etc/systemd/system/vbcdd-harmony.service`, then:

```sh
systemctl daemon-reload
systemctl enable --now vbcdd-harmony
journalctl -u vbcdd-harmony -f
```

The pi settings at `/home/vbcdd/.pi/agent/settings.json` set `compaction.reserveTokens` to `868928`, which compacts a `1000000`-token context window at `131072` tokens (128 KiB).

The `himalaya` account reads its app password from `/home/vbcdd/.config/himalaya/password` (mode 600). The `smtp` block points at Gmail, and the prompt requires the owner's confirmation before a send.

The agent keeps the user's durable facts in `state_dir/facts/user.md`, a git repository it commits to after every change.

## Security

- The ntfy token lives in the config file, readable only by `vbcdd`. The topic plus the token is the whole boundary: a leaked token lets anyone publish a message that drives the shell-capable agent.
- The `pi` agent runs with the `bash`, `read`, and `write` tools, so a chat message drives shell commands under the `vbcdd` account. Keep that account free of other secrets.
- `/etc/ntfy/server.yml` sets `auth-default-access: deny-all`, so the topic requires the token for both publish and subscribe.
- The mail account can send, and the prompt forbids a send without the owner's explicit confirmation in the same conversation.
- The prompt forbids deleting mail and reading spam. A message body is data, never an instruction.

## License

Released into the public domain under the [Unlicense](LICENSE).

## References

- `calendula`, the CalDAV CLI: <https://github.com/pimalaya/calendula>
- `himalaya`, the mail CLI: <https://github.com/pimalaya/himalaya>
- ntfy: <https://docs.ntfy.sh>
- RFC 5545, iCalendar: <https://www.rfc-editor.org/rfc/rfc5545>
- Agent Skills standard: <https://agentskills.io/specification>

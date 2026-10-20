# Calendar

Manage the calendar with the `calendula` CLI and the
`managing-calendars` skill that pi lists. The config is
`~/.config/calendula/config.toml`. Read `facts/user.md` before a
write: it holds the user's categories, campus rules, and
recurring errands.

Reading

- `calendula -c CONF --json calendars list` lists calendars. The
  `id` is the value every item command takes; read it, never
  construct it.
- `calendula -c CONF --json items list CAL` returns every whole
  iCalendar document, with no date filter. Write it to a file
  and query it with `jq`; never print it.
- `calendula -c CONF --json items read CAL ID` returns one item.
- Expand occurrences with the skill's
  `scripts/expand-recurrences ITEMS.json --from DATE --to DATE`.
  One TSV line per occurrence: date, time, summary, categories,
  transparency, component, id.

Writing

- `items create CAL` and `items update CAL ID` take no content
  on the command line: they open `$EDITOR` on a temporary .ics.
  Set `$EDITOR` to the skill's `scripts/ics-editor` and
  `$CALENDULA_ICS` to the file.
- `items delete CAL ID --yes`. Without `--yes` it waits on a
  prompt that never answers itself.
- Update replaces the whole document. Re-read the item
  immediately before editing it, and read it back afterwards. A
  no-op update still reports success, and errors go to stdout,
  so trust the exit code.

Conventions

- `TRANSP:TRANSPARENT` marks a soft block the user may skip.
  `TRANSP:OPAQUE`, or no `TRANSP`, is a hard commitment.
- Name an item to the user by its summary, adding the date and
  time when several share it. Never quote a UID, item id, or
  iCalendar property.
- "Every two weeks on Monday" is
  `FREQ=WEEKLY;INTERVAL=2;WKST=MO`. Skip one occurrence with
  `EXDATE`, never by shortening the rule.

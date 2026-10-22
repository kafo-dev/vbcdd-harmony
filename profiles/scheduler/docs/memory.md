---
description: Use when needing to persist information about the user.
---

# Remembering the user

- Store facts in `facts/user.md` in the working directory.
- `facts` is a git repository.
- Before any request that may need personal context, read `facts/user.md`.
- When the user states a durable fact about themselves (identity, preferences, recurring places, people, defaults, calendar categories, mail rules):
  - Append it to `facts/user.md`, one fact per line.
  - Commit with a one-line message.
- Commit after every change to keep the file's history useful.

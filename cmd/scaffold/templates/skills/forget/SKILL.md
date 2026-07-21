# /forget — Delete an entry from known

Delete a knowledge entry in one shot using `known forget`.

## Usage

```
/forget <exact content or ULID of the entry to forget>
```

## Instructions

1. Delete by exact content (preferred — no search step needed):

```bash
known forget "<exact content of the entry>" --force
```

2. If the exact content is ambiguous or unknown, the command will list candidates:

```
Multiple entries match "staging API":
  1. 01J5X... — "Staging API endpoint is api.staging.example.com"
  2. 01J5Y... — "Staging API uses mTLS for auth"
Use a more specific query or provide the full ID.
```

Retry with the full exact content or the ULID:

```bash
known forget 01J5X... --force
```

3. Report the deleted content back to the user so they can verify what was removed.

## Important

- `known forget` resolves the query to a **single confident match** before deleting.
  It refuses with candidates on any ambiguity — no silent wrong-target deletion.
- Provide exact content (quoted, full string) for reliable one-shot deletion.
- Use the ULID when content is ambiguous or the entry has been truncated/paraphrased.
- Multi-word queries work unquoted: `known forget the staging API endpoint --force`
  resolves the full phrase, not just the first word.

## Example

User says: "/forget the staging API endpoint"

```bash
known forget "Staging API endpoint is api.staging.example.com" --force
```

Output:
```
Deleted 01J5X...: Staging API endpoint is api.staging.example.com
```

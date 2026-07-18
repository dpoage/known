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

2. If the CLI reports ambiguity, it lists candidates automatically:

```
Multiple entries match "staging API":
  1. 01J5X... — "Staging API endpoint is api.staging.example.com"
  2. 01J5Y... — "Staging API uses mTLS for auth"
Use a more specific query or provide the full ID.
```

Retry with the full exact content or the ULID shown:

```bash
known forget 01J5X... --force
```

3. Confirm success — the CLI echoes what was deleted:

```
Deleted 01J5X...: Staging API endpoint is api.staging.example.com
```

## Important

- `known forget` resolves to a **single confident match** before deleting — it refuses
  with candidates on any ambiguity. No silent wrong-target deletion.
- Provide exact content (quoted, full string) for reliable one-shot deletion.
- Use the ULID when content is ambiguous or you only have a paraphrase.
- Multi-word queries work unquoted: `known forget the staging API endpoint --force`

## Example

User says: "/forget the staging API endpoint"

```bash
known forget "Staging API endpoint is api.staging.example.com" --force
```

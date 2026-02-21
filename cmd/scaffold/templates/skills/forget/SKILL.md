# /forget — Delete an entry from known

Find and delete a knowledge entry. This is a two-step process: search first, then delete after confirmation.

## Usage

```
/forget <query describing what to forget>
```

## Instructions

1. Search for matching entries using JSON output to get IDs. Note: `--json` is a global flag and goes **before** the subcommand:

```bash
known --json search '<query>'
```

2. Present the matching entries to the user and ask which one(s) to delete. Show the entry content and ID for each match.

3. After the user confirms, delete the selected entry:

```bash
known delete <id> --force
```

4. Report success back to the user.

## Important

- Always confirm with the user before deleting. Never delete without explicit approval.
- Use `--force` on the delete command to skip the interactive prompt (since we already confirmed with the user).
- If multiple entries match, let the user choose which to delete — don't delete all matches.

## Examples

User says: "/forget the staging API endpoint"

Step 1 — Search:
```bash
known --json search 'staging API endpoint'
```

Step 2 — Show results and ask:
> I found these entries:
> 1. `01J5X...` — "Staging API endpoint is api.staging.example.com"
> 2. `01J5Y...` — "Staging API uses mTLS for auth"
>
> Which would you like to delete?

Step 3 — Delete after confirmation:
```bash
known delete 01J5X... --force
```

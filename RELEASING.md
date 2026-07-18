# Releasing a New Plugin Version

Checklist for shipping a new version of the `known` Claude Code / omp plugin.

## Files to update

- `plugin/.claude-plugin/plugin.json` — `"version"` field
- `.claude-plugin/marketplace.json` — `plugins[0].version` field
- `plugin/skills/known/SKILL.md` — `version:` frontmatter field

## Steps

1. Bump all three version fields above (grep `0\.\d\+\.\d\+` to find them all).
2. Validate plugin frontmatter parses as YAML (no unquoted bracket values):
   ```sh
   python3 -c "
   import yaml, os
   for root, _, files in os.walk('plugin'):
       for f in files:
           if f.endswith('.md'):
               p = os.path.join(root, f)
               c = open(p).read()
               if c.startswith('---'):
                   yaml.safe_load(c[3:c.find('---',3)])
                   print('OK:', p)
   "
   ```
3. Commit and merge to `main`; push to `dpoage/known`.

## Updating installed copies after merge

### Claude Code (omp shares the same plugin cache)

The marketplace is installed as a git checkout from `github:dpoage/known`.
Installed path: `~/.claude/plugins/marketplaces/known-marketplace/`
Plugin cache: `~/.claude/plugins/cache/known-marketplace/known/<version>/`

**To update:**
```
/plugin update known
```
or uninstall and reinstall:
```
/plugin uninstall known
/plugin install known
```
This pulls the latest `main` from GitHub, reads `plugin/.claude-plugin/plugin.json`
for the new version, and creates a fresh cache dir at the new version path.
The old `0.2.0/` directory is left but no longer referenced.

### Verification

After update, start a new session. Check `~/.omp/logs/omp.YYYY-MM-DD.log`:
- Must have **no** `Failed to parse YAML frontmatter` entry for `known-marketplace/known/`
- `/known:` slash commands must be available in the session

Quick log check:
```sh
grep 'known-marketplace' ~/.omp/logs/omp.$(date +%F).log
# Expected: no output (no errors). Any match = still loading old version.
```

Confirm active plugin version:
```sh
cat ~/.claude/plugins/cache/known-marketplace/known/*/plugin.json | python3 -c "import sys,json; [print(d['version']) for d in [json.load(open(f.strip()))] for f in sys.stdin]" 2>/dev/null || \
  ls ~/.claude/plugins/cache/known-marketplace/known/
# Should show 0.2.1 (or current) only
```

## How the install works (for reference)

- `known_marketplaces.json` → `source: github, repo: dpoage/known` → git cloned to `~/.claude/plugins/marketplaces/known-marketplace/`
- On install/update: git fetch + reset to `origin/main`; version read from `plugin/.claude-plugin/plugin.json`
- Plugin files copied to `~/.claude/plugins/cache/known-marketplace/known/<version>/`
- `installed_plugins.json` records `installPath`, `version`, `gitCommitSha`
- Both Claude Code and omp read from the same `~/.claude/plugins/` directory

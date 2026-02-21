---
description: Retrieve knowledge relevant to a query
argument-hint: <query>
---

Retrieve knowledge from the graph, optimized for LLM context.

## Instructions

1. Run the recall command with the user's query:

```bash
known recall '$ARGUMENTS'
```

2. Use the results to inform your work. If the user asked a question, answer it using the recalled knowledge. If you're performing a task, apply the recalled facts (conventions, decisions, environment details) to what you're doing.

3. Briefly tell the user what you found, but don't just dump raw output — synthesize it into your response.

4. If no results are returned, tell the user no matching knowledge was found. Suggest `/known:search` with a lower `--threshold` to broaden the search.

## Scope

The scope is auto-derived from the current working directory. To search a different scope, pass `--scope`:

```bash
known recall '<query>' --scope backend.api
```

## Examples

```bash
known recall 'database connection pooling config'
known recall 'authentication flow' --scope backend
```

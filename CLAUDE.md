# About agent-store

## What it owns

The single source of truth for agent identity and config — identity, runtime configs, tools, limits and memories — **and for prompts**: the host prompt and every repo's own prompt are sections in this store, and the prompt files on disk are renders of them. SQLite at `~/.config/agent-store/agents.db`. It is a Go library (`github.com/kayushkin/agent-store`, package `agentstore`) that llm-bridge-server embeds, and a standalone server (`cmd/server`) on `:8300` over the same database. README "HTTP API" and "The prompt source" are the route tables.

## Where this prompt lives

These sections are stored in agent-store as a project prompt collection and rendered, with identical text, to `AGENTS.md` and `CLAUDE.md` at the root of this repo, so that whichever file a harness reads it gets the same thing. Edit them on dash `/files`, or edit either rendered file: the 15-minute scan carries the edit back into the sections and out to the other file. The host prompt keeps one row for this repo with only what an agent elsewhere needs.

# How it works

## Collections, sections and outputs

A `prompt_collection` is `global` (one, rooted at `$HOME`) or `project` (one per directory with its own prompt, keyed on `root_path`). Its `prompt_sections` carry a `level` (0 preamble, 1 group, 2 section), a `title`, a `body`, `tags`, a `position` and `enabled`; the heading line is composed from level and title (`ComposePromptSectionHeading`) and never stored apart from them. **A section names no harness and no file.** `prompt_section_revisions` is append-only and records every create, update and delete with its source and note, so deleting a section loses nothing. `prompt_collection_outputs` are the files a collection renders to, each with `accounted_sha256` — the hash of the last disk content the sections account for. `RenderPromptCollection` writes every enabled output, and **refuses with 409 when an output has unsettled drift**, so a render never overwrites an edit nobody has looked at.

## The splitter and the canonical render

`SplitPromptMarkdown` (`promptmarkdown.go`) cuts a file at level-1 and level-2 headings and **never inside a fenced code block** — a shell comment starts with `# ` too, and the first splitter cut every code sample in two at them; its rows are kept in `prompt_sections_legacy_per_file`. Level-3 and deeper headings stay inside their section. `RenderPromptMarkdown` is the one way sections become a file: heading, blank line, body, a blank line between sections, one newline at the end. **Splitting a render gives back the same sections**, and that round trip is what lets drift be judged by comparing section lists instead of bytes — keep it true when changing either function (`promptmarkdown_test.go`).

## Drift: an edit made to a rendered file

`DetectPromptDrifts` finds an output whose disk hash is not its accounted hash; `computePromptDriftOperations` splits the file and diffs it against the last accounted content with `DiffPromptMarkdownSections` — longest common subsequence over headings, and a removed section paired with an added one of identical body is a **rename**, which keeps the section's id, tags and history. **No model writes section text**: operations come from the parser. `ReconcilePromptDrifts` applies a drift made only of updates at once and re-renders the collection's other files; a drift that inserts or deletes a section is **held** until a person applies or dismisses it. `ApplyPromptDrift` checks inside its transaction that the resulting sections account for the file (`verifySectionsAccountForFile`). A drift's status is `open`, `held`, `applied`, `dismissed` or `superseded`. The only model call is in llm-bridge-server, and it proposes **tags** for inserted sections (`PromptDriftInsertedSectionLabel` has no title field on purpose).

## Harness deliveries and what a session receives

`prompt_harness_deliveries` holds one row per harness id: `inject` (the bridge puts the prompt in the system prompt) or `native_file` with `native_relative_path` (the harness reads that rendered file itself). **There is no default** — `ResolveContext` returns `ErrPromptHarnessDeliveryUnknown` for a harness with no row, and llm-bridge-server records missing rows at start with `RecordPromptHarnessDeliveryIfAbsent`. Every harness gets the same sections: the global collection, then each project collection whose root is the session's working directory or an ancestor; a `native_file` harness is given only the collections its file does not already carry.

## Tracked files and ignore rules

The scan walks `$HOME` for prompt and agent files and keeps every version of each. `tracked_file_ignore_rules` **stamp, never delete**: kind `path_pattern` matches a path, and kind `git_worktree` detects a linked worktree by its `.git` being a *file* — worktree folders on this host follow no naming pattern, so a name rule cannot find them. `ListTrackedFilesIncludingIgnored` shows the stamped ones. `ImportUntrackedPromptFiles` turns a prompt file no collection renders into a project collection.

# Access and operations

## Two ways in, one database

llm-bridge-server embeds this library and serves its routes on `:8160`, where they are operator-only (service token or an administrator). `agent-store.service` runs `~/bin/agent-store` on `AGENT_STORE_ADDR=:8300` over the same `agents.db` and re-scans `$HOME` every 15 minutes; scheduler job 12 (`*/15`) posts `:8160/files/scan`, which scans and then reconciles drift. **Use `:8160`**: only the embedded copy has `HandlerHooks` wired, so only it notifies connected runners of a rendered file and asks for tags on a held drift. ⚠️ `:8300` listens on every interface with **no auth** and serves the same write routes, including `PUT /prompt-sections/{id}` — noteboard todo `115b5b0d-080a-4940-9151-41e66d523d53`. The bridge links this repo through `replace github.com/kayushkin/agent-store => ../agent-store`, so **a change here reaches `:8160` only when llm-bridge-server is rebuilt and deployed** — which restarts every bridge session — and the deploy gate checks this clone is on a clean, pushed `main` when it does.

# Working in this repo

## Build, test and deploy

Plain `go build ./cmd/server` and `go test ./...`; no build tag. `schema.sql` is embedded and applied at open, and `store.go` carries the migrations — `setAsidePerFilePromptSections` is the one that moved the first splitter's rows aside. `./deploy.sh` deploys only the standalone `:8300` server. The prompt routes live in `promptsource_server.go`; a write body is `promptSectionWrite`, where **`level` is required** because 0 is a real level and an absent one could not be told from it. Pushes to `github.com/kayushkin/agent-store`.

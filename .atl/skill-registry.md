# Skill Registry

**Delegator use only.** Any agent that launches sub-agents reads this registry to resolve compact rules, then injects them directly into sub-agent prompts. Sub-agents do NOT read this registry or individual SKILL.md files.

See `_shared/skill-resolver.md` for the full resolution protocol.

## User Skills

| Trigger | Skill | Path |
|---------|-------|------|
| Create Gentle AI pull requests with issue-first checks. Trigger: creating, opening, or preparing PRs for review. | branch-pr | C:\Users\Lenovo\.gemini\skills\branch-pr\SKILL.md |
| Trigger: PRs over 400 lines, stacked PRs, review slices. Split oversized changes into chained PRs that protect review focus. | chained-pr | C:\Users\Lenovo\.gemini\skills\chained-pr\SKILL.md |
| Design docs that reduce cognitive load. Trigger: writing guides, READMEs, RFCs, onboarding, architecture, or review-facing docs. | cognitive-doc-design | C:\Users\Lenovo\.gemini\skills\cognitive-doc-design\SKILL.md |
| Write warm, direct collaboration comments. Trigger: PR feedback, issue replies, reviews, Slack messages, or GitHub comments. | comment-writer | C:\Users\Lenovo\.gemini\skills\comment-writer\SKILL.md |
| Trigger: Go tests, go test coverage, Bubbletea teatest, golden files. Apply focused Go testing patterns. | go-testing | C:\Users\Lenovo\.gemini\skills\go-testing\SKILL.md |
| Create Gentle AI issues with issue-first checks. Trigger: creating GitHub issues, bug reports, or feature requests. | issue-creation | C:\Users\Lenovo\.gemini\skills\issue-creation\SKILL.md |
| Trigger: judgment day, dual review, adversarial review, juzgar. Run blind dual review, fix confirmed issues, then re-judge. | judgment-day | C:\Users\Lenovo\.gemini\skills\judgment-day\SKILL.md |
| Trigger: new skills, agent instructions, documenting AI usage patterns. Create LLM-first skills with valid frontmatter. | skill-creator | C:\Users\Lenovo\.gemini\skills\skill-creator\SKILL.md |
| Trigger: improve skills, audit skills, refactor skills, skill quality. Audit and upgrade existing LLM-first skills. | skill-improver | C:\Users\Lenovo\.gemini\skills\skill-improver\SKILL.md |
| Plan commits as reviewable work units. Trigger: implementation, commit splitting, chained PRs, or keeping tests and docs with code. | work-unit-commits | C:\Users\Lenovo\.gemini\skills\work-unit-commits\SKILL.md |

## Compact Rules

Pre-digested rules per skill. Delegators copy matching blocks into sub-agent prompts as `## Project Standards (auto-resolved)`.

### branch-pr
- Every PR MUST link an approved issue (`Closes #N`, `Fixes #N`, `Resolves #N`) having the `status:approved` label.
- Branch names MUST match: `^(feat|fix|chore|docs|style|refactor|perf|test|build|ci|revert)/[a-z0-9._-]+$`.
- Add exactly one `type:*` label matching the commit type (e.g. `feat` -> `type:feature`, `fix` -> `type:bug`).
- Commit messages MUST match Conventional Commits format: `type(scope): description` or `type: description`.
- Run shellcheck on modified shell scripts before pushing.
- Do not add "Co-Authored-By" or AI attribution to commit messages.

### chained-pr
- Split PRs over 400 changed lines unless a maintainer explicitly accepts `size:exception`.
- Keep each PR reviewable in about ≤60 minutes with one deliverable work unit per PR.
- Keep tests and docs with the unit they verify.
- In Feature Branch Chain, use a draft/no-merge tracker PR; child PR #1 targets the tracker branch, later child PRs target the immediate parent branch.
- Every child PR must include a dependency diagram marking the current PR with `📍`.
- Treat polluted diffs as base bugs: retarget or rebase until only the current work unit appears.

### cognitive-doc-design
- Lead with the answer/decision/outcome, and put context after.
- Use progressive disclosure: happy path first, then details, edge cases, and references.
- Group related information into small sections (chunking) and keep flat lists short.
- Prefer tables, checklists, examples, and templates over prose that must be remembered.
- Design docs so reviewers can verify intent without reconstructing the whole story.

### comment-writer
- Start comments with the actionable point; keep feedback concise (1-3 short paragraphs or tight bullet list).
- Sound like a warm, direct, and thoughtful teammate, not a corporate bot.
- Give the technical reason when asking for a change.
- Match the thread language. Use neutral/professional Spanish by default unless regional tone is explicitly called for.
- Do not use em dashes; use commas, periods, or parentheses instead.

### go-testing
- Prefer table-driven tests for multiple cases; use `t.Run(tt.name, ...)`.
- Test behavior and state transitions, not implementation trivia. Assert outputs, errors, state, and side effects explicitly.
- Use `t.TempDir()` for filesystem tests; never rely on a real home directory.
- Keep integration tests skippable with `testing.Short()` when they run external commands or slow flows.
- For Bubbletea, test `Model.Update()` directly for state changes; use `teatest` only for interactive flows.
- Golden files must be deterministic; update only through `-update` and check diffs before committing.

### issue-creation
- Blank issues are disabled; must use bug report or feature request template.
- Every issue is auto-labeled `status:needs-review` on creation.
- A maintainer must add `status:approved` before any PR can be opened.
- Questions go to GitHub Discussions, not issues.

### judgment-day
- Resolve project skills from registry and inject matching skill instructions into both judge and fix prompts.
- Launch two blind judges in parallel with identical target and criteria. Accept verdict only when both complete.
- Ask before fixing Round 1 issues. Re-judge in parallel after fix agent runs.
- Terminal states are only `JUDGMENT: APPROVED` or `JUDGMENT: ESCALATED`. Escalate after 2 fix iterations.

### skill-creator
- Follow `docs/skill-style-guide.md` if available.
- Description must be one quoted physical line (<=250 chars) and contain essential trigger words.
- A skill is a runtime instruction contract, not human documentation. Keep body concise (<=700 tokens).
- Place templates, schemas, and fixtures in `assets/`; put conceptual detail in `references/`.

### skill-improver
- Treat `docs/skill-style-guide.md` as normative. Default to audit-only.
- Use `.atl/skill-registry.md` to select skill paths.
- Improve frontmatter, triggers, formatting, and convert tutorials into imperative instructions.
- Never delete meaningful content silently.

### work-unit-commits
- Commit by deliverable behavior/fix unit, not by file types (e.g. do not split model from services).
- Keep tests and docs inside the same commit as the code changes they verify/explain.
- Map each task/work unit to a single candidate PR/commit with clear start/finish states and rollback safety.
- If tasks forecast >400 lines, split into chained PRs prior to implementation.

## Project Conventions

| File | Path | Notes |
|------|------|-------|

Read the convention files listed above for project-specific patterns and rules. All referenced paths have been extracted — no need to read index files to discover more.

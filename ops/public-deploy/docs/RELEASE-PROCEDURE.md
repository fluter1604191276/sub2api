# Release Procedure

This is the repeatable procedure for publishing a Sub2API production
candidate. It is intentionally separate from the production switch checklist:
the candidate must be complete and verifiable before any production write is
considered.

## 1. Reconcile source state

For every round of二开, start from the live production image that is actually
serving production. Do not infer the
baseline from a readable version tag, a remembered branch, or the newest local
worktree. Query the live node first:

```bash
ssh fluterapi-prod 'cat /etc/fluterapi-node-role'
ssh fluterapi-prod 'docker inspect sub2api --format "{{.Config.Image}}|{{.Image}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{index .Config.Labels "org.opencontainers.image.source-snapshot"}}"'
```

The role must be exactly `production`. The image revision label is the required
starting commit for the next development line. Create a new worktree from that
commit, then run:

```bash
ops/public-deploy/check-production-baseline.sh
```

That check requires the current HEAD to be the production commit or a
descendant of it. It blocks unrelated historical lines before any build can
start.

For the current incident-recovered line, the production-derived worktree is:

```text
/Users/fluter_claw/Documents/study_project/sub2api/.worktrees/public-from-production-20260830
```

The older `public-deploy` worktree remains preserved for investigation and
uncommitted model-plaza work. It is not a release input. After an upstream
upgrade or every production switch, repeat the live query and create a new
production-derived line with `create-production-derived-worktree.sh`; do not
keep extending an old version-named worktree.

See `ops/public-deploy/docs/RELEASE-LINES.md` for the active candidate and
historical-line registry.

Record the current branch, HEAD, upstream base, and `git status --short`
before touching files. A mixed staged/unstaged tree is an audit condition, not
a reason to reset it. Review every changed path and keep unrelated user work.

The candidate source must be a clean commit. Local reports, databases,
credentials, browser state, screenshots, backups, generated caches, and
temporary release evidence are not candidate inputs.

## 2. Reconcile extensions

For every site-specific behavior:

- keep one capability row in `PRODUCTION-EXTENSIONS.md`;
- keep one dated record in `docs/extensions/`;
- list source files, routes/jobs, data dependency, billing/scheduling/protocol
  impact, tests, image smoke evidence, and rollback.

The extension record and the source diff must agree. A feature that exists only
in a session note, an old image tag, a local patch file, or a production
database is not part of the candidate.

## 3. Verify before commit

Run the smallest relevant tests, then the complete release floor:

```bash
go test ./...                         # from backend/
pnpm typecheck                        # from frontend/
pnpm test:run                         # from frontend/
pnpm build                            # from frontend/
python3 -m unittest discover -s ops/public-deploy -p 'test_*.py'
bash -n ops/public-deploy/*.sh
python3 -m py_compile ops/public-deploy/*.py
git diff --check
```

The Responses tool matrix is a protocol boundary. Test native Responses and
each bridge separately; do not claim native terminal support when a bridge
filters or rewrites that tool.

## 4. Commit and push

Stage reviewed paths explicitly. Never use broad staging or commit shortcuts.
Use Lore-protocol commits with a reason, constraint, rejected alternative,
confidence, scope risk, directive, tested evidence, and known gaps. Push only
the named feature branch; do not push or force-push `main`.

Write the body with real newline characters (for example, via a temporary
message file or multiple `-m` paragraphs), not escaped `\\n` text. Verify the
stored message with `git show -s --format='%B' HEAD` before pushing so the
Lore fields remain separate and machine-readable.

After pushing, verify the remote commit and require a clean worktree. Do not
build until this point.

## 5. Build and prove the candidate

Build from the clean commit:

```bash
ops/public-deploy/build-production-image.sh fluter/sub2api:<candidate>
```

The image must be `linux/amd64` and carry labels for the Git commit and full
source snapshot. Generate a secret-free manifest containing the exact image
digest, previous production digest, every capability result, test results, and
image smoke results. `verify-release-bundle.sh --image ...` must inspect the
compiled `/app/sub2api` binary and check runtime markers for scheduler,
recovery probe, quality/cache telemetry, pricing, model sync, error handling,
and Responses tools. Source-only evidence or `/health` is insufficient. Run
the verifier with the manifest and image.

Do not switch production in this procedure. A production switch is a separate
explicit operation requiring the role-marker check, backup, rollback pair, and
post-switch capability smoke tests from `RELEASE-CHECKLIST.md`.

# Release Procedure

This is the repeatable procedure for publishing a Sub2API production
candidate. It is intentionally separate from the production switch checklist:
the candidate must be complete and verifiable before any production write is
considered.

## 1. Reconcile source state

Work only in:

```text
/Users/fluter_claw/Desktop/study_project/sub2api/.worktrees/public-deploy
```

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
image smoke results. Run `verify-release-bundle.sh` with the manifest and image.

Do not switch production in this procedure. A production switch is a separate
explicit operation requiring the role-marker check, backup, rollback pair, and
post-switch capability smoke tests from `RELEASE-CHECKLIST.md`.

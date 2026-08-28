# Production Switch Incident Review: 2026-08-28

## Executive Summary

The incident was a release-integrity failure, not a simple container startup failure. The new image was built from a dirty worktree containing many uncommitted customizations, but the release had no immutable source commit, extension inventory, capability manifest, or feature-level acceptance evidence. The container became healthy, so the switch was treated as successful even though the image's custom feature set and protocol compatibility had not been proven.

The production service was rolled back to:

~~~text
fluter/sub2api:fluter-0.1.171-cache-hit-rate-20260826-r3
~~~

Rollback evidence:

~~~text
/www/sub2api/backups/rollback-before-cache-hit-rate-20260828T130318Z
/www/sub2api/backups/compose-before-rollback-20260828T130821Z.yml
~~~

The rollback changed the sub2api container only. PostgreSQL data was not modified.

## User Impact

- Existing custom administration features, including smart scheduling, scheduled probes, quality scoring, and related telemetry, were not guaranteed to exist in the switched image. Users discovered missing functionality after the switch.
- Codex clients depending on terminal-related Responses tools could lose tool capability when their request crossed a Chat Completions compatibility bridge. A healthy HTTP process did not detect this.
- The release process had no reliable way to distinguish a complete production image from an image that merely started successfully.

## Evidence And Confidence

### Confirmed

1. The public deployment worktree is dirty and contains a large set of tracked and untracked changes across scheduling, probes, account/group quality, cache-hit statistics, pricing, model synchronization, and operations.
2. The incident image was tagged with a descriptive label but was not bound to a real Git commit, a source snapshot hash, or a capability manifest.
3. The old and incident image digests differed:

   ~~~text
   old:     sha256:ee5b428161ce0eb4f99f2ac26ffcfd7d9da3acc0ad8a6fbca2de50980dbc6c2a
   incident: sha256:92ca0eead94bdddae1163ee6ba0b3c6329de4804e95718e6c15079e476db1715
   ~~~

4. The health check proves process and port health only. It does not prove admin routes, scheduler behavior, probe startup, billing, or client protocol behavior.
5. In backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go, responsesToolsToChatTools keeps only function tools. local_shell, custom, and other Responses-native tools are skipped by design because Chat Completions has no native equivalent.
6. In the Responses-to-Anthropic request converter, custom is normalized to an ordinary Anthropic tool and other types fall through to a generic tool shape. That is not equivalent to native terminal execution semantics.

### Not Yet Proven

- Which exact feature files were absent from the incident image. Image filesystem comparison was not available during this review, so the missing-feature conclusion is based on the untracked/dirty build input and the observed production behavior, not on an extracted binary diff.
- Whether the reported terminal failure used the Chat Completions bridge, the Anthropic bridge, or a different upstream route. The source exposes a confirmed compatibility risk; request/response fixtures from the affected request are still needed to close the final path.
- Whether the incident image itself introduced the tool filtering behavior. The behavior must be compared against the image that was known-good on 2026-08-20 before attributing it to this build.

## Root Causes

### 1. No immutable custom-code baseline

The release was assembled from a dirty worktree. A directory containing a change does not prove that the Docker build context, generated frontend bundle, or final image contains that change. There was no required relationship between:

~~~text
production image -> image digest -> release manifest -> source snapshot -> tests -> custom feature inventory
~~~

### 2. Liveness was mistaken for capability

/health, Docker health, and healthy Postgres/Redis only show that the application can start and connect to dependencies. They do not show that a particular route is registered, a timer is running, a frontend control is bundled, a billing field is present, or a protocol item survives conversion.

### 3. Protocol conversion had an undocumented capability boundary

local_shell and custom are Responses-native tool concepts. A Chat Completions upstream cannot receive them as native Responses tools. The current bridge intentionally filters non-function tools to avoid malformed tool-call histories. That protects some upstreams from invalid requests, but it also means terminal capability can disappear unless the route explicitly supports or rejects it. Silent filtering is an operationally unsafe contract for a client feature.

### 4. No release stop condition

There was no mandatory answer to: Which二开 are in this image, how was each one tested, and what is the rollback artifact? The release therefore optimized for a successful container restart instead of a verifiable production capability set.

## Corrective Actions

### Completed

- Rolled back to the known production image.
- Preserved the incident image and pre-rollback Compose evidence.
- Added a production extension inventory at ops/public-deploy/docs/PRODUCTION-EXTENSIONS.md.
- Added a release baseline and switch checklist.
- Added a release manifest generator that records source, capability, image, and test evidence without recording secrets.
- Added a release bundle verifier that blocks incomplete or dirty bundles by default.
- Documented the local_shell/custom compatibility boundary and made it a release-gate item.

### Required Before The Next Switch

1. Build from an immutable commit or an explicitly acknowledged source snapshot.
2. Generate a manifest with the exact image digest and linux/amd64 architecture.
3. Pass the custom capability matrix, backend tests, frontend typecheck/build, and git diff --check.
4. Run an image-level smoke test for core admin routes, scheduler/probe fields, cache-hit fields, and protocol fixtures.
5. Back up Compose/env/database and verify the production role marker before any write.
6. Switch only with docker compose up -d --no-deps sub2api; never use down for a normal application image change.
7. Keep the previous image and manifest until post-release observation is complete.

## Long-Term Rule

Production is releasable only when the image is reproducible, the custom capability set is enumerated, every required capability has evidence, and rollback is one command away. Container healthy is necessary but never sufficient.

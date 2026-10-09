# Inflight estimate retained-memory measurement

Capability IDs: official-0214-compatibility, ops-baseline.

The b1c226 full unit run reported 8,665,272 bytes of whole-process heap growth,
exceeding the existing 8 MiB bound after 20,000 random unpriced model estimates.
The no-direct-DB and snapshot-read-count assertions passed. That raw failure is
preserved; its exact allocation source is unknown and is not classified as flaky.

The measurement is isolated in the current test binary's child process, keeping
the same workload, pricing assertions, database/snapshot checks and 8 MiB bound.
The measured gateway, snapshot and repository remain live through the final heap
sample. A separate, deliberately retained per-model payload must fail that same
bound as a negative control. This avoids measuring unrelated surviving test work
without reducing the memory contract or claiming to detect every possible leak.

Static inspection of this fixture's estimate/mapping path found no storage keyed
by its random model names; this does not explain the original process-wide
allocation. No production estimation, pricing, cache or billing code changes.
Actual test outcomes, child exit states and heap samples are recorded separately
in .release-evidence/20261009-contract-memory/. Earlier b1c/de40 evidence is not
rewritten. New source/image identities still require explicit binding; no release
gate, exemption, production configuration or automation is changed.

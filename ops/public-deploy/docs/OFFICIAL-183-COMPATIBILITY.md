# Official v0.1.183 Compatibility Review

## Decision

The candidate is based on official tag `v0.1.183` at commit
`e8cb019fabf8b55199436229044cbf9aa7a82564`. Official behavior remains the
default where the same boundary is involved. Site-specific behavior is kept as
an additive extension, with explicit tests for the areas that share code.

## Overlap Review

| Official area in v0.1.171..v0.1.183 | Site extension | Resolution |
| --- | --- | --- |
| Responses custom tool-call IDs and Lite tool-call handling | Responses/Chat/Anthropic compatibility bridges | Keep official ID and event semantics first; keep bridge conversion only where the target protocol requires it. Unsupported native terminal capability remains explicit. |
| Codex session affinity and sticky-capacity spillover | Smart scheduling, sticky escape, exploration, and quality-aware selection | Keep official session binding and capacity behavior; custom selection runs inside the legal candidate set and may escape only under the documented weak-sticky rules. |
| OAuth 429 quota scheduling, Kimi recoverable concurrency 403, and Antigravity token limits | Scheduler transient-failure and account-quality signals | Keep official recoverability/quota boundaries; custom scoring consumes the resulting availability state and does not reclassify protocol limits as healthy. |
| Channel Monitor V2 aggregation fixes | Site monitor quality/probe views and account attribution | Keep official aggregation/null handling; custom UI and probe attribution consume the corrected aggregates. |
| Anthropic cache TTL and official usage rollups | Account quality, cache-hit statistics, probe attribution, and internal cost accounting | Keep official usage fields and TTL intent; custom metrics are derived telemetry and do not alter user billing. |
| Official model catalog and pricing resources | Channel/model calibration, image/video/search/audio/tool cost extensions | Keep official token/long-context/Fast/Flex/Priority pricing; site fields extend operation-specific internal cost and never replace official user-charge semantics. |
| OAuth/image/payment/security fixes | Error sanitization, account model sync, release-integrity controls | Keep official security and lifecycle behavior; custom controls remain at the admin/observability boundary. |

## Known Boundary

`responses-tools` is intentionally partial: a compatibility bridge can translate
supported tool shapes, but it is not proof of native terminal execution. The
release manifest must record this decision and the protocol fixture result.

## Evidence

- Backend full test suite, frontend typecheck/lint/build/full tests, and
  release-bundle tests are required before a candidate is eligible.
- The candidate source, image labels, source snapshot, capability inventory,
  and immutable image digest are one release identity.
- No production switch is part of building or validating this candidate.

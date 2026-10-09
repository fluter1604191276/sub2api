# Token refresh deadline correction

Capability IDs: official-0214-compatibility, ops-baseline.

Base: local candidate de40dc688df032fbc9459cd0e5261f7826c2f785. Its first
complete local integration attempt exposed a late refresh success accepted after
the attempt deadline. The original failure remains in the sealed de40 evidence;
an earlier unit pass does not negate it.

The correction checks both cancellation state and the actual context deadline.
The timer publishing context.Err may lag the deadline. A fallback refresh must
not persist new credentials once the attempt or parent deadline has passed.
Parent cancellation/deadline takes precedence over an internal attempt timeout,
so it does not become account cooldown evidence. The unified refresh path's
already durable exact-state CAS result keeps its existing bounded state cleanup
behavior. This does not introduce provider calls, fee policy or migration changes.

The original late-success regression remains unchanged. Deterministic tests cover
expired deadlines whose Err is still nil, exact deadline boundaries and parent
cancellation precedence. New source/image identities and required check outcomes
belong to .release-evidence/20261009-contract-deadline/; de40 records are not
relabelled as executions of the successor.

No new-SHA push, PR, production operation or automation modification is authorized
by this correction. allow_release remains false pending the complete gates and
scope decisions. Previous de40/7ae source and image evidence is preserved.

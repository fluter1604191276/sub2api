# Native HTTP server lifecycle (isolated candidate)

When `server.h2c.enabled` is false (the unchanged default), the application-owned
`ManagedHTTPServer` delegates to its one native `http.Server`. When enabled, one
bound listener dispatches HTTP/1 and HTTP/2 to two native servers sharing the same
router/body limit while retaining separate configured idle timers. Native HTTP2Config
retains concurrent-stream, frame and connection/stream receive-window settings.
Zero H2 idle retains the previous H1 idle/ReadTimeout fallback.

The classifier mirrors native prior-knowledge detection: first 13 bytes, then at
most 24 only when the prefix matches. Each accepted socket keeps its original
header deadline; initial positive deadlines are clamped until native parsing
clears them. Subsequent body and keepalive deadlines pass through. Scheduling
retains one task per accepted connection without an added admission cap. Temporary
accept errors back off from 5 milliseconds to one second, as native serving does.
The classifier handles connection ownership only, never request/account/financial
logic. It replays every consumed byte and preserves public TCP half-close methods.

All application start/stop methods use the manager rather than a promoted embedded
method. Pending sockets are removed atomically when the native listener accepts
them. Shutdown stops admission/pending classification and concurrently waits for
both native servers under the caller's single context; it does not force-close
active or hijacked connections on timeout. Explicit Close retains separate force
close behavior for native-owned connections. Hijacked connections stay outside
native shutdown ownership.

The TLS surface preserves the measured old entrypoint behavior: ServeTLS adjusts
ALPN according to configured Protocols (H1 in the supplied H2C configuration),
whereas directly supplying a TLS listener configured with the original h2 ALPN
allows H2. Public TLS handshake/ALPN dispatch retains the actual tls.Conn and
Request.TLS. The normal application's plaintext ListenAndServe exposure is unchanged.

This manager supports this application's startup configuration. It does not promise
arbitrary mutable BaseContext/ConnContext/ConnState/RegisterOnShutdown callback
compatibility, and does not use private standard-library registration hooks,
reflection, linkname, lint exemptions or alternative HTTP2 legacy implementations.

No balance, price, billing attempt, idempotency or Policy B migration behavior is
changed. A new frozen source SHA requires the full acceptance matrix and fresh image
binding; prior candidate checks are not inherited. This document is not deployment
approval.

Official Wire regeneration exposed an existing provider/source drift: the supplied
provider would replace the running five-dependency group constructor with a
configuration-only alternative. `ProvideGroupHandlerWithSiteServices` calls the
original constructor with exactly those five dependencies and no config argument,
so regeneration preserves the existing runtime graph rather than dropping the
scheduler/probe services. No handler business implementation was changed.

Unobserved startup callbacks, external listener adapters or different integration
entrypoints require separate compatibility review before use. Full new-SHA checks,
source/binary vulnerability scans, fresh image smoke/E2E and hosted CI remain
required; native amd64 load, real upstream and historical finance gates are not
waived by the focused loopback tests.

TLS listener ownership follows the exact Go 1.27.2 entrypoint: ServeTLS certificate
setup errors leave a caller-supplied listener open; after successful setup serving
owns and closes it. ListenAndServeTLS owns its newly created listener and closes it
on setup failure. Explicit nil/empty protocol selections use the native TLS defaults
without mutating the exposed configuration.

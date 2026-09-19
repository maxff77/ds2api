## Problem Statement

Operators running a ds2api instance lose their DeepSeek accounts. After a period of normal operation — days, sometimes weeks — DeepSeek suspends an account for 1-7 days, and sometimes permanently. The operator finds out only when requests start failing, because nothing in ds2api distinguishes "this account was suspended" from "this token expired" from "the upstream hiccuped".

There is no way to answer the three questions that matter:

1. **Which account got suspended, and when?** Token invalidation is handled silently. The account's token is cleared and the pool rotates onward. No record survives.
2. **How hard was that account being worked?** The pool caps concurrent in-flight requests per account, but nothing caps requests *over time*. One account can absorb hundreds of requests in an hour and nothing notices.
3. **Why did DeepSeek flag it?** No hypothesis can be tested, because no evidence is collected.

Meanwhile a suspended account stays in rotation. Every request routed to it burns a slot, fails, and rotates — so a single suspension degrades throughput for every consumer of the instance until a human notices and intervenes.

Separately, and independently of volume, ds2api's outbound requests carry two internal contradictions that make the client trivially fingerprintable as non-genuine:

- The TLS ClientHello impersonates Safari, while the HTTP `User-Agent` claims to be the DeepSeek Android app. No real client produces that combination.
- The ClientHello's ALPN extension is rewritten to advertise only HTTP/1.1, while genuine Safari advertises HTTP/2. So the fingerprint is not even a faithful Safari — it is a mutant that exists nowhere in the wild.

## Solution

Three capabilities, shipped in order of cost and confidence, each independently revertable.

**The operator can see what the pool is doing.** Every account hand-off and every token invalidation emits a structured log event carrying the account identity. The operator counts requests per account per hour and lists suspension events straight from container logs — no new storage, no new endpoint, no dashboard required to get the first answer.

**The operator can cap how hard any one account is worked.** A per-account hourly request budget, configurable at runtime and disabled by default. When an account is at budget it stops being handed out; the pool rotates to another, and existing backpressure queues callers when every account is saturated. Setting the budget to zero restores today's behaviour exactly.

**Suspended accounts remove themselves from rotation and return on their own.** When ds2api judges a token invalid, the account enters quarantine with an expiry. The pool skips quarantined accounts. When the window lapses the account is tried again naturally — if it was a temporary suspension it resumes working; if it is suspended again the next window doubles. The operator sees every quarantined account in the admin panel, with the reason, the strike count, and when it returns, and can release one manually.

**The outbound client stops contradicting itself.** The TLS fingerprint, the negotiated protocol, and the HTTP headers all describe the same client: desktop Chrome. HTTP/2 is genuinely negotiated rather than advertised-then-abandoned.

Consumers of the OpenAI-compatible API see none of this except as improved reliability: fewer requests routed to dead accounts, fewer accounts dying.

## User Stories

1. As an operator, I want every account hand-off recorded with the account identity, so that I can count how many requests each account served in any window.
2. As an operator, I want every token invalidation recorded with the account identity, so that I can tell which account was suspended and exactly when.
3. As an operator, I want those records in the instance's normal log stream, so that I can read them with the tooling I already use, without deploying storage or a new endpoint.
4. As an operator, I want the records to be machine-parseable, so that I can aggregate per-account request rates without writing a parser.
5. As an operator, I want to collect a week of these records before changing anything else, so that I am fixing the cause rather than guessing at it.
6. As an operator, I want to know the peak requests-per-hour any single account reached, so that I can decide whether request volume is plausibly what triggers suspension.
7. As an operator, I want to cap how many requests a single account serves per hour, so that no account looks like automated traffic to DeepSeek.
8. As an operator, I want that cap to be configurable at runtime, so that I can tune it without rebuilding or redeploying.
9. As an operator, I want that cap disabled by default, so that upgrading does not silently change the behaviour of a working instance.
10. As an operator, I want setting the cap to zero to fully restore the previous behaviour, so that I have an instant rollback that needs no deployment.
11. As an operator, I want the cap applied per account rather than across the pool, so that adding accounts increases total capacity.
12. As an operator, I want an account at its budget to be skipped rather than to fail, so that the pool rotates to a usable account instead of returning an error.
13. As an operator, I want callers to queue when every account is at budget, so that a burst is delayed rather than dropped.
14. As an operator, I want an account judged suspended to leave rotation immediately, so that subsequent requests are not wasted on it.
15. As an operator, I want a quarantined account to re-enter rotation automatically once its window lapses, so that a temporary suspension needs no intervention from me.
16. As an operator, I want a repeatedly-suspended account to get a longer window each time, so that a permanently-banned account stops consuming retry attempts.
17. As an operator, I want that backoff capped, so that the window cannot grow to an absurd length.
18. As an operator, I want quarantine to need no background worker, so that there is no extra goroutine, no timer, and no probe traffic to DeepSeek.
19. As an operator, I want to see every quarantined account in the admin panel, so that I do not have to read logs to know the pool's health.
20. As an operator, I want each quarantined entry to show the reason it was quarantined, so that I can tell a suspension from an expired token.
21. As an operator, I want each entry to show its strike count, so that I can tell a one-off from an account DeepSeek keeps rejecting.
22. As an operator, I want each entry to show when the account returns to rotation, so that I know whether to wait or to act.
23. As an operator, I want to release a quarantined account manually, so that I can put it back immediately after replacing its token.
24. As an operator, I want releasing an account to also clear its strike count, so that a genuinely fixed account is not punished by its history.
25. As an operator, I want an empty quarantine list to render as a clear empty state, so that I can tell "nothing is wrong" from "the panel failed to load".
26. As an operator, I want the quarantine panel in my own language, so that it matches the rest of the admin interface.
27. As an operator, I want suspension detection to recognise DeepSeek's Chinese-language rejection messages, so that suspensions are not missed because the upstream replied in Chinese.
28. As an operator, I want suspension detection to recognise rate-limit phrasing, so that throttling is handled as quarantine rather than retried into a harder ban.
29. As an operator, I want the outbound TLS fingerprint to match the advertised User-Agent, so that the client does not identify itself as automation on the handshake alone.
30. As an operator, I want the client to genuinely negotiate HTTP/2 when it advertises it, so that the connection does not contradict its own ClientHello.
31. As an operator, I want the HTTP client hints to be consistent with the claimed browser, so that a Chrome handshake is not paired with headers no Chrome would send.
32. As an operator, I want mobile-app-specific headers dropped once the client claims to be a browser, so that no residual field contradicts the new identity.
33. As an operator, I want the non-streaming fallback path left on its existing transport, so that I have a working escape hatch if HTTP/2 misbehaves against the upstream.
34. As an operator, I want each capability shipped and observed separately, so that when suspensions stop I know which change did it.
35. As an operator, I want each capability revertable on its own, so that one bad change does not force me to roll back the rest.
36. As an operator, I want a documented verification routine per capability, so that I can confirm a deploy is healthy without inventing checks under pressure.
37. As an operator, I want per-account proxy assignment to keep working unchanged, so that this work does not disturb an instance that already routes accounts through separate egress.
38. As an API consumer, I want requests routed away from suspended accounts, so that I do not receive failures caused by an account the instance already knows is dead.
39. As an API consumer, I want streaming responses to keep working after the transport change, so that the protocol migration is invisible to me.
40. As an API consumer, I want a burst that exceeds the pool's budget to be queued, so that I get a slower answer rather than an error.
41. As a maintainer, I want suspension detection to live at one place in the code, so that a new suspension phrasing is added once rather than at every call site.
42. As a maintainer, I want the budget and quarantine gates to hang off the pool's existing acquisition check, so that no acquisition path can bypass them.
43. As a maintainer, I want an automated test proving HTTP/2 is negotiated, so that the fingerprint regression cannot silently return.
44. As a maintainer, I want an automated test proving the User-Agent and client hints are browser-consistent, so that a future edit cannot reintroduce the contradiction.

## Implementation Decisions

### Verified corrections that constrain this work

Earlier analysis of this problem produced three claims that are false against the current source. They are recorded here so the implementation does not re-adopt them:

- **There is no "web endpoint" to migrate to.** The upstream endpoints this project already calls *are* DeepSeek's web chat API. The mobile client and the web client share them.
- **Proof-of-work cannot be dropped.** The completion, continuation, and file-upload paths all attach a proof-of-work response header, and the endpoint requiring it is the same one the web client uses. Removing it breaks completions outright. The proof-of-work implementation is out of scope and must not be touched.
- **Authentication is a bearer token, not a cookie.** Whether DeepSeek's web client carries its token as a cookie or a bearer header is unverified. No auth-mechanism change is in scope until that is captured from a real browser session.

### Sequencing

Four phases, shipped one at a time, each observed in production for at least 72 hours before the next. Phase 1 additionally gates on a week of collected data. The sequence is deliberately ordered by cost-and-confidence, not by apparent impact: the suspensions are *gradual*, which fits accumulated behaviour, while a static fingerprint mismatch would fail on the first handshake. The fingerprint work is real but is not obviously the cause, so it ships last, after measurement has had a chance to identify the actual driver.

### Phase 1 — Instrumentation

The account pool module emits a structured event whenever it hands out an account, carrying the account identity and current in-flight count. The auth resolver emits a structured event whenever it marks a token invalid, carrying the account identity and a reason.

Both use the standard library's structured logger at the existing default handler — no new logging dependency, no new sink, no new configuration. The operator aggregates from the container's log stream.

Events are named so they can be grepped as distinct streams: one name for acquisitions, one for token invalidations.

### Phase 2 — Per-account hourly budget

A new rate-limiter component tracks request timestamps per account over a rolling one-hour window.

**The gate is the pool's existing per-account acquisition check.** Both acquisition paths — targeted acquisition and round-robin rotation — already funnel through it, so gating there covers every path without touching rotation logic. This is the single most important structural decision in this phase: no new interception point is introduced.

The budget is carried in the existing runtime configuration section alongside the current in-flight and queue limits, so it inherits the existing admin settings write path and runtime-apply mechanism. One new runtime value: requests per hour.

**No artificial inter-request delay.** An earlier draft specified a random 30s-3min pause between requests. It is dropped. The budget already produces the spacing — ten accounts at twenty requests per hour each yields roughly one request every eighteen seconds without injecting anything — while a delay in the request path would add that latency to every completion and make the API unusable for interactive consumers. The pacing is a property of the budget, not of a sleep.

A budget of zero or less disables the limiter entirely, and is the default. This is the rollback mechanism — a config write, no deployment.

The rolling-window implementation keeps a per-account timestamp slice, pruned on read. This is adequate for pools of tens of accounts at tens of requests per hour and is marked as such; it is not the right structure at thousands of accounts.

### Phase 3 — Quarantine

A new quarantine component holds, per account, the reason it was quarantined, when, until when, and a strike count.

**No background worker.** An earlier design proposed a periodic prober goroutine that would re-test quarantined accounts on a timer. That is dropped. Each quarantine record carries an expiry; the pool's acquisition check treats an expired record as inactive, so the account is retried naturally by ordinary traffic. This removes a goroutine, a ticker, and all probe traffic to the upstream.

The backoff window doubles per strike from a 24-hour base, capped at 16× the base. Recorded here because the schedule is the decision:

| Strike | Window |
|---|---|
| 1 | 24h |
| 2 | 48h |
| 3 | 96h |
| 4 | 192h |
| 5+ | 384h (cap) |

Releasing an account deletes its record entirely, which clears the strike count — a deliberate choice, so that an operator who has replaced a token is not penalised by the old token's history.

**The gate is the same acquisition check as Phase 2**, checked before the budget check.

Quarantine is triggered from the existing point where the auth resolver marks a token invalid. That site already fires on exactly this condition and already holds the account identity, so no new detection path is created. The resolver gains a reference to the pool for this call; every construction site of the resolver must be updated.

Suspension detection — the existing predicate that decides a token is invalid — is broadened from its current keyword set to also match suspension and throttling phrasing, including Chinese-language equivalents for "banned", "restricted", and "anomalous". The predicate stays in one place; the keyword list becomes a single collection iterated over, rather than a chain of boolean ORs, so future additions are one-line.

### Phase 3 — Admin surface

Two endpoints on the existing admin router, inside the existing admin-authentication group: one listing quarantined accounts, one releasing a named account. They follow the established pattern of a per-concern handler package registered from the admin handler's route registration, taking the shared pool-controller dependency.

The shared pool-controller interface gains two methods: list quarantine records, and release an account.

The admin web UI gains a navigation entry and a panel that lists quarantined accounts with reason, strike count, and return time, plus a per-row release action. It follows the existing panel conventions: the shared authenticated-fetch helper, the shared message callback for success and error, and the existing internationalisation helper. New translation keys must be added to every locale file, not just the default.

### Phase 4 — Transport and identity consistency

Two changes that must land together; either alone leaves a contradiction.

**Genuine HTTP/2.** The transport currently supplies a custom TLS dialer to the standard HTTP transport. Setting that transport's HTTP/2 flag does not work in this configuration: the standard library type-asserts the dialed connection to the standard TLS connection type in order to hand it to the HTTP/2 round-tripper, and a uTLS connection is not that type. The fix is to build the HTTP/2 round-tripper directly from the extended networking library, which accepts a custom TLS dialer by design. The required dialer signature takes a context, network, address, and TLS config, and returns a connection — verified against the pinned library.

The ALPN-rewriting helper that forces HTTP/1.1 is deleted. Its existence is what makes the current fingerprint a mutant.

The non-streaming fallback client stays on the plain standard transport, unchanged, as the escape hatch.

**Matching identity.** The ClientHello profile moves from Safari to Chrome. The User-Agent becomes a desktop Chrome string. Browser client hints and fetch-metadata headers are added. The mobile-app-specific platform, version, and locale headers are removed, along with the Android API level from the client constant model.

**The Chrome major version must agree in three places**: the uTLS hello profile's target, the `Chrome/<major>` in the User-Agent, and the version claimed in the client-hint header. In the currently-cached library version the auto Chrome profile targets Chrome 131. The pinned version in the manifest was never downloaded locally, so the implementer must resolve dependencies and re-check the profile's actual target before fixing the other two.

### Explicitly unchanged

Endpoints, proof-of-work, the bearer-token auth mechanism, the per-account proxy resolution path, session creation and deletion, and the streaming forwarding layer.

## Testing Decisions

### What makes a good test here

Assert on externally observable behaviour: whether the pool hands out an account, what the emitted log event says, what headers leave the process, which protocol was negotiated, what the admin endpoint returns. Do not assert on internal counters, private fields, or the shape of intermediate structures. The unexported suspension predicate in particular should be exercised through the behaviour it drives — an account leaving rotation — not called directly.

Every test names the behaviour it protects, so that a failure reads as a regression statement rather than a mechanical mismatch.

### Seams

Four seams. Three already exist and must be reused rather than duplicated.

**Seam A — the account pool's public acquisition API.** Covers Phases 1, 2, and 3 in their entirety. All three behaviours are observable through whether acquisition returns an account, because all three gate at the same acquisition check. Prior art: the existing pool test file, which builds a pool by seeding configuration through environment variables and drives the public acquire methods. Two helpers already exist there — one seeding a single account, one seeding two for rotation — and both must be reused. Phase 1's log assertions are made at this seam too, by swapping the default structured-logger handler for a buffer and asserting on the emitted record; the log line is the deliverable, so it is external behaviour here, not an implementation detail.

**Seam B — the outbound request, intercepted at the round-tripper.** Covers Phase 4's header consistency. Prior art: the existing continue-path and upload-path client tests, which define a round-tripper function type, install it on the client, and assert on the outbound request's headers. This seam sits above the transport and therefore cannot observe TLS or protocol negotiation — that is what Seam C is for.

**Seam C — a real TLS test server, driven through the transport client. NEW.** Covers Phase 4's HTTP/2 negotiation. This is the only new seam and it is unavoidable: ALPN negotiation is invisible at every higher layer, so no existing seam can observe it. The test stands up an HTTP/2-enabled TLS test server, issues a request through the transport client, and asserts the response's protocol is HTTP/2. Because the test server presents a self-signed certificate that the production path must continue to reject, this requires a clearly-named test-only constructor that accepts the test certificate; the production constructor's trust behaviour is not relaxed.

**Seam D — the admin HTTP handlers.** Covers the quarantine endpoints. Prior art: the existing admin handler tests, which construct requests directly against handlers and assert on the recorded response.

### Coverage per phase

- Phase 1: acquisition emits an event naming the account; token invalidation emits an event naming the account.
- Phase 2: an account at budget is not handed out; a second account still is, proving the budget is per-account and not global; a budget of zero disables the limiter regardless of recorded volume.
- Phase 3: a quarantined account is not handed out; the first strike sets the base window; a second strike doubles it; release removes the record and clears strikes; an expired record does not block acquisition.
- Phase 4: the transport negotiates HTTP/2; the User-Agent is a Chrome desktop string; the User-Agent does not mention Android; the required client hints are present; the mobile platform header is gone.
- Admin: the list endpoint returns quarantined records; the release endpoint releases the named account; a release with no account identifier is rejected.

### Regression risk to watch

The streaming layer is the main risk in Phase 4. HTTP/2 frames differently from chunked HTTP/1.1, and the streaming runtime flushes per frame. The full suite must pass, with particular attention to the server-sent-events consumer and the chat streaming runtime.

## Out of Scope

- **Any change to proof-of-work.** It is mandatory on the completion path and must be left exactly as it is.
- **Any change to the authentication mechanism.** Whether to move from bearer token to cookie depends on a capture from a real browser session that has not been performed. A separate spec, gated on that capture.
- **Any change to upstream endpoints.** They are already the web API.
- **Automated login or account creation.** Tokens are obtained by a human from a real browser session and pasted in. This spec does not change that and does not add programmatic login.
- **Egress IP strategy.** See Further Notes — this is a real and probably significant risk, but it is a deployment decision rather than a code change, and no code in this spec depends on how it is resolved.
- **Per-account health scoring.** Deferred until the pool is large enough for a score to mean anything.
- **Alerting.** No notification channel is added. Phase 1's log events are the substrate a future alerting spec would build on.
- **Persisting quarantine across restarts.** Quarantine is in-memory. A restart clears it, and a still-suspended account is re-quarantined on its next failure. Persistence is deferred until restarts prove frequent enough to matter.

## Further Notes

**All accounts currently share one egress IP.** Whatever the per-account budget is, the upstream can correlate every account in the pool as a single actor by source address. This is not addressed by any phase here. If all four phases land and suspensions continue, IP correlation is the leading remaining explanation, and the only fix is more egress addresses. The per-account proxy assignment path already exists and is the mechanism for that, should it become necessary.

**The host's existing Cloudflare WARP container belongs to a different service** and is not in this project's request path today; the instance's outbound address is currently the host's own address. Routing this project through that container would place both services behind one shared egress, coupling their fates rather than isolating them. That is a deliberate decision to make, not a default to fall into.

**The dependency manifest pins library versions that were never resolved locally.** The verified facts in this spec about the uTLS hello profiles and the HTTP/2 dialer signature come from the versions actually present in the local module cache, which are older than the pinned ones. Resolve dependencies and re-verify before implementing Phase 4.

**The decision gate after Phase 1 is real and should be honoured.** If a week of data shows no account exceeding roughly twenty requests per hour, then volume is not the driver, Phase 2 will not help, and the fingerprint work in Phase 4 should be promoted ahead of Phases 2 and 3.

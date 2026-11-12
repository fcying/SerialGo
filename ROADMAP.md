# SerialGo Roadmap

English | [简体中文](ROADMAP.zh-CN.md)

[User guide](README.md) | [Architecture](ARCHITECTURE.md)

This document records deferred work and its acceptance gates, not implemented features or a release schedule. Recheck the recorded dependency evaluations before starting a trial.

## Deferred Serial Library Migration

**Decision: worth an isolated trial, but only after regression testing of the other application features is complete. No dependency or serial-backend migration has been applied.** The current backend remains `github.com/albenik/go-serial/v2 v2.6.1`; automatic flow control is not currently exposed by this application.

### Candidates and maintenance

- The current library's latest tagged version is still [`v2.6.1`, with source commit dated 2024-01-28](https://proxy.golang.org/github.com/albenik/go-serial/v2/@latest). Its [last default-branch commit](https://github.com/albenik/go-serial/commit/594ef15cc4760409acc4074b486375a6de28bf54) is also from that date. The repository is not archived, but this is a maintenance risk, not evidence of recent active maintenance.
- The original upstream, [`go.bug.st/serial v1.8.0`](https://github.com/bugst/go-serial/releases/tag/v1.8.0), was released on 2026-07-15. It has more recent maintenance, but its stable API does not include flow control; [RTS/CTS PR #201](https://github.com/bugst/go-serial/pull/201) remains unmerged.
- The trial candidate is [`github.com/briardhq/go-serial v1.8.0-briard.1`](https://github.com/briardhq/go-serial/tree/v1.8.0-briard.1), commit `7fa2b2eddb3a0968a2eea9467c39e33e098f526b`, dated 2026-09-30. The implementation is on its `briard` branch, not `master`. It is based on upstream v1.8.0 plus the RTS/CTS patch and a Windows RTS-handshake fix. Its [README](https://github.com/briardhq/go-serial/blob/v1.8.0-briard.1/README.md) explicitly calls it a temporary fork until upstream releases the feature; long-term maintenance is not established. If upstream gains equivalent stable support before migration, prefer upstream instead.

### Findings and migration blockers

- The candidate declares Go 1.25.0 and only `golang.org/x/sys` as a module dependency. The current Go 1.27.4 build toolchain already satisfies that toolchain floor; this does not remove the I/O behavior checks below.
- Source inspection confirms `Mode.RTSCTSFlowControl` enables Linux `CRTSCTS` and Windows CTS output gating plus driver-managed RTS handshake. It does **not** add XON/XOFF software flow control or DTR/DSR flow control. This is source-level evaluation, not hardware verification.
- `SetMode` does not apply changes to the flow-control field. Flow-control changes must reopen the port unless this is addressed and tested separately.
- Our current open path calls manual `SetRTS` after opening. The candidate's Windows `SetRTS` replaces the RTS handshake bits, so a migration must avoid that call while hardware flow control owns RTS, including later DTR-only updates. Manual DTR and non-flow-controlled RTS behavior must remain available.
- The candidate uses a `Port` interface and `Mode` configuration instead of our current concrete port and option functions. It has `SetReadTimeout`, but no public write-timeout API. Preserve the application's 10ms receive-idle polling and 250ms write deadline; do not silently replace a bounded write with an indefinitely pending write.
- Its Unix implementation still switches the descriptor to blocking mode after opening. The existing nonblocking I/O and borrowed-descriptor compatibility work cannot simply be deleted or reused unchanged. Retain the existing Windows port enumeration and device descriptions; a library change must not replace them with intrusive USB probing.

### Historical receive-compatibility gate

The UART migration history supplied by the user is an additional blocker, not evidence that switching back is now safe. Commit `8873737` (2024-08-29) changed the active UART implementation to albenik and explicitly reported that the first-byte-timeout strategy could "break rx data", while `SetReadTimeout` plus `ReadyToRead` worked better and faster. The later mutex change was separate from that original reason. The old `go.bug.st/serial v1.6.2` dependency was actually replaced by [`github.com/fcying/go-serial` at `312149864b96`](https://github.com/fcying/go-serial/tree/312149864b9635cb5702daf7966eeca833c8755a), so comparisons must use that fork rather than assume unmodified upstream behavior.

Source comparison of that old fork and `v1.8.0-briard.1` does **not** establish a fix for the historical receive problem:

- Both Windows implementations still use the first-byte-return configuration for positive read timeouts (`ReadIntervalTimeout` and `ReadTotalTimeoutMultiplier` both `MAXDWORD`). The newer CH340 workaround changes the infinite/very-large timeout constant to `0x7FFFFFFE`; it does not demonstrate a fix for the old positive 1ms timeout path.
- The old fork reports an empty timed-out read as a `Timeout` error, while the candidate returns `(0, nil)` for a configured read timeout. The adapter must preserve the intended caller behavior rather than treat this semantic change as proof that lost data was fixed.
- The current UART wrapper polls `ReadyToRead` with immediate reads and handles waiting in its own deadline loop. The current SerialGo backend instead uses a 10ms first-byte timeout on Windows, and `Session.receive` continuously copies every returned byte slice. These are different consumers; one `Read` completion is not a protocol-frame boundary. Display grouping and raw byte preservation must be evaluated separately.
- Albenik also provides `SetFirstByteReadTimeout`, but the UART implementation deliberately avoids it. A migration must preserve the polling workaround or demonstrate equivalent reliable receive behavior; a newer library version alone is not the acceptance criterion.
- The reviewed briard `Port` interface has no public `ReadyToRead` method, so the UART workaround cannot simply be copied into that backend. Do not assume its `SetReadTimeout(0)` is equivalent to albenik's immediate-read mode. An equivalent waiting/reading path needs separate evaluation. The current SerialGo strategy differs from UART's, but the historical record alone does not establish that SerialGo loses data.
- The UART wrapper's current `ReadLength` also reads into a larger buffer and clamps the copied count to the remaining requested length without retaining excess bytes. If multiple frames are available, this caller-side truncation must be isolated from any library defect. No UART code is changed by this evaluation.

Before considering a switch, first reproduce the historical receive workload on the same adapter/driver where possible. Compare the working UART polling strategy against the pinned candidate with identical data, baud rate, timeout, and receive workload: single-byte arrivals, header followed by a delayed tail, back-to-back frames, bursts, sustained traffic, and timeout followed by new data. Check raw byte order, sequence numbers/checksums, and total bytes for loss, duplication, or truncation; do not compare only `Read` sizes or displayed line breaks. Resolve any exact-length over-read behavior in the test consumer so it cannot bias the comparison. Without this evidence, the historical problem remains unverified and the candidate remains deferred even if RTS/CTS works.

### Deferred trial and acceptance gate

1. Complete the base application features and their regression testing first. Then reassess upstream/fork maintenance, available releases, flow-control fixes, receive APIs, and the historical timeout problem before deciding whether to trial a migration. This section records the current findings, not approval to switch automatically. If a trial is still justified, perform it as a separate backend change and pin the newly reviewed tag/commit rather than a floating branch.
2. Keep the `communication/serialport` wrapper contract, executable-local configuration, saved framing and control-line values, presets, and UI behavior unchanged. Adapt opening, timeouts, errors, and handle ownership inside the backend; do not treat an import-path replacement as a complete migration.
3. Before adoption, verify real Windows serial transmission/reception, custom baud rates, file sending, packet-gap display, manual RTS/DTR, blocked CTS and resume, driver-managed RTS, write timeout, stop/close responsiveness, disconnect/reconnect, and repeated open/close. Use hardware or a virtual setup that actually supports handshake signals; UI launch alone is insufficient.
4. Follow the existing Linux policy: build only by default. Do not claim Linux I/O or hardware-flow-control validation without exercising it. The trial is not considered accepted until the existing timeout/cancellation guarantees and Windows serial behavior have been demonstrated.


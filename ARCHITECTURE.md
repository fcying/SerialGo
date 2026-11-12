# SerialGo Architecture

English | [简体中文](ARCHITECTURE.zh-CN.md)

[User guide](README.md) | [Roadmap](ROADMAP.md)

## Application boundary

`main.go` creates `internal.App` and passes it to `ui.Run(app, title)`. `internal/app.go` is the framework-independent business entry point for serial connections, sending, capture/export, counters and operation status. Its settings actions and persistence helpers own baud rates, presets, dirty revisions and shutdown flushing. This layer does not import MyGo.

`ui/` owns controls, interaction drafts, confirmations, native file dialogs and the receive-display model. It calls App commands and implements `internal.View`: `Dispatch(func())` posts results through `Window.Update`, and `AppendEvents([]internal.Event)` updates the display. App commands and observable state belong to the frontend thread; serial enumeration/open/close, transmission, file I/O and settings writes run in background workers. A replacement UI supplies this adapter without reimplementing those operations. Control-line confirmation preserves its synchronous driver-error result.

## Modules

| Location | Responsibility |
| --- | --- |
| `main.go` | Application name, version, window title, and startup wiring |
| `internal/app.go` | Connections, sending, capture/export, counters, and operation status |
| `internal/settings_actions.go`, `internal/settings_save.go` | Settings rules, dirty revisions, snapshots, persistence, and exit flushing |
| `internal/session.go`, `internal/format.go` | Serial sessions, receive events, and text/HEX encoding and decoding |
| `ui/` | Native MyGo controls, interaction drafts, confirmations, dialogs, and receive-display state |
| `communication/serialport/` | Device enumeration, serial configuration, driver control, and I/O |
| `config/` | Settings schema, normalization, executable-local paths, and persistence format |
| `assets/`, `cmd/appicon/` | Embedded defaults and icons, and build-time icon generation |

Business code must not import MyGo or another UI framework. Dependencies are pinned in `go.mod`; the native UI uses [MyGo v0.9.0](https://github.com/egoist/mygo/releases/tag/v0.9.0), without WebView, Node.js, or TypeScript bindings.

## Settings and persistence

- Resolve executable symlinks and use only the `config.toml` beside the actual executable. Never search the working directory or a user configuration directory, and never fall back to another directory after a save failure.
- The repository-root [`config.toml`](config.toml) is the sole default template. Just copies it to the generated `assets/default-config.toml` before compilation; Go decodes that embedded resource. The executable is standalone and does not read the repository template at runtime.
- Create defaults immediately if the executable-local file is missing. Loading must not overwrite an existing file, including one that cannot be read or decoded. Initialize omitted scalar fields from the embedded defaults; an omitted or explicitly empty preset list stays empty.
- Committed edits mark `settingsDirty`, without per-keystroke writes or save timers. Property-dialog drafts stay separate until confirmation.
- Save dirty settings on actual use: successful port open, accepted send, successful log export or capture start, confirmed port-property change, preset addition or confirmed deletion, and immediately applied display, language, or preset-panel changes. Skip unchanged uses and exits, and do not save per packet during timed or looping sends.
- Serialize background writes using immutable snapshots with independently copied preset and baud-rate slices. Clear dirty state only for a successfully saved current revision; an older save cannot clear a newer edit.
- Preserve dirty state and save errors after failure so the next use can retry. Later operation-success notifications must not hide a save error. Normal shutdown drains work and flushes remaining drafts through `App.Shutdown`.
- Configure MyGo's locale as session-only. The executable-local `config.toml` remains the sole preferences store; language switching does not translate or reset user data.

## Receive display and native assets

- The UI owns retained display history separately from raw receive-to-file capture. Display limits count raw event payload bytes, including outgoing history, rather than HEX expansion or timestamps. Eviction drops whole oldest events with a 16,384-byte headroom target and always retains the newest event, even if oversized.
- Windows controls use Microsoft YaHei UI. The receive log uses its configured family, point size, and color; send and preset data use the system monospace font with fallback for Chinese characters.
- The lower workspace defaults to 90 logical pixels: three 24-pixel rows, two 4-pixel gaps, vertical padding, and borders. The connection card follows the content width of its Baud row: it shows port selection first, then a visible Baud label, a selector sized to fit `1000000`, and Config on the same row. The Port selector and Config share a right edge; the last row places the port-status light immediately before Open/Close. File actions use two equal columns: Save data aligns with Open file, Record data aligns with Send file, and the recording button switches to Stop recording while capture is active. Raw captures retain incoming bytes in `.dat` files; capture state is runtime-only and resets on restart. File delay and Repeat interval fields are both 48 logical pixels wide. File delay uses centered digits and shares the last row with Stop, which has a localized file-send tooltip. The send header holds HEX, Repeat, a centered five-digit-width interval field with equal horizontal padding, and right-aligned Presets without wrapping. The editor spans the two rows below it, with Send and Clear send stacked on the right. The divider can still expand the workspace.
- HEX and Repeat use up to 8 additional logical pixels from the send header's elastic space, without changing the connection card width. Only that extra gap shrinks when a narrow viewport runs out of room; the five-digit interval field and other controls keep their widths. Text and HEX editors share the same empty-input prompt. The footer orders S, R, Reset, serial settings, and status, separated by vertical bars; the operation-status cell exposes its full localized message on hover.
- Receive-option rows share a locale-sized first column so Timestamp and Auto scroll remain vertically aligned without clipping either language.
- Maintain only `assets/icon.svg`. The SVG's serial-port outlines and curve control points are mirrored about its vertical centerline, with a solid light circular background. `cmd/appicon/` generates PNG, multi-size ICO, and Windows `.syso` resources; do not manually edit or commit generated icons.
- Windows loads the embedded ICO for the current DPI; Linux supplies the embedded PNG to GTK. Native title-bar icon display remains the window manager/compositor's decision. Keep native window decorations rather than adding a custom title bar to force an icon.

## Build and verification workflow

Use Go 1.27.4 or later, Just, and Bash. Build recipes live directly in `justfile`, without standalone build scripts or a C cross-compiler. Both `just windows` and `just linux` embed the default configuration and generate icon resources first; `just build` invokes the Windows build. These explicit build commands do not launch the application.

| Command | Output |
| --- | --- |
| `just build` / `just windows` | `build/windows-amd64/SerialGo.exe` |
| `just linux` | `build/linux-amd64/SerialGo` |

Prepare generated resources with `just _icon` before running focused Go tests directly from a clean working tree. Choose tests for the changed area: encoding and business behavior in `internal/`, receive-display and layout behavior in `ui/`. Linux builds only by default; it does not run tests.

`just test` is a Windows build/deploy/launch recipe, not a Go unit-test runner. The current plain `just` entry invokes that recipe as well. It depends directly on `windows`. If `/mnt/d/project/SerialGo` exists, replace only its `SerialGo.exe`; otherwise skip deployment without creating the directory. Preserve the destination configuration.

If copying fails, convert the destination with `wslpath`, invoke `/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe` to terminate the process at that exact executable path, and retry copying once. Do not rebuild or change configuration during the retry. After a successful copy, enter the deployment directory and launch with `setsid`; do not launch after a failed copy.

For Windows desktop verification, connect directly to `cth-win10`, build with `just windows`, and replace only `Desktop/SerialGo/SerialGo.exe`. Preserve its existing configuration, launch in the logged-in desktop session, and use run-specific scheduled tasks if needed. Clean up only the verification processes, tasks, scripts, and temporary files created for that run; retain the application and configuration. Without a physical or virtual COM port, report only the UI, settings, and error paths actually exercised, not successful serial I/O.

# SerialGo

English | [简体中文](README.zh-CN.md)

A native serial-port debugging assistant for Windows and Linux, with text/HEX transmission, receive logging, file transfer, and editable send presets.

[Architecture](ARCHITECTURE.md)

## Screenshots

<img width="800" height="420" alt="SerialGo English interface" src="https://github.com/user-attachments/assets/adc31ff4-fd61-4181-bf9c-856801c997b0" />

## Getting started

1. Run `SerialGo.exe` on Windows or `SerialGo` in a Linux graphical session. Use a writable executable directory so the application can create and save its adjacent `config.toml`.
2. Select a port and baud rate, then click **Open**. The initial baud rate is `115200`; use **Settings** beside the baud selector if you need to change framing or control lines.
3. Enter text or HEX bytes in the send area and click **Send**. Choose the text encoding in **Options** when needed.
4. View incoming data in the upper pane. Click **Save data** to export displayed text, or **Record data** to start saving raw received bytes while connected.

The lower cards contain connection controls, receive controls, file operations, and the send editor, from left to right. The main window restores its normal position, size, and maximized state from the executable-adjacent `[Window]` table in `config.toml`. The saved rectangle must overlap a display's work area by at least 100x50 logical pixels; otherwise the window is centered at its saved size. Windows stores native-pixel bounds to avoid mixed-DPI drift; legacy settings without pixel bounds are centered when connected displays use different scales. A fresh configuration starts centered at 800x600. The divider position is not saved.
The minimum window width is 800 logical pixels so the send controls stay on one row.

## Connecting and baud rates

- Ports are listed at startup and refreshed whenever the Port dropdown opens, by mouse or keyboard. Close and reopen it to retry; there is no separate Refresh button. Enumeration failure preserves the previous list and selection.
- Linux discovery reads device-node metadata only; it never opens a UART or changes its baud rate, framing, or flow control. A listed node may still be unavailable; errors are reported when you explicitly open it.
- Recognized Linux board-UART families include `ttyTHS`, `ttyPS`, `ttySAC`, `ttySTM`, `ttymxc`, and `ttyLP`, with numeric suffixes, alongside the existing USB, ACM, standard UART, and RFCOMM names. Console and pseudo-terminal names are not listed as serial ports.
- Windows shows device descriptions when available, such as `COM12 USB-SERIAL CH340`; Linux shows device paths. Hover over a truncated port name to see its full value, including while connected. Opening and saving use the actual device name, not its description.
- Refresh preserves an available selected port, otherwise selects the first port. An empty list clears the selection and disables Open. Port and baud selectors are locked while connected.
- **Settings** opens data bits, parity, stop bits, RTS, and DTR properties. Settings are global, not per device; the footer shows the current framing. While connected, only RTS/DTR can change; driver errors stay in the dialog without applying changes.
- **OK** applies dialog changes; Cancel, Escape, or closing the dialog discards drafts. Unchanged confirmation does not write configuration. Framing is edited only in Settings, not on the main panel.
- The **⋯** button to the left of Open/Close opens the Tools menu. **Serial plot** extracts numeric values from received log lines and plots them in a separate window. See [Serial plots](#serial-plots).

Both baud selectors initially offer `9600`, `19200`, `57600`, `115200`, `921600`, and `1000000`:

- **Custom...** adds and selects a rate, deduplicated and sorted numerically. Input accepts integers from `1` to `4294967295`; actual support depends on the device and driver.
- Right-click a numeric rate, choose Delete, and confirm. Cancel or Escape changes nothing. Deleting the selected rate selects the first remaining rate.
- Main-panel additions and deletions save immediately. Inside Settings, they remain drafts until the outer OK; outer Cancel or Escape discards them.
- An empty list stays empty after restart and disables Open until a rate is added. You may also edit `BaudRates` in `config.toml` while the application is closed.

## Serial plots

Open **⋯ > Serial plot**. **Configuration** opens a separate dialog for the profile name, optional time regex, time scale, and **Samples per curve**. Each profile can retain 1 to 1,000,000 points per curve. Set `0` for unlimited retention; memory use then grows as samples arrive. New profiles and older profiles without a saved limit default to 10,000 points. **OK** validates, applies, and automatically saves changes beside the executable in `config.toml`; Cancel, Escape, or closing the dialog discards unconfirmed edits. **Start** begins a fresh acquisition. Up to 16 profiles are supported, without a hard limit on curves per profile. Memory use and parsing/rendering work grow with the channel count.

The sample-limit field keeps ASCII digits only and accepts at most eight digits. A paste that would exceed eight digits is rejected without changing the field. Empty or out-of-range values show an error and disable **OK** until corrected.

- The left sidebar contains only the scrollable curve list. It initially uses 260 logical pixels and remembers a manually resized width in `CurveSidebarWidth` in `config.toml`. Drag the divider or use its arrow keys to resize the sidebar. It stops at 170 logical pixels so the controls stay visible. Use **Hide** to collapse it completely. Shrinking the window limits the visible sidebar to leave at least 160 logical pixels for the plot, without overwriting the remembered width; enlarging the window restores the available width. The narrow plot hides the time-range caption but keeps the dropdown and history slider. Opening Configuration does not change the sidebar width. Each curve shows its enabled state, name, extraction rule, unit, and color marker. Prefix rules show the pattern directly without a `Prefix:` label. **+ Add** and each row's pencil icon open a separate editor; the trash icon requests confirmed deletion. Icons retain localized tooltips and accessible names. **OK** validates and immediately applies/saves the change; Cancel, Escape, or closing the editor discards its unconfirmed edits. Enabling/disabling a curve and confirmed deletion also apply automatically. Invalid input shows an error in its dialog without replacing the last valid active or saved rules. Schema changes reset plot history and partial records while preserving whether acquisition is running or stopped; renaming a profile retains its history. Unchanged edits do not write settings. Save failures are reported and retain dirty settings for the normal exit flush.
- **Hide / Show** toggles the curve-list sidebar without clearing its remembered width. Showing it again restores that width, limited only by the available window space. Resizing the sidebar does not write to disk. Closing the plot saves the new width with other pending settings. Restarting restores the saved width with the sidebar visible; visibility itself is session-only. Older configurations without a width, or with nonpositive or non-finite numeric widths, use 260.
- Curve rows display the configured name without an added `CH1`, `CH2`, or similar index prefix. Saved names and curve order are unchanged.
- Each curve's editor includes a color picker and HEX input. `Color` is saved per curve in `CurveProfiles[].Channels[]` in `config.toml`, accepting `#RRGGBB` or `#RRGGBBAA` with opacity. The sidebar marker, plot, hover values, and extraction results use that color. Disabling or deleting another curve does not shift its color; changing only colors preserves existing samples and previews. Older configurations without colors receive defaults in configured channel order, without rewriting the file on load.
- **Prefix** takes the signed decimal or scientific-notation number immediately after a literal prefix, allowing intervening spaces. For `[ADC] VBAT=3712 mV`, use prefix `VBAT=`, scale `0.001`, offset `0`, and unit `V` to plot `3.712 V`.
- **Regex (Go/RE2)** uses Go's standard-library [`regexp`](https://pkg.go.dev/regexp), with RE2-style syntax, not PCRE/PCRE2. It supports neither lookaround nor backreferences such as `\1`, and excludes RE2's `\C`. The implementation version follows the Go toolchain, not a separately versioned RE2 library; the editor shows the build's Go version. The first capture group supplies the number, for example `battery:\s*([-+]?\d+(?:\.\d+)?)`. Each enabled curve extracts at most one value per line; several curves may match the same line. The conversion is `value × scale + offset`. The optional time regex uses the same syntax.
- The X axis defaults to the computer's receive wall-clock time in the system time zone, displayed as hours, minutes, and seconds, with milliseconds for short ranges and hover values. Split lines use the completing read's time; several lines in one batch may share a timestamp. An optional time regex in **Configuration** overrides this with capture group 1 times the time scale, displayed as device seconds rather than calendar time. Hover over the time-regex input for a short two-case hint below it; the hint disappears when the pointer leaves, even if the input keeps keyboard focus. The time scale is disabled while the regex is empty, retaining its value; entering a regex makes it editable again. Device uptime is a numeric time axis, not a UNIX timestamp: it is not converted to a date or offset by the computer's clock. For `uptime_ms=12345 VBAT=3712`, `uptime_ms=(\d+)` with scale `0.001` gives `12.345 s`. Clearing the regex restores system time without discarding the multiplier. Switching clock modes clears previous history and partial records; backwards timestamps and reconnections begin separate line segments.
- When device time resets or moves backwards, a fixed-span live view follows the latest received sample rather than the old maximum timestamp. The history slider and **All** still provide access to retained history.
- Device-time tick precision follows the visible range, so nearby ticks remain distinct even at long uptimes, such as a 60-second window around 100,000 seconds. Ordinary ranges use decimal seconds; extreme magnitudes or very small ranges may use scientific notation.
- Hover labels show the sample's device time at its stored precision, even when neighboring samples fall between the same two ticks.
- **Test rules** opens an on-demand log test window. Paste logs and click **Preview extraction** to test current configuration inputs without saving or changing acquisition. Results appear in the test window and chart; invalid inputs report errors. System-time preview simulates one second per record, ending at current computer time. Device-time preview uses pasted timestamps instead. The final pasted line need not end with a newline.
- Switching profiles rebuilds a visible preview using the newly selected rules and time mode.
- The default test log alternates three voltage records (`VBAT=3712 mV`, `VBAT=3680 mV`, `VBAT=3740 mV`) and three current records (`IBAT=1024 ua`, `IBAT=926 ua`, `IBAT=1524 ua`). Use prefixes `VBAT=` and `IBAT=` for the two curves. With scale `0.001`, use units `V` and `mA` respectively. Existing profile rules are not changed by the sample.
- **Pause view** freezes the displayed snapshot while acquisition continues. Color-only edits update its colors without replacing frozen samples or counters. Select a time range and move the history slider to inspect retained data; **Follow live** returns to the latest snapshot. Hover over the chart to inspect nearby samples. Enabled curves share one automatically scaled Y axis, so use compatible units.
- The plot defaults to 1040 × 600 logical pixels and can be resized down to 720 × 260. All toolbar controls stay on a single row at the minimum width. Closing it saves its size and position separately from the main window in `config.toml`; both are restored when you reopen it, even after an app restart. There is no channel legend above the chart, so additional channels do not consume chart height. Manage channels with the enable checkboxes in the left curve list; changing the enabled channels resets plot history.
- **Stop** retains the current history; **Start** resets it. **Clear** resets only plot data and counters, preserving whether acquisition is running. Closing the plot stops acquisition and discards samples, counters, previews, and unconfirmed edits. Reopening shows an empty, stopped plot with the saved rules.
- Deleting the active profile clears its samples, counters, and unfinished record even if the next profile has identical rules. Acquisition keeps its running or stopped state when the replacement is valid; deleting the last profile or selecting an invalid retained profile stops it and shows any rule error. Stored channels remain visible and editable without truncation or saving on open.
- Sparse lines are clipped at the time-window edges instead of disappearing when both original samples lie outside the window. Y autoscaling includes visible line intersections; hover values still identify real samples, not interpolated data.
- Plot processing cannot hold up serial reading or raw recording. If it falls behind, the plot reports dropped input chunks and bytes; missing input breaks the affected traces rather than drawing across the gap. Unrelated logs update statistics without replacing the plotted samples.

Plotting accepts RX logs terminated by LF or CRLF, independently of receive HEX/text formatting, display-history eviction, and raw recording. Serial-read boundaries may split lines. Unmatched lines add no samples; invalid numbers are counted and omitted. Lines longer than 4096 bytes are discarded until the next LF. Lowering the sample limit drops only the oldest live points; increasing it preserves the points still held. Neither change restarts acquisition or replaces a frozen live snapshot. History and acquisition state are not persisted. Binary packets and CR-only logs are not decoded by this tool.

## Sending data

- Text supports UTF-8 and GB18030. **HEX** in the send editor accepts contiguous bytes or spaces/commas between bytes, for example `01030000` or `01 03 00 00`.
- **Parse text escapes** under **Options > Sending** is off by default. When enabled, text mode interprets `\\`, `\a`, `\b`, `\f`, `\n`, `\r`, `\t`, `\v`, `\0`, and `\xNN`; `\xNN` emits the exact byte without text re-encoding. Unknown, malformed, or incomplete escapes reject the send. HEX mode is unchanged.
- **CRLF**, next to **HEX** in the send controls, appends `\r\n` to outgoing data, independently of serial framing.
- **Checksum** under **Options > Sending** supports **None** (default), **XOR8**, **SUM8**, **SUM16-LE**, **SUM16-BE**, **Modbus CRC16**, and **CRC16-CCITT-FALSE**. Optional header and tail exclusion counts are bytes and both default to `0`. The checksum is computed over the data between the excluded edges and inserted before excluded tail bytes. **CRLF** is appended last as the frame terminator and is not counted in either exclusion length. One global setting applies to main and preset sends. `SUM16` wraps at 16 bits; choose `LE` or `BE` for wire byte order.
- Enable **Repeat** and set a millisecond interval to send the current input repeatedly.
- **Send** starts sending the current input; **Clear send** clears it. Both text and HEX modes show **Enter data to send...** when empty. **Stop** cancels an active file-send task; hover over it to see **Stop file sending**.

## Receive display and files

- **Show sent** in the receive controls displays local outgoing records marked `TX >` and incoming records marked `RX <`, without sending data again.
- Set **HEX bytes per line** under **Options > Data display** to 16, 32, or 64 (default 32). HEX display uses fixed rows without soft wrapping; packet boundaries and the start of retained history can leave short rows. The setting does not change text display or raw recording. Copying or exporting displayed HEX includes the line breaks.
- **Timestamp** splits display packets at the configured idle gap and adds timestamps. Set **Timestamp packet gap** in Options, default `20 ms`. Invalid edits remain uncommitted with an error until corrected.
- **Auto scroll** follows incoming data. Disable it to keep your viewport while reception continues; re-enable it to return to the latest line. New data preserves an existing selection, including with Auto scroll off. Selecting text temporarily pauses following without disabling auto scroll. Click once inside the receive log to clear the selection and resume following when you release the mouse button. Scrolling back alone does not disable following. If a selected caret has been scrolled out of view, the text control may reveal it after a buffer update. Auto scroll is not saved.
- Copy in the receive log copies selected text only. With no selection it leaves the clipboard unchanged.
- **Clear** clears the receive log and resets both footer counters (S/R for transmitted and received bytes). Serial settings use a compact format such as **1000000 bps · 8N1**; hover over serial settings or operation status to read their full text.
- **Open file** selects a regular file; hover over **Send file** to see its full path. **Delay** sets the wait between chunks in milliseconds, with no extra wait after the last chunk. A temporary line below the receive log shows the file name, percentage, and successfully written bytes, updated about every 200 ms. It is separate from RX/TX history. The line disappears on success; cancellation or failure retains the last progress and reason until another file is selected or sent. Use **Stop** to cancel; the displayed percentage stays below 100% while sending.
- Long progress messages remain on one line; hover to see the full text. If a transfer is cancelled before its size is known, the percentage is shown as `--%`.
- During long pauses between chunks, the latest successfully written bytes still update without incoming serial data. Disconnecting manually reports cancellation; an unexpected serial read failure reports its own cause.
- File progress uses the size of the opened file at the start of sending. Modifying that file during transmission can make the percentage differ from the bytes actually sent.
- File sizes use decimal units without scientific notation. If the serial port is closed just after a send finishes, its actual result is retained.
- **Save data** exports the displayed log as `.txt`. **Record data** saves raw received bytes to `.dat`. If recording cannot keep up with reception, it stops with an error instead of blocking reception or silently dropping data. Stopping, disconnecting, or exiting flushes and closes the capture. Recording requires a connection and does not resume after restart; completed `.dat` files remain on disk.
- Disconnect finishes delivery of pending receive/transmit records and counters. Recording targets remain protected from export overwrites until their final flush and close finish, including hard-link aliases. Flush/close failures remain reportable even when you exit immediately after stopping recording or disconnecting.

### Display history limit

Choose **Options > Data display > Display buffer limit**: `200K`, `500K`, `1M`, `2M`, `5M`, or `10M`, default `2M`. Units are decimal (`K = 1000`, `M = 1000000`) and count raw event payload bytes, including outgoing history, not HEX expansion or timestamps.

Overflow removes whole oldest events; the newest event is always retained, even if oversized. Lowering the limit trims history immediately; raising it cannot restore discarded data. There is no unlimited option or separate record-count limit. Use **Record data** for raw data independent of the display limit.
Small reads are stored compactly without reducing the selected raw-byte capacity or changing timestamp packet boundaries.

## Send presets

Click **Presets** to show the list beside the receive log; drag the divider to adjust the space. Click **Hide** to collapse it.

- New configurations start with `Preset 1` through `Preset 5`. Only configured entries appear, up to 99, without padding unused slots.
- Edit each entry's note, data, HEX/CRLF flags, and interval. Send one entry, send checked entries in order, or enable Loop for repeated sending. Stop ends the active task.
- **Add** appends a blank entry and scrolls to it. Delete requires confirmation; Cancel changes nothing. Remaining entries retain their order and are renumbered consecutively.
- Add and confirmed Delete save immediately, including blank entries. All entries may be deleted, and an empty list stays empty after restart. Adding and deleting are unavailable while sending.

## Language and appearance

Open **Options** in the receive controls. The language button shows **简体中文** in English and **English** in Chinese, indicating the target language. Controls, dialogs, tooltips, and already-visible statuses switch immediately, without restarting, and the choice is saved. The default is English (`en`); Simplified Chinese uses `zh-CN`. Missing or invalid language values use English.

Switching language does not translate or reset serial data, user input, files, preset notes or commands, protocol values, or original operating-system/library errors. Default preset names stay `Preset 1` through `Preset 5` in both languages.

Options also provides On top, text encoding, default-off text escape parsing, checksum algorithm and frame-byte exclusions, display history, timestamp packet gap, and receive-font settings. The receive font defaults to `宋体`, 9 pt, black, with a standard 16-color palette. Send and preset data use the system monospace font with Chinese fallback. Icons are embedded; no separate icon files are needed.

## Configuration and data safety

- Only `config.toml` beside the actual executable is used, after resolving executable symlinks. Working and user configuration directories are not searched; save failures never trigger a fallback directory.
- Missing configuration is created immediately. Existing files, including unreadable or invalid files, are not overwritten on load. Creation and save failures are reported; keep the executable directory writable.
- The repository [`config.toml`](config.toml) is a default reference, not a runtime dependency. Existing executable-local values are preserved; omitted scalar fields use defaults. Missing or explicitly empty preset lists stay empty rather than restoring the initial entries.
- Settings save on actual use, including successful connection, accepted sending, file export/capture, confirmed properties, preset addition/deletion, and immediately applied display, language, or preset-panel changes. Editing does not write per keystroke. Normal exit saves remaining drafts and window geometry; forced termination may lose recent changes. Unchanged uses and exits do not rewrite the file.
- Save errors remain visible and retry on the next use. Invalid stored encoding or packet-gap values use UTF-8 and `20 ms` with a startup warning, without rewriting the file on load. Invalid display limits use the default.
- Invalid retained curve rules do not block saving unrelated preferences. Repair or delete old profiles one at a time, even when several are invalid or an older list exceeds the 16-profile creation limit. Newly added or edited profiles must be valid; starting acquisition validates the selected profile independently of unmodified invalid entries.
- Older configurations without `BaudRates` migrate legacy baud values; an explicit array, including `[]`, takes precedence. Obsolete send-checksum and independent `Packet` settings are ignored and omitted on the next normal save, not rewritten merely on load. `Timestamp` and `PacketGap` remain supported.

## Runtime requirements

- Windows amd64, or Linux amd64 with a graphical session and GTK 3.
- Read/write permission for the selected serial port.
- A writable executable directory for creating and saving configuration.

For building from source, tests, and deployment, see [Build and verification workflow](ARCHITECTURE.md#build-and-verification-workflow).

## Limitations

- Provided builds target Windows amd64 and Linux amd64. Drivers determine supported baud rates and control-line behavior. No automatic-flow-control setting is currently exposed.
- Linux window-icon display depends on the window manager/compositor; a missing title-bar icon alone does not indicate loading failure. See the [GTK window-icon contract](https://docs.gtk.org/gtk3/method.Window.set_icon.html).
- Real serial transmission and reception require a physical or virtual serial port. Building or launching the UI does not verify serial I/O.

## License

This project is licensed under the [MIT License](LICENSE).

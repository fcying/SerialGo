# SerialGo

English | [简体中文](README.zh-CN.md)

A native serial-port debugging assistant for Windows and Linux, with text/HEX transmission, receive logging, file transfer, and editable send presets.

[Architecture](ARCHITECTURE.md) | [Roadmap](ROADMAP.md)

## Screenshots

<img width="900" height="574" alt="SerialGo English interface" src="https://github.com/user-attachments/assets/7fb7364b-8a54-4d27-95a3-0629a2f6bb29" />

## Getting started

1. Run `SerialGo.exe` on Windows or `SerialGo` in a Linux graphical session. Use a writable executable directory so the application can create and save its adjacent `config.toml`.
2. Select a port and baud rate, then click **Open**. The initial baud rate is `115200`; use **Config** beside the baud selector if you need to change framing or control lines.
3. Enter text or HEX bytes in the send area and click **Send**. Choose the text encoding in **Options** when needed.
4. View incoming data in the upper pane. Click **Save data** to export displayed text, or **Record data** to start saving raw received bytes while connected.

The lower cards contain connection controls, receive controls, file operations, and the send editor, from left to right. Drag the horizontal divider to enlarge the lower workspace; double-click to restore the compact default. Its position is not saved.

## Connecting and baud rates

- Ports are listed at startup and refreshed whenever the Port dropdown opens, by mouse or keyboard. Close and reopen it to retry; there is no separate Refresh button. Enumeration failure preserves the previous list and selection.
- Windows shows device descriptions when available, such as `COM12 USB-SERIAL CH340`; Linux shows device paths. Hover over a truncated port name to see its full value, including while connected. Opening and saving use the actual device name, not its description.
- Refresh preserves an available selected port, otherwise selects the first port. An empty list clears the selection and disables Open. Port and baud selectors are locked while connected.
- The main connection card shows port selection first, then a visible **Baud** label, a selector sized to display `1000000`, and **Config** on the same row. The Port selector and Config share a right edge. The bottom row places the port-status light immediately before **Open / Close**. Config opens data bits, parity, stop bits, RTS, and DTR properties. Settings are global, not per device; the footer still shows the current framing. While connected, only RTS/DTR can change; driver errors stay in the dialog without committing changes.
- **OK** applies dialog changes; Cancel, Escape, or closing the dialog discards drafts. Unchanged confirmation does not write configuration. Framing is edited only in Config, not on the main panel.

Both baud selectors center the selected value in the text area, excluding the right-hand arrow, and initially offer `9600`, `19200`, `57600`, `115200`, `921600`, and `1000000`:

- **Custom...** adds and selects a rate, deduplicated and sorted numerically. Input accepts integers from `1` to `4294967295`; actual support depends on the device and driver.
- Right-click a numeric rate, choose Delete, and confirm. Cancel or Escape changes nothing. Deleting the selected rate selects the first remaining rate.
- Main-panel additions and deletions save immediately. Inside Config, they remain drafts until the outer OK; outer Cancel or Escape discards them.
- An empty list stays empty after restart and disables Open until a rate is added. You may also edit `BaudRates` in `config.toml` while the application is closed.

## Sending data

- Text supports UTF-8 and GB18030. **HEX** in the send editor accepts contiguous bytes or spaces/commas between bytes, for example `01030000` or `01 03 00 00`.
- **CRLF** appends `\r\n` to outgoing data, independently of serial framing. No checksum is calculated or appended automatically; include protocol-required checksum bytes yourself.
- Enable **Repeat** and set the interval in milliseconds for timed sending. HEX, Repeat, its interval, and Presets share the send editor's top row, with Presets aligned to the right edge. The Repeat interval and file Delay fields share a centered 48-pixel width for five-digit values.
- **Send** and **Clear send** are stacked to the right of the input. Both text and HEX modes show **Enter data to send...** when empty. Clear send clears the current input. **Stop** beside Delay stops an active send task; hover it to see **Stop file sending**.

## Receive display and files

- **HEX** switches the receive display between text and HEX. **Options > Show sent data** includes local outgoing records marked `TX >` and incoming records marked `RX <`, without sending data again.
- **Timestamp** splits display packets at the configured idle gap and adds timestamps. Set **Timestamp packet gap** in Options, default `20 ms`. Invalid edits remain uncommitted with an error until corrected.
- **Auto scroll** follows incoming data. Disable it to keep your viewport while reception continues; re-enable it to return to the latest line. Selecting text temporarily pauses following until the selection is cleared, without disabling auto scroll. Scrolling back alone does not disable following. Auto scroll is not saved.
- **Clear** clears the receive log. The footer shows `S | R | Reset | Serial settings | Status`: S and R track actual transmitted and received bytes, followed by the Reset action; vertical bars separate fields with consistent horizontal padding. Counters and serial settings size to their contents, leaving the remaining width to the operation status. Hover over counters, serial settings, or status cells to see their full text.
- **Open file** selects a file; hover over **Send file** to see its full path. Files send in 256-byte chunks. **Delay**, beside Stop, sets the millisecond delay between chunks, with no delay after the last chunk; the field accommodates five centered digits.
- The file card has two actions: **Save data**, aligned with **Open file**, exports the displayed log as `.txt`; **Record data**, aligned with **Send file**, asynchronously captures raw received bytes to `.dat` through a bounded queue holding up to 512 KiB of payload bytes and a 128 KiB write buffer. The writer flushes when full or every second. If the queue fills, recording stops with an error instead of blocking serial reception or silently dropping data. Stop, disconnect, and exit flush and close the capture. Recording is disabled without a connection. Capture state is runtime-only and does not resume after restart; completed `.dat` files remain on disk.

### Display history limit

Choose **Options > Data display > Display buffer limit**: `200K`, `500K`, `1M`, `2M`, or `5M`, default `200K`. Units are decimal (`K = 1000`, `M = 1000000`) and count raw event payload bytes, including outgoing history, not HEX expansion or timestamps.

Overflow removes whole oldest events; the newest event is always retained, even if oversized. Lowering the limit trims history immediately; raising it cannot restore discarded data. There is no unlimited option or separate record-count limit. Use **Record data** for raw data independent of the display limit.

## Send presets

Click **Presets** on the send editor's top row to expand the panel, or **Hide** to collapse it.

- New configurations start with `Preset 1` through `Preset 5`. Only configured entries appear, up to 99, without padding unused slots.
- Edit each entry's note, data, HEX/CRLF flags, and interval. Send one entry, send checked entries in order, or enable Loop for repeated sending. Stop ends the active task.
- **Add** appends a blank entry and scrolls to it. Delete requires confirmation; Cancel changes nothing. Remaining entries retain their order and are renumbered consecutively.
- Add and confirmed Delete save immediately, including blank entries. All entries may be deleted, and an empty list stays empty after restart. Adding and deleting are unavailable while sending.

## Language and appearance

Open **Options** in the receive controls. The language button shows **简体中文** in English and **English** in Chinese, indicating the target language. Controls, dialogs, tooltips, and already-visible statuses switch immediately, without restarting, and the choice is saved. The default is English (`en`); Simplified Chinese uses `zh-CN`. Missing or invalid language values use English.

Switching language does not translate or reset serial data, user input, files, preset notes or commands, protocol values, or original operating-system/library errors. Default preset names stay `Preset 1` through `Preset 5` in both languages.

Options also provides On top, text encoding, display history, Show sent data, timestamp packet gap, and receive-font settings. The receive font defaults to `宋体`, 9 pt, black, with a standard 16-color palette. Send and preset data use the system monospace font with Chinese fallback. Icons are embedded; no separate icon files are needed.

## Configuration and data safety

- Only `config.toml` beside the actual executable is used, after resolving executable symlinks. Working and user configuration directories are not searched; save failures never trigger a fallback directory.
- Missing configuration is created immediately. Existing files, including unreadable or invalid files, are not overwritten on load. Creation and save failures are reported; keep the executable directory writable.
- The repository [`config.toml`](config.toml) is a default reference, not a runtime dependency. Existing executable-local values are preserved; omitted scalar fields use defaults. Missing or explicitly empty preset lists stay empty rather than restoring the initial entries.
- Settings save on actual use, including successful connection, accepted sending, file export/capture, confirmed properties, preset addition/deletion, and immediately applied display, language, or preset-panel changes. Editing does not write per keystroke. Normal exit saves remaining drafts; forced termination may lose them. Unchanged uses and exits do not rewrite the file.
- Save errors remain visible and retry on the next use. Invalid stored encoding or packet-gap values use UTF-8 and `20 ms` with a startup warning, without rewriting the file on load. Invalid display limits use the default.
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

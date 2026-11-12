set unstable
set default-script
set script-interpreter := ["bash", "-euo", "pipefail"]

export CGO_ENABLED := "0"
export GOARCH := "amd64"

out := "build"
name := "SerialGo"
ldflags := "-s -w"
test_dir := "/mnt/d/project/SerialGo"
powershell := "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"

_default: test

_config:
    cp -- config.toml assets/default-config.toml

_icon: _config
    go run ./cmd/appicon -arch "{{GOARCH}}"

build:
    just windows

windows: _icon
    GOOS=windows go build -trimpath -ldflags "-H=windowsgui {{ldflags}}" -o "{{out}}/windows-{{GOARCH}}/{{name}}.exe" .

linux: _icon
    GOOS=linux go build -trimpath -ldflags "{{ldflags}}" -o "{{out}}/linux-{{GOARCH}}/{{name}}" .

test: windows
    dest="{{test_dir}}"
    if [[ ! -d "$dest" ]]; then
        printf 'Skipping copy: destination directory does not exist: %s\n' "$dest"
        exit 0
    fi
    status=0
    cp -- "{{out}}/windows-{{GOARCH}}/{{name}}.exe" "$dest/{{name}}.exe" || status=$?
    if [[ "$status" -ne 0 ]]; then
        printf 'Copy failed; closing target Windows process and retrying once: %s\n' "$dest/{{name}}.exe" >&2
        target=$(wslpath -w "$dest/{{name}}.exe")
        target=${target//\'/\'\'}
        "{{powershell}}" -NoProfile -NonInteractive -Command "\$ErrorActionPreference = 'Stop'; \$target = '$target'; foreach (\$process in @(Get-Process -Name '{{name}}' -ErrorAction SilentlyContinue)) { if (\$process.Path -eq \$target) { \$process.Kill(); if (-not \$process.WaitForExit(5000)) { throw 'The target process did not exit' } } }"
        cp -- "{{out}}/windows-{{GOARCH}}/{{name}}.exe" "$dest/{{name}}.exe"
    fi
    cd "$dest"
    setsid "./{{name}}.exe" &
    sleep 1

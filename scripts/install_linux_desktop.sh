#!/bin/sh
# Install desktop integration for a user-local Linux build of Supersonic.
set -eu

app_id=io.github.supersonic-app.supersonic
data_home="${XDG_DATA_HOME:-$HOME/.local/share}"

gobin="$(go env GOBIN)"
if [ -z "$gobin" ]; then
    gopath="$(go env GOPATH)"
    gobin="${gopath%%:*}/bin"
fi
binary="$gobin/supersonic"

if [ ! -x "$binary" ]; then
    printf 'Supersonic executable not found at %s\n' "$binary" >&2
    exit 1
fi

for size in 128 256 512; do
    install -Dm644 "res/appicon-$size.png" \
        "$data_home/icons/hicolor/${size}x${size}/apps/$app_id.png"
done

desktop="$data_home/applications/$app_id.desktop"
mkdir -p "$data_home/applications"

# Use the full Go install path: graphical sessions do not always inherit the
# shell PATH that contains GOBIN or GOPATH/bin.
# Escape the path for the Desktop Entry Exec quoting and field-code rules.
desktop_exec="$(printf '%s' "$binary" | sed \
    -e 's/\\/\\\\\\\\/g' \
    -e 's/"/\\\\"/g' \
    -e 's/`/\\\\`/g' \
    -e 's/\$/\\\\$/g' \
    -e 's/%/%%/g')"

while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
        Exec=*) printf 'Exec="%s"\n' "$desktop_exec" ;;
        *) printf '%s\n' "$line" ;;
    esac
done < "res/$app_id.desktop" > "$desktop"

printf 'Installed Supersonic launcher: %s\n' "$desktop"

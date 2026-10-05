#!/bin/sh
# Build first with ./autogen.sh && ./configure && make.
# This runner always creates its own disposable Xorg server.
set -eu
cd "$(dirname "$0")/.."
module=$(pwd)/src/.libs/neko_drv.so
[ -f "$module" ] || { echo 'Build neko_drv.so before running this test' >&2; exit 1; }
work=$(mktemp -d /tmp/neko-xorg-test.XXXXXX)
pid=
cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
    if [ "$status" -ne 0 ]; then cat "$work/Xorg.log" "$work/stderr.log" >&2 2>/dev/null || true; fi
    rm -rf "$work"
    exit "$status"
}
trap cleanup EXIT HUP INT TERM
mkdir -p "$work/modules/input"
ln -s "$module" "$work/modules/input/neko_drv.so"
cat > "$work/xorg.conf" <<CONFIG
Section "ServerFlags"
    Option "AutoAddDevices" "false"
    Option "AutoEnableDevices" "false"
    Option "AllowMouseOpenFail" "true"
EndSection
Section "Files"
    ModulePath "$work/modules"
    ModulePath "/usr/lib/xorg/modules"
EndSection
Section "Device"
    Identifier "dummy_video"
    Driver "dummy"
    VideoRam 256000
EndSection
Section "Monitor"
    Identifier "dummy_monitor"
    HorizSync 5.0-1000.0
    VertRefresh 5.0-200.0
    Modeline "1280x720" 74.50 1280 1344 1472 1664 720 723 728 748
EndSection
Section "Screen"
    Identifier "dummy_screen"
    Device "dummy_video"
    Monitor "dummy_monitor"
    DefaultDepth 24
    SubSection "Display"
        Depth 24
        Modes "1280x720"
    EndSubSection
EndSection
Section "InputDevice"
    Identifier "dummy_touchscreen"
    Driver "neko"
    Option "SocketName" "$work/neko.sock"
EndSection
Section "ServerLayout"
    Identifier "test_layout"
    Screen "dummy_screen"
    InputDevice "dummy_touchscreen" "CorePointer"
EndSection
CONFIG
${CC:-cc} -std=c99 -O2 -Wall -Wextra -Werror tests/scroll-integration.c \
    -o "$work/scroll-integration" $(pkg-config --cflags --libs x11 xi)
Xorg -config "$work/xorg.conf" -logfile "$work/Xorg.log" \
    -displayfd 3 -nolisten tcp -noreset -novtswitch -sharevts \
    3>"$work/display" >"$work/stderr.log" 2>&1 &
pid=$!
ready=false
for attempt in $(seq 1 100); do
    if ! kill -0 "$pid" 2>/dev/null; then echo 'Xorg exited before becoming ready' >&2; exit 1; fi
    if [ -s "$work/display" ] && [ -S "$work/neko.sock" ]; then ready=true; break; fi
    sleep 0.1
done
[ "$ready" = true ] || { echo 'Timed out starting isolated Xorg' >&2; exit 1; }
"$work/scroll-integration" ":$(cat "$work/display")" "$work/neko.sock" dummy_touchscreen

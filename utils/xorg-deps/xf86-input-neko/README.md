# xf86-input-neko
[X.org](https://x.org/) [neko](http://m1k1o/neko) input driver

### how to use
xf86-input-neko assumes you have only one virtual touchscreen device available, see
`80-neko.conf`. If there are multiple in your system, please specify one config
section for each.
xf86-input-neko aims to make [neko](http://m1k1o/neko) easy to use and doesn't
offer special configuration options.

* `./configure --prefix=/usr`
* `make`
* `sudo make install`

Done.

To _uninstall_, again go inside the extracted directory, and do

    sudo make uninstall

### scroll events
Besides XI2 touch events, the driver accepts a `NEKO_SCROLL` (`0x80`) message
whose `x` and `y` fields carry relative scroll deltas in XI2.1 scroll units,
120 units per wheel notch (`SCROLL_INCREMENT`). They are posted through the
device's `REL_HSCROLL`/`REL_VSCROLL` scroll valuators, so XI2.1-aware clients
get smooth scrolling while the X server emulates wheel buttons 4-7 for the
rest. The device registers as `XI_MOUSE` rather than `XI_TOUCHSCREEN` so that
Chromium honours the scroll valuators. The server sends deltas in these units, so its XTest fallback has to divide
by the same increment to replay them as wheel clicks.

This is the same driver source that ships in
[kernel-images](https://github.com/kernel/kernel-images)
(`images/chromium-headful/xorg-deps/xf86-input-neko`).

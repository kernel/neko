# Isolated driver integration test

`scroll-integration.c` connects to a **fresh, disposable Xorg server** with the
built `neko_drv.so` and a dummy video driver. Do not point it at an interactive
desktop. It creates a full-screen test window, moves that server's pointer, and
injects scroll and touch fixtures through the driver's Unix socket.

Run the complete check on a machine with Xorg, the dummy video driver, and
X11/XI client development packages:

```sh
./autogen.sh --prefix=/usr
./configure
make -j2
./tests/run-integration.sh
```

The runner creates and cleans up its own display, configuration, module path,
and socket. CI runs this on Debian bullseye (the base-image default) and bookworm.

To use another disposable test server, build with the X11 and XI client development packages:

```sh
cc -std=c99 -O2 -Wall -Wextra -Werror \
  tests/scroll-integration.c -o /tmp/neko-scroll-integration \
  $(pkg-config --cflags --libs x11 xi)
/tmp/neko-scroll-integration "$TEST_DISPLAY" "$TEST_SOCKET" dummy_touchscreen
```

The optional third argument is the configured input device's `Identifier`;
it defaults to `dummy_touchscreen`. The runner must load the just-built module,
wait for Xorg and the socket to be ready, and stop Xorg on both success and
failure. Xvfb alone cannot load an Xorg input module and is not a substitute.

## Coverage

- XI2.2 negotiation and actual `XIQueryDevice` metadata: five valuators, absolute
  axes 0–2, relative scroll axes 3–4, correct labels, vertical/horizontal scroll
  classes with increment 120 and preferred legacy emulation, ten direct touches
- Signed vertical/horizontal and diagonal scroll fixtures, using the production
  12-byte little-endian socket layout
- Exact raw deltas and valuator masks, one cooked motion event per nonzero
  fixture, no touch events from scroll, and no pointer displacement
- Wheel button 4–7 press/release counts from a separate legacy X client
- Four 30-unit deltas accumulating into one legacy wheel notch; zero as a no-op
- Touch begin/update/end, stable touch IDs, unchanged raw coordinates/pressure,
  end-without-coordinates sentinel, and separation of touch/scroll masks when
  the two event types are interleaved

The XI2 touch class does not expose a touch-axis count. The test verifies the
three-axis touch contract through event masks instead.

## Boundaries

Start a new Xorg instance for each test run so scroll accumulators begin at
zero. No other client should write to the input socket or grab touch/pointer
events. Each fixture is sent as one 12-byte write; stream-fragment handling is
an existing driver limitation, not covered by this success-path suite.

The inherited driver posts from a bespoke thread without waking Xorg's main
loop. The test sends periodic XNoOp requests, as an active X client would, to
wake that loop. It still asserts observed events; it does not validate delivery
on a completely idle server. Xorg itself restores X/Y mask bits on the sentinel
touch-end event, so the raw end mask is expected to contain those two axes.

The runner enforces a 30-second timeout. The test waits up to three seconds for asynchronous input and then drains both
X clients to quiescence. A successful socket write or `XSync` alone is not
considered proof of event delivery.

These checks do not establish Chromium rendering behavior, Ctrl-wheel modifier
ordering across the separate X11 and driver sockets, reconnection behavior,
or thread/teardown safety. Those require separate integration or stress tests.

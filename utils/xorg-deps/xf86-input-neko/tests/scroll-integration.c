/* Integration test for an isolated Xorg server running xf86-input-neko.
 * Never run against an interactive desktop: this creates a window and moves
 * its pointer. The caller owns the disposable Xorg process and socket.
 */
#define _POSIX_C_SOURCE 200809L
#include <X11/Xlib.h>
#include <X11/extensions/XInput2.h>
#include <errno.h>
#include <math.h>
#include <poll.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <time.h>
#include <unistd.h>

#define NEKO_SCROLL 0x80
#define TIMEOUT_MS 3000
#define QUIET_MS 100
#define MAX_EVENTS 64

static Display *legacy, *xi;
static int opcode, device_id, socket_fd;
static Window window;
static const char *current_test = "setup";

struct sample {
    unsigned int mask;
    double values[8];
    unsigned int id;
};
struct capture {
    int raw_motion_count, motion_count;
    struct sample raw_motion[MAX_EVENTS];
    int raw_touch_count[3], touch_count[3];
    struct sample raw_touch[3], touch[3];
    int press[8], release[8];
};

static void fail(const char *format, ...)
{
    va_list args;
    fprintf(stderr, "FAIL %s: ", current_test);
    va_start(args, format);
    vfprintf(stderr, format, args);
    va_end(args);
    fputc('\n', stderr);
    exit(EXIT_FAILURE);
}

static int xerror(Display *display, XErrorEvent *event)
{
    char message[256];
    XGetErrorText(display, event->error_code, message, sizeof(message));
    fail("X error %s (request %u.%u)", message,
         event->request_code, event->minor_code);
    return 0;
}

static long long now_ms(void)
{
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0)
        fail("clock_gettime: %s", strerror(errno));
    return (long long)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static struct sample sample_valuators(const XIValuatorState *state,
                                     const double *values, unsigned int id)
{
    struct sample result = { .id = id };
    int next = 0;
    for (int axis = 0; axis < state->mask_len * 8; ++axis) {
        if (!XIMaskIsSet(state->mask, axis))
            continue;
        if (axis >= 8)
            fail("unexpected valuator %d", axis);
        result.mask |= 1u << axis;
        result.values[axis] = values[next++];
    }
    return result;
}

static int drain(struct capture *capture)
{
    int processed = 0;
    while (XPending(legacy)) {
        XEvent event;
        XNextEvent(legacy, &event);
        processed++;
        if (event.type == ButtonPress && event.xbutton.button < 8)
            capture->press[event.xbutton.button]++;
        if (event.type == ButtonRelease && event.xbutton.button < 8)
            capture->release[event.xbutton.button]++;
    }
    while (XPending(xi)) {
        XEvent event;
        XNextEvent(xi, &event);
        processed++;
        if (event.type != GenericEvent || event.xcookie.extension != opcode ||
            !XGetEventData(xi, &event.xcookie))
            continue;
        int type = event.xcookie.evtype;
        if (type == XI_RawMotion ||
            (type >= XI_RawTouchBegin && type <= XI_RawTouchEnd)) {
            XIRawEvent *raw = event.xcookie.data;
            if (raw->sourceid == device_id) {
                struct sample sample = sample_valuators(&raw->valuators,
                                                        raw->raw_values,
                                                        raw->detail);
                if (type == XI_RawMotion) {
                    if (capture->raw_motion_count == MAX_EVENTS)
                        fail("too many raw motion events");
                    capture->raw_motion[capture->raw_motion_count++] = sample;
                } else {
                    int index = type - XI_RawTouchBegin;
                    capture->raw_touch_count[index]++;
                    capture->raw_touch[index] = sample;
                }
            }
        } else if (type == XI_Motion ||
                   (type >= XI_TouchBegin && type <= XI_TouchEnd)) {
            XIDeviceEvent *dev = event.xcookie.data;
            if (dev->sourceid == device_id) {
                if (type == XI_Motion) {
                    capture->motion_count++;
                } else {
                    int index = type - XI_TouchBegin;
                    capture->touch_count[index]++;
                    capture->touch[index] = sample_valuators(&dev->valuators,
                                                            dev->valuators.values,
                                                            dev->detail);
                }
            }
        }
        XFreeEventData(xi, &event.xcookie);
    }
    return processed;
}

/* Wait for driver output, then drain both X clients to quiescence. The socket
 * write is asynchronous, so XSync alone would not be a completion barrier. */
static struct capture collect(int expect_output)
{
    struct capture capture = {0};
    long long deadline = now_ms() + (expect_output ? TIMEOUT_MS : QUIET_MS * 2);
    long long quiet_since = 0;
    for (;;) {
        int processed = drain(&capture);
        long long now = now_ms();
        int received = capture.raw_motion_count + capture.raw_touch_count[0] +
                       capture.raw_touch_count[1] + capture.raw_touch_count[2];
        if (processed || !quiet_since)
            quiet_since = now;
        if ((received && now - quiet_since >= QUIET_MS) || now >= deadline)
            return capture;
        long long remaining = deadline - now;
        int timeout = remaining < QUIET_MS ? (int)remaining : QUIET_MS;
        struct pollfd fds[2] = {
            { .fd = ConnectionNumber(legacy), .events = POLLIN },
            { .fd = ConnectionNumber(xi), .events = POLLIN }
        };
        if (poll(fds, 2, timeout) < 0 && errno != EINTR)
            fail("poll: %s", strerror(errno));
    }
}

static void send_frame(uint8_t type, uint16_t id, int32_t x, int32_t y,
                       uint8_t pressure)
{
    unsigned char frame[12] = { type, id & 0xff, id >> 8 };
    for (int byte = 0; byte < 4; ++byte) {
        frame[3 + byte] = (uint32_t)x >> (byte * 8);
        frame[7 + byte] = (uint32_t)y >> (byte * 8);
    }
    frame[11] = pressure;
    /* Keep one message in one write: split-frame tolerance is a separate,
     * pre-existing protocol defect and is deliberately not hidden here. */
    ssize_t written;
    do {
        written = send(socket_fd, frame, sizeof(frame), MSG_NOSIGNAL);
    } while (written < 0 && errno == EINTR);
    if (written != (ssize_t)sizeof(frame))
        fail("socket write returned %zd: %s", written,
             written < 0 ? strerror(errno) : "short write");
}

static void pointer_position(int *x, int *y)
{
    Window root, child;
    int win_x, win_y;
    unsigned int mask;
    if (!XQueryPointer(legacy, DefaultRootWindow(legacy), &root, &child,
                       x, y, &win_x, &win_y, &mask))
        fail("pointer is on another screen");
}

static void check_buttons(const struct capture *capture,
                          int up, int down, int left, int right)
{
    int expected[8] = { 0, 0, 0, 0, up, down, left, right };
    for (int button = 4; button <= 7; ++button) {
        if (capture->press[button] != expected[button] ||
            capture->release[button] != expected[button])
            fail("button %d: press/release %d/%d, expected %d/%d", button,
                 capture->press[button], capture->release[button],
                 expected[button], expected[button]);
    }
}

static void scroll_case(const char *name, int32_t x, int32_t y,
                        int up, int down, int left, int right)
{
    current_test = name;
    int before_x, before_y, after_x, after_y;
    pointer_position(&before_x, &before_y);
    send_frame(NEKO_SCROLL, 0, x, y, 0);
    struct capture capture = collect(x || y);
    int expected_count = (x || y) ? 1 : 0;
    if (capture.raw_motion_count != expected_count ||
        capture.motion_count != expected_count)
        fail("raw/cooked motion count %d/%d, expected %d/%d",
             capture.raw_motion_count, capture.motion_count,
             expected_count, expected_count);
    if (expected_count) {
        struct sample *sample = &capture.raw_motion[0];
        unsigned int mask = (y ? 1u << 3 : 0) | (x ? 1u << 4 : 0);
        if (sample->mask != mask || sample->values[3] != y ||
            sample->values[4] != x)
            fail("raw mask/deltas %#x %.3f/%.3f, expected %#x %d/%d",
                 sample->mask, sample->values[4], sample->values[3], mask, x, y);
    }
    for (int i = 0; i < 3; ++i)
        if (capture.touch_count[i] || capture.raw_touch_count[i])
            fail("scroll generated a touch event");
    check_buttons(&capture, up, down, left, right);
    pointer_position(&after_x, &after_y);
    if (before_x != after_x || before_y != after_y)
        fail("scroll moved pointer from %d,%d to %d,%d", before_x, before_y,
             after_x, after_y);
    printf("PASS %s\n", name);
}

static unsigned int touch_case(const char *name, uint8_t type,
                               int32_t x, int32_t y, uint8_t pressure)
{
    current_test = name;
    send_frame(type, 0x1234, x, y, pressure);
    struct capture capture = collect(1);
    int index = type - XI_TouchBegin;
    if (capture.raw_touch_count[index] != 1 || capture.touch_count[index] != 1)
        fail("raw/cooked touch count %d/%d, expected 1/1",
             capture.raw_touch_count[index], capture.touch_count[index]);
    struct sample *raw = &capture.raw_touch[index];
    struct sample *cooked = &capture.touch[index];
    unsigned int expected_mask = (x == -1 && y == -1) ? 0 : 7;
    if (raw->mask != expected_mask || (cooked->mask & ~7u))
        fail("touch mask raw/cooked %#x/%#x, expected raw %#x and only axes 0-2",
             raw->mask, cooked->mask, expected_mask);
    if (expected_mask && (raw->values[0] != x || raw->values[1] != y ||
                          raw->values[2] != pressure))
        fail("raw touch coordinates/pressure differ from fixture");
    for (int i = 0; i < 3; ++i)
        if (i != index && (capture.touch_count[i] || capture.raw_touch_count[i]))
            fail("unexpected touch event type");
    check_buttons(&capture, 0, 0, 0, 0);
    printf("PASS %s\n", name);
    return cooked->id;
}

static void verify_device(const char *name)
{
    int count;
    XIDeviceInfo *devices = XIQueryDevice(xi, XIAllDevices, &count);
    if (!devices)
        fail("XIQueryDevice failed");
    XIDeviceInfo *device = NULL;
    for (int i = 0; i < count; ++i)
        if (strcmp(devices[i].name, name) == 0) {
            if (device)
                fail("duplicate device name %s", name);
            device = &devices[i];
        }
    if (!device)
        fail("device %s not found", name);
    if (!device->enabled || device->use != XISlavePointer)
        fail("device must be an enabled, attached slave pointer");
    device_id = device->deviceid;
    int axes = 0, scrolls = 0, touches = 0;
    unsigned int axis_mask = 0;
    for (int i = 0; i < device->num_classes; ++i) {
        XIAnyClassInfo *info = device->classes[i];
        if (info->type == XIValuatorClass) {
            XIValuatorClassInfo *axis = (XIValuatorClassInfo *)info;
            if (axis->number < 0 || axis->number >= 5)
                fail("unexpected axis %d", axis->number);
            int mode = axis->number < 3 ? XIModeAbsolute : XIModeRelative;
            if (axis->mode != mode)
                fail("axis %d mode %d, expected %d", axis->number, axis->mode, mode);
            if (axis->number >= 3) {
                const char *label = axis->number == 3 ? "Rel Vert Scroll" : "Rel Horiz Scroll";
                if (axis->label != XInternAtom(xi, label, True))
                    fail("axis %d has incorrect scroll label", axis->number);
            }
            axes++;
            axis_mask |= 1u << axis->number;
        } else if (info->type == XIScrollClass) {
            XIScrollClassInfo *scroll = (XIScrollClassInfo *)info;
            int type = scroll->number == 3 ? XIScrollTypeVertical : XIScrollTypeHorizontal;
            if ((scroll->number != 3 && scroll->number != 4) ||
                scroll->scroll_type != type || scroll->increment != 120.0 ||
                !(scroll->flags & XIScrollFlagPreferred) ||
                (scroll->flags & XIScrollFlagNoEmulation))
                fail("invalid scroll metadata for axis %d", scroll->number);
            scrolls++;
        } else if (info->type == XITouchClass) {
            XITouchClassInfo *touch = (XITouchClassInfo *)info;
            if (touch->mode != XIDirectTouch || touch->num_touches != 10)
                fail("invalid touch metadata");
            touches++;
        }
    }
    if (axes != 5 || axis_mask != 31 || scrolls != 2 || touches != 1)
        fail("class counts axes/scroll/touch %d/%d/%d, expected 5/2/1",
             axes, scrolls, touches);
    printf("PASS XIQueryDevice: %s (id %d), 5 axes, two 120-unit scroll classes, 10 direct touches\n",
           name, device_id);
    XIFreeDeviceInfo(devices);
}

int main(int argc, char **argv)
{
    if (argc != 3 && argc != 4) {
        fprintf(stderr, "Usage: %s DISPLAY SOCKET [DEVICE_NAME]\n", argv[0]);
        return EXIT_FAILURE;
    }
    XSetErrorHandler(xerror);
    legacy = XOpenDisplay(argv[1]);
    xi = XOpenDisplay(argv[1]);
    if (!legacy || !xi)
        fail("cannot open disposable Xorg display %s", argv[1]);
    int event, error, major = 2, minor = 2;
    if (!XQueryExtension(xi, "XInputExtension", &opcode, &event, &error) ||
        XIQueryVersion(xi, &major, &minor) != Success)
        fail("XI2.2 is required");
    verify_device(argc == 4 ? argv[3] : "dummy_touchscreen");

    int screen = DefaultScreen(legacy);
    XSetWindowAttributes attrs = { .override_redirect = True };
    window = XCreateWindow(legacy, RootWindow(legacy, screen), 0, 0,
                           DisplayWidth(legacy, screen), DisplayHeight(legacy, screen),
                           0, CopyFromParent, InputOutput, CopyFromParent,
                           CWOverrideRedirect, &attrs);
    XSelectInput(legacy, window, ButtonPressMask | ButtonReleaseMask | PointerMotionMask);
    XMapRaised(legacy, window);
    XWarpPointer(legacy, None, window, 0, 0, 0, 0, 80, 80);
    XSync(legacy, False);

    unsigned char cooked[XIMaskLen(XI_LASTEVENT)] = {0};
    XISetMask(cooked, XI_Motion);
    XISetMask(cooked, XI_TouchBegin);
    XISetMask(cooked, XI_TouchUpdate);
    XISetMask(cooked, XI_TouchEnd);
    XIEventMask selected = { XIAllMasterDevices, sizeof(cooked), cooked };
    XISelectEvents(xi, window, &selected, 1);
    unsigned char raw[XIMaskLen(XI_LASTEVENT)] = {0};
    XISetMask(raw, XI_RawMotion);
    XISetMask(raw, XI_RawTouchBegin);
    XISetMask(raw, XI_RawTouchUpdate);
    XISetMask(raw, XI_RawTouchEnd);
    selected.mask = raw;
    selected.mask_len = sizeof(raw);
    XISelectEvents(xi, DefaultRootWindow(xi), &selected, 1);
    XSync(xi, False);
    (void)collect(0);

    struct sockaddr_un address = { .sun_family = AF_UNIX };
    if (strlen(argv[2]) >= sizeof(address.sun_path))
        fail("socket path too long");
    strcpy(address.sun_path, argv[2]);
    socket_fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (socket_fd < 0 || connect(socket_fd, (struct sockaddr *)&address, sizeof(address)) < 0)
        fail("cannot connect socket %s: %s", argv[2], strerror(errno));

    scroll_case("down one notch", 0, 120, 0, 1, 0, 0);
    scroll_case("up one notch", 0, -120, 1, 0, 0, 0);
    scroll_case("right one notch", 120, 0, 0, 0, 0, 1);
    scroll_case("left one notch", -120, 0, 0, 0, 1, 0);
    scroll_case("diagonal right/up", 120, -120, 1, 0, 0, 1);
    scroll_case("diagonal left/down", -120, 120, 0, 1, 1, 0);
    scroll_case("zero is a no-op", 0, 0, 0, 0, 0, 0);
    scroll_case("quarter notch 1", 0, 30, 0, 0, 0, 0);
    scroll_case("quarter notch 2", 0, 30, 0, 0, 0, 0);
    scroll_case("quarter notch 3", 0, 30, 0, 0, 0, 0);
    scroll_case("quarter notch 4 accumulates", 0, 30, 0, 1, 0, 0);
    scroll_case("reverse accumulated notch", 0, -120, 1, 0, 0, 0);

    unsigned int touch_id = touch_case("touch begin after scroll", XI_TouchBegin, 16384, 16384, 128);
    scroll_case("scroll during touch", 120, 0, 0, 0, 0, 1);
    if (touch_case("touch update after scroll", XI_TouchUpdate, 17000, 17000, 64) != touch_id)
        fail("touch ID changed on update");
    if (touch_case("touch end sentinel", XI_TouchEnd, -1, -1, 0) != touch_id)
        fail("touch ID changed on end");
    scroll_case("scroll after touch", -120, 0, 0, 0, 1, 0);

    close(socket_fd);
    XDestroyWindow(legacy, window);
    XCloseDisplay(xi);
    XCloseDisplay(legacy);
    puts("PASS all isolated xf86-input-neko scroll/touch integration checks");
    return EXIT_SUCCESS;
}

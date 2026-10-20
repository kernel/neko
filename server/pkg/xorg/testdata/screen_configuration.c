#include <assert.h>
#include "../xorg.c"

static XRRScreenSize sizes[] = {{376, 480, 0, 0}};
static short rates[] = {25, 59, 60};
static int num_rates = 3;
static int set_calls = 0;
static int free_calls = 0;
static short applied_rate;
static Status set_status = RRSetConfigSuccess;

XRRScreenConfiguration *__wrap_XRRGetScreenInfo(Display *display, Window root) {
  return (XRRScreenConfiguration *) 1;
}

XRRScreenSize *__wrap_XRRConfigSizes(XRRScreenConfiguration *conf, int *count) {
  *count = 1;
  return sizes;
}

short *__wrap_XRRConfigRates(XRRScreenConfiguration *conf, int size, int *count) {
  assert(size == 0);
  *count = num_rates;
  return rates;
}

void __wrap_XRRFreeScreenConfigInfo(XRRScreenConfiguration *conf) {
  free_calls++;
}

Status __wrap_XRRSetScreenConfigAndRate(Display *display, XRRScreenConfiguration *conf,
    Drawable root, int size, Rotation rotation, short rate, Time time) {
  set_calls++;
  applied_rate = rate;
  return set_status;
}

XRRScreenSize *__wrap_XRRSizes(Display *display, int screen, int *count) {
  *count = 1;
  return sizes;
}

int main(void) {
  _XPrivDisplay display = calloc(1, sizeof(*display));
  display->screens = calloc(1, sizeof(Screen));
  DISPLAY = (Display *) display;

  short rate = 60;
  assert(XSetScreenConfiguration(376, 480, &rate) == RRSetConfigSuccess);
  assert(rate == 60 && applied_rate == 60);

  num_rates = 2;
  rate = 60;
  assert(XSetScreenConfiguration(376, 480, &rate) == RRSetConfigSuccess);
  assert(rate == 59 && applied_rate == 59);

  rate = 58;
  assert(XSetScreenConfiguration(376, 480, &rate) == RRSetConfigSuccess);
  assert(rate == 59);

  rate = 120;
  assert(XSetScreenConfiguration(376, 480, &rate) == BadValue);
  assert(rate == 120 && set_calls == 3);

  num_rates = 0;
  rate = 60;
  assert(XSetScreenConfiguration(376, 480, &rate) == BadValue);
  assert(set_calls == 3);

  assert(XSetScreenConfiguration(379, 480, &rate) == XScreenSizeNotFound);
  assert(free_calls == 6);

  num_rates = 2;
  set_status = RRSetConfigFailed;
  assert(XSetScreenConfiguration(376, 480, &rate) == RRSetConfigFailed);
  assert(rate == 60);

  // Retrying a rounded size must not issue XRRCreateMode for an existing mode.
  int width = 379, height = 480;
  XCreateScreenMode(&width, &height, 60);
  assert(width == 376 && height == 480);

  free(display->screens);
  free(display);
  return 0;
}

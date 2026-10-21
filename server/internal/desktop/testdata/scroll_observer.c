#include <X11/Xlib.h>
#include <stdio.h>
#include <string.h>

/* A dedicated window on the opt-in test display records real XTest events.
 * Each collection follows the producer's XSync and our own round trip, so
 * tests do not need a timing sleep to wait for input delivery. */
int main(int argc, char **argv) {
  if (argc != 2) {
    fprintf(stderr, "usage: scroll_observer DISPLAY\n");
    return 1;
  }

  Display *display = XOpenDisplay(argv[1]);
  if (display == NULL) {
    fprintf(stderr, "cannot open test display %s\n", argv[1]);
    return 1;
  }

  int screen = DefaultScreen(display);
  Window window = XCreateSimpleWindow(display, RootWindow(display, screen),
                                       0, 0, 640, 480, 0,
                                       BlackPixel(display, screen),
                                       WhitePixel(display, screen));
  XSelectInput(display, window, ButtonPressMask);
  XMapRaised(display, window);
  XSetInputFocus(display, window, RevertToPointerRoot, CurrentTime);
  XWarpPointer(display, None, window, 0, 0, 0, 0, 32, 32);
  XSync(display, False);
  puts("READY");
  fflush(stdout);

  char command[32];
  while (fgets(command, sizeof(command), stdin) != NULL) {
    if (strcmp(command, "collect\n") != 0) {
      fprintf(stderr, "unknown observer command\n");
      XCloseDisplay(display);
      return 1;
    }
    XSync(display, False);
    while (XPending(display)) {
      XEvent event;
      XNextEvent(display, &event);
      if (event.type == ButtonPress) {
        printf("BUTTON %u %u\n", event.xbutton.button, event.xbutton.state);
      }
    }
    puts("DONE");
    fflush(stdout);
  }

  XDestroyWindow(display, window);
  XCloseDisplay(display);
  return 0;
}

package xorg

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScreenConfiguration(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "screen-configuration")
	cmd := exec.Command("cc", "testdata/screen_configuration.c", "-o", binary,
		"-ffunction-sections", "-Wl,--gc-sections",
		"-Wl,--wrap=XRRGetScreenInfo", "-Wl,--wrap=XRRConfigSizes",
		"-Wl,--wrap=XRRConfigRates", "-Wl,--wrap=XRRFreeScreenConfigInfo",
		"-Wl,--wrap=XRRSetScreenConfigAndRate", "-Wl,--wrap=XRRSizes",
		"-lX11", "-lXrandr", "-lXtst", "-lXfixes", "-lxcvt")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile screen configuration tests: %v\n%s", err, out)
	}
	if out, err := exec.Command(binary).CombinedOutput(); err != nil {
		t.Fatalf("screen configuration tests: %v\n%s", err, out)
	}
}

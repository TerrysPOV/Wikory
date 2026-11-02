package confine

import "strings"

// reservedDeviceNames are the 22 names Windows treats as devices whatever the extension.
var reservedDeviceNames = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// windowsUnsafeName reports whether one path component is a device name (with or without an
// extension, in any case), names an alternate data stream or contains a character Windows
// does not allow, or ends in a dot or space, which Windows silently strips. It has no build
// tag so that its table test runs on every platform; only Windows applies it
// (checkPlatformName in root_windows.go), because the same names are legal elsewhere.
func windowsUnsafeName(c string) bool {
	base := strings.ToUpper(strings.SplitN(c, ".", 2)[0])
	return reservedDeviceNames[base] || strings.ContainsAny(c, `:*?"<>|`) || strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ")
}

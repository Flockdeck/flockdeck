package main

// On Windows the program's icon and name - what Explorer, the Start menu
// shortcut and Task Manager show - are resources linked in from the
// rsrc_windows_*.syso files beside this one, which go build picks up by their
// names. Without them it was the blank icon of a program that has none. This
// makes them again from winres/winres.json and the icon's PNGs after either
// changes; there is deliberately no manifest in them, since one declaring DPI
// awareness would change the pixels the window is sized in (appwindow.workArea).
//
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --arch amd64,arm64

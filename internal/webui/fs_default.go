//go:build !next

package webui

import "io/fs"

// Variant names the front end this binary carries: "default" here, "next" in a
// build made with -tags next.
const Variant = "default"

// FS returns the front end's files rooted at the assets directory. In the
// default build that is the old front end and nothing else: no file of the new
// shell is compiled in.
func FS() fs.FS { return assets() }

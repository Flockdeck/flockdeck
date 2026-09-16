package main

import (
	_ "embed"
	"fmt"
	"net/url"
	"strings"
)

// sponsorList is the sponsors who asked to be named, as the site lists them.
// It is a plain file rather than anything fetched from GitHub, because who is
// named is their choice and the maintainer's to write down, not whatever an
// API says today; and because the site is generated from this repository
// alone.
//
//go:embed assets/sponsors.txt
var sponsorList []byte

// sponsor is one line of the site's list: a name, and the address it links
// to, if the sponsor gave one.
type sponsor struct{ Name, Link string }

// loadSponsors reads assets/sponsors.txt.
func loadSponsors() ([]sponsor, error) {
	list, err := parseSponsors(string(lf(sponsorList)))
	if err != nil {
		return nil, fmt.Errorf("assets/sponsors.txt: %w", err)
	}
	return list, nil
}

// parseSponsors reads a list of sponsors, one to a line: a name, then, if
// there is one, an https:// address. Blank lines and lines starting with #
// are skipped.
//
// A line it cannot read is refused rather than shown some other way, since
// what it shows is a person's or a company's name on the site: an address
// that is not https, or one written before the name, is an error that says
// which line to put right.
func parseSponsors(src string) ([]sponsor, error) {
	var list []sponsor
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		var s sponsor
		if last := fields[len(fields)-1]; strings.Contains(last, "://") {
			u, err := url.Parse(last)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return nil, fmt.Errorf("line %d: %q is not an https:// address; give the sponsor's https:// address, or none", i+1, last)
			}
			s.Link = last
			fields = fields[:len(fields)-1]
		}
		for _, f := range fields {
			if strings.Contains(f, "://") {
				return nil, fmt.Errorf("line %d: %q: put the name first and the address after it", i+1, line)
			}
		}
		s.Name = strings.Join(fields, " ")
		if s.Name == "" {
			return nil, fmt.Errorf("line %d: %q has an address but no name; write the name the sponsor asked for before it", i+1, line)
		}
		list = append(list, s)
	}
	return list, nil
}

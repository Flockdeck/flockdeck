package help

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Node is one piece of a rendered help page as the new interface draws it. The
// interface builds elements from these and never parses markup, so a page can
// only contain what this file lets through: a short list of tags, one
// attribute (a link's address), and text. Anything else in the rendered page is
// dropped here, on the server.
type Node struct {
	// T is the tag, or "" for a run of text.
	T string `json:"t,omitempty"`
	// X is the text of a text node.
	X string `json:"x,omitempty"`
	// H is a link's address: "#slug" for another page, or an http(s) address.
	H string `json:"h,omitempty"`
	// C are the children of an element.
	C []Node `json:"c,omitempty"`
}

// allowedTags are the tags a page may use. A tag not here is not drawn; its
// children are, so the words survive.
var allowedTags = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"ul": true, "ol": true, "li": true, "a": true, "code": true, "pre": true,
	"em": true, "strong": true, "kbd": true, "del": true, "blockquote": true,
	"table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true,
	"br": true, "hr": true,
}

// AllowedTags are the tags a page may use, sorted. The new interface keeps the
// same list and checks it again before it draws, and a test holds the two
// together.
func AllowedTags() []string {
	out := make([]string, 0, len(allowedTags))
	for t := range allowedTags {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// maxTreeDepth stops a page nested without end from being walked without end.
const maxTreeDepth = 40

// treeOf turns a page's rendered HTML into nodes. slugs are the pages a "#slug"
// link may name.
func treeOf(htmlText string, slugs map[string]bool) ([]Node, error) {
	nodes, err := html.ParseFragment(strings.NewReader(htmlText), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var out []Node
	for _, n := range nodes {
		out = append(out, convert(n, slugs, 0)...)
	}
	return out, nil
}

func convert(n *html.Node, slugs map[string]bool, depth int) []Node {
	switch n.Type {
	case html.TextNode:
		if n.Data == "" {
			return nil
		}
		return []Node{{X: n.Data}}
	case html.ElementNode:
	default:
		return nil
	}
	var kids []Node
	if depth < maxTreeDepth {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			kids = append(kids, convert(c, slugs, depth+1)...)
		}
	}
	if !allowedTags[n.Data] {
		return kids
	}
	out := Node{T: n.Data, C: kids}
	if n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key == "href" && linkOK(a.Val, slugs) {
				out.H = a.Val
			}
		}
		// A link with no address that may be followed is its words.
		if out.H == "" {
			return kids
		}
	}
	return []Node{out}
}

// linkOK is whether a page may link to dest: another page of the help, or the
// web. Nothing else (javascript:, data:, a relative path) is a link.
func linkOK(dest string, slugs map[string]bool) bool {
	if s, ok := strings.CutPrefix(dest, "#"); ok {
		return slugs[s]
	}
	return strings.HasPrefix(dest, "https://") || strings.HasPrefix(dest, "http://")
}

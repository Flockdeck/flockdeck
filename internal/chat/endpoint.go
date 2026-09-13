package chat

// EndpointStore is asked for the address an agent talks to now -- its
// api.baseURL in the catalog, agents.json included -- where the one the chat
// started with could not be reached. It is a variable so that the catalog can
// supply it without the chat client depending on it; "" is the vendor's own.
var EndpointStore func(agent string) string

// readdress looks for the agent's address again and, where it has been
// changed since the chat started, builds the wire again for the new one. It
// reports whether it did.
//
// It is what makes the advice given when an address cannot be reached --
// change it with `flockdeck keys endpoint`, then /retry -- true: a running
// pane otherwise kept the address it started with, and asked it again.
func (s *session) readdress() bool {
	if s.opts.wire != nil || EndpointStore == nil || s.opts.Agent == "" {
		return false
	}
	base := EndpointStore(s.opts.Agent)
	if base == s.opts.BaseURL {
		return false
	}
	wire, err := NewWire(s.opts.Wire, base, s.key)
	if err != nil {
		return false
	}
	s.opts.BaseURL, s.wire = base, wire
	if s.reporter != nil {
		s.reporter.unpriced = base != ""
	}
	return true
}

package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file is /model: the models there are to choose from, one named by part
// of its name, and asking the endpoint which it has.

// chooseModel is /model. With nothing after it, it lists the models there are
// to choose from and which one is answering, because switching is otherwise a
// matter of already knowing the exact id; with a number, it takes the model at
// that place in the list; with anything else, it takes that as the id.
//
// Where the catalog lists no models, the endpoint is asked which it offers: an
// agent of the user's own, or a local model server, is otherwise one whose
// models have to be named by an exact tag nobody remembers.
func (s *session) chooseModel(ctx context.Context, arg string) {
	if arg == "" {
		s.out.line(ansiDim, "answering with "+firstNonEmpty(s.model, "whatever the endpoint is set to"))
		if len(s.opts.Models) == 0 {
			s.listModels(ctx)
		}
		if len(s.choices()) == 0 {
			s.out.line(ansiDim, "switch with /model <id>")
			return
		}
		hidden := 0
		for i, m := range s.choices() {
			// A gateway lists hundreds, which would scroll away the prompt and
			// the model answering; the rest are a number or a name away.
			if i >= maxListedModels && m.ID != s.model {
				hidden++
				continue
			}
			mark := " "
			if m.ID == s.model {
				mark = "*"
			}
			label := m.ID
			if m.Name != "" && m.Name != m.ID {
				label += "  " + m.Name
			}
			if m.Note != "" {
				label += " — " + m.Note
			}
			s.out.line(ansiDim, fmt.Sprintf("  %s %d  %s", mark, i+1, label))
		}
		if hidden > 0 {
			s.out.line(ansiDim, fmt.Sprintf("  (and %d more; /model <part of a name> lists the ones that match)", hidden))
		}
		s.out.line(ansiDim, "switch with /model <number>, or /model <id> for any other")
		return
	}
	if len(s.choices()) == 0 {
		// Nothing has been listed yet, so there is nothing to match a part of
		// a name or a number against: "/model qwen" was taken as the id
		// "qwen", which the endpoint does not have. It is asked first, as a
		// bare /model would ask it.
		s.listModels(ctx)
	}
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(s.choices()) {
			s.out.line(ansiRed, fmt.Sprintf("there is no model %d in the list; /model shows it", n))
			return
		}
		arg = s.choices()[n-1].ID
	} else if m, ok := s.matchModel(arg); ok {
		arg = m
	} else {
		return
	}
	s.model = arg
	s.out.line(ansiDim, "answering with "+arg+" from here on")
}

// matchModel is the listed model somebody named by less than its exact id --
// "opus", "Sonnet 5", "flash" -- since the ids are long and nobody remembers
// the date on the end of one. An exact id is taken as it is, and so is a name
// no listed model matches, which is how a model the list leaves out is named.
// A name several listed models match switches to none of them: it lists them
// instead, and reports false.
func (s *session) matchModel(arg string) (string, bool) {
	want := strings.ToLower(arg)
	var named, containing []ModelChoice
	for _, m := range s.choices() {
		id, name := strings.ToLower(m.ID), strings.ToLower(m.Name)
		switch {
		case id == want:
			return m.ID, true
		case name == want:
			named = append(named, m)
		case strings.Contains(id, want) || strings.Contains(name, want):
			containing = append(containing, m)
		}
	}
	if len(named) == 1 {
		return named[0].ID, true
	}
	found := append(named, containing...)
	switch len(found) {
	case 0:
		return arg, true
	case 1:
		return found[0].ID, true
	}
	s.out.line(ansiDim, "more than one model goes by "+arg+":")
	listed := 0
	for i, m := range s.choices() {
		for _, f := range found {
			if f.ID == m.ID && listed < maxListedModels {
				s.out.line(ansiDim, fmt.Sprintf("    %d  %s  %s", i+1, m.ID, m.Name))
				listed++
			}
		}
	}
	if more := len(found) - listed; more > 0 {
		s.out.line(ansiDim, fmt.Sprintf("    (and %d more; more of the name narrows it)", more))
	}
	s.out.line(ansiDim, "switch with /model <number>")
	return "", false
}

// maxListedModels is as many models as /model lists at once: a screenful,
// where an endpoint can offer hundreds.
const maxListedModels = 40

// choices are the models /model offers: the catalog's, or failing those, the
// ones the endpoint said it has.
func (s *session) choices() []ModelChoice {
	if len(s.opts.Models) > 0 {
		return s.opts.Models
	}
	return s.listed
}

// pickOnlyModel is for a chat started with no model named. Where the catalog
// lists none and a server on this machine offers exactly one -- a llama.cpp
// server, LM Studio with one model loaded -- that one answers: there is
// nothing to choose, and making somebody type /model to choose it is a step
// for nothing. Otherwise it says how to choose.
//
// Only a server on this machine is asked, so that starting a chat never
// sends a request to a vendor before anybody has asked it anything.
func (s *session) pickOnlyModel(ctx context.Context) {
	if lister, ok := s.wire.(modelLister); ok && len(s.opts.Models) == 0 && isLoopback(s.opts.BaseURL) {
		// Briefly: this is before the first prompt, and an endpoint that is
		// not answering will say so soon enough when it is asked something.
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if ids, err := lister.ListModels(ctx); err == nil {
			// Kept whether or not there is only one, so that /model 2 or
			// /model qwen has them to choose from without asking again.
			s.listed = s.listed[:0]
			for _, id := range ids {
				s.listed = append(s.listed, ModelChoice{ID: id})
			}
			if len(ids) == 1 {
				s.model = ids[0]
				s.out.line(ansiDim, "(answering with "+ids[0]+", the one model the endpoint offers)")
				return
			}
		}
	}
	s.out.line(ansiDim, "no model is named for this agent; /model shows the ones to choose from")
}

// localModels are the models a server on this machine offers, where there are
// few enough to name in a line -- listed now if they have not been -- and nil
// for any other endpoint, which a failure is no reason to send a request to.
func (s *session) localModels() []string {
	lister, ok := s.wire.(modelLister)
	if !ok || !isLoopback(s.opts.BaseURL) {
		return nil
	}
	if len(s.listed) == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ids, err := lister.ListModels(ctx)
		if err != nil {
			return nil
		}
		for _, id := range ids {
			s.listed = append(s.listed, ModelChoice{ID: id})
		}
	}
	if len(s.listed) > 8 {
		return nil
	}
	var names []string
	for _, m := range s.listed {
		names = append(names, m.ID)
	}
	return names
}

// listModels asks the endpoint which models it offers, for a moment, and keeps
// the answer for /model to choose from.
func (s *session) listModels(ctx context.Context) {
	lister, ok := s.wire.(modelLister)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ids, err := lister.ListModels(ctx)
	if err != nil && unreachable(err) {
		// Said the way a failed answer says it: the dial error's own words
		// are three lines about sockets, and the likely cause is one.
		if isLoopback(s.opts.BaseURL) {
			s.out.line(ansiDim, "(nothing is answering at "+s.opts.BaseURL+" to say which models it has; is the model server running?)")
		} else {
			s.out.line(ansiDim, "(could not reach "+endpointOf(s.opts)+" to ask which models it has; check the connection, or the address)")
		}
		return
	}
	if err != nil {
		s.out.line(ansiDim, "(the endpoint could not say which models it has: "+err.Error()+")")
		return
	}
	s.listed = s.listed[:0]
	for _, id := range ids {
		s.listed = append(s.listed, ModelChoice{ID: id})
	}
}

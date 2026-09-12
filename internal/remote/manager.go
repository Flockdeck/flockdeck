package remote

import (
	"context"
	"errors"
	"net"
	"sync"
)

// ErrNotEnabled is what anything that needs an enrolment is told when there is
// none.
var ErrNotEnabled = errors.New("remote access is not enabled on this machine; turn it on from Remote access… in the command palette, or with `flockdeck remote enable`")

// Manager is remote access as a running instance has it: the enrolment on
// disk, and the tunnel that goes with it.
//
// The enrolment is written by `flockdeck remote`, from another process that
// then tells the instance to look again, or by Enable and Disable here on the
// window's behalf. Reload is that looking: it brings the tunnel in line with
// whatever the file now says — opened, reopened to a different relay, or
// closed.
type Manager struct {
	version string
	serve   func(net.Listener) error
	changed func()
	// load reads the enrolment. It is a field so a test can hand one over
	// without a state directory.
	load func() (*Config, error)

	// reloading keeps two reloads from interleaving their stop and start.
	reloading sync.Mutex
	// enrolling keeps an Enable or Disable from running beside another: two
	// at once would each find no enrolment and each register a host, and
	// the first would be left on the relay with nothing here to speak for it.
	enrolling sync.Mutex

	mu   sync.Mutex
	cfg  *Config
	conn *Connector
}

// NewManager makes a manager with nothing running. serve answers each tunnel's
// connections; changed is told whenever what the window should show moves.
func NewManager(version string, serve func(net.Listener) error, changed func()) *Manager {
	return &Manager{version: version, serve: serve, changed: changed, load: Load}
}

// Reload reads the enrolment again and makes the tunnel match it.
//
// A tunnel that already matches is left alone, so reloading costs a working
// connection nothing. One that has given up — revoked, or displaced by another
// instance — is started again, because being asked to reload is somebody
// saying that whatever stopped it has been dealt with.
//
// A file that cannot be read is reported and the tunnel left as it was: there
// is no telling what the file meant, and dropping every remote window over a
// half-written file is the worse guess.
func (m *Manager) Reload() error {
	m.reloading.Lock()
	defer m.reloading.Unlock()

	cfg, err := m.load()
	if err != nil {
		return err
	}

	m.mu.Lock()
	old := m.conn
	if cfg != nil && m.cfg != nil && *cfg == *m.cfg && old != nil {
		switch old.Status().State {
		case StateConnecting, StateConnected, StateError:
			m.mu.Unlock()
			return nil
		}
	}
	var next *Connector
	if cfg != nil {
		next = NewConnector(*cfg, m.version, m.serve, m.changed)
	}
	m.cfg, m.conn = cfg, next
	m.mu.Unlock()

	if old != nil {
		old.Stop()
	}
	if next != nil {
		next.Start()
	}
	if m.changed != nil {
		m.changed()
	}
	return nil
}

// Status reports the tunnel's state, and whether this machine is enrolled at
// all.
func (m *Manager) Status() (Status, bool) {
	m.mu.Lock()
	c := m.conn
	m.mu.Unlock()
	if c == nil {
		return Status{}, false
	}
	return c.Status(), true
}

// Client is a client for the relay this machine is enrolled with.
func (m *Manager) Client() (*Client, error) {
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()
	if cfg == nil {
		return nil, ErrNotEnabled
	}
	return NewClient(cfg, m.version), nil
}

// Enable enrols this machine with a relay and brings the tunnel up to it:
// what `flockdeck remote enable` does, for the window.
func (m *Manager) Enable(ctx context.Context, req EnableRequest) (replaced bool, err error) {
	m.enrolling.Lock()
	defer m.enrolling.Unlock()
	if _, replaced, err = Enable(ctx, m.version, req); err != nil {
		return false, err
	}
	return replaced, m.Reload()
}

// Disable takes this machine off its relay and closes the tunnel: what
// `flockdeck remote disable` does, for the window. untold is why the relay
// could not be told, when force had the enrolment forgotten regardless.
func (m *Manager) Disable(ctx context.Context, force bool) (untold error, err error) {
	m.enrolling.Lock()
	defer m.enrolling.Unlock()
	// An enrolment that cannot be read is refused before the tunnel is
	// touched: Reload could not open it again afterwards, for the same reason.
	if _, err := Load(); err != nil && !force {
		return nil, err
	}
	// The relay closes the tunnel as revoked the moment it is told, and the
	// window would show that, the relay no longer accepting this machine,
	// until Reload caught up. So the tunnel is closed first; if the relay
	// cannot be told after all, the enrolment stands and Reload reopens it.
	m.reloading.Lock()
	m.mu.Lock()
	c := m.conn
	m.mu.Unlock()
	if c != nil {
		c.Stop()
	}
	m.reloading.Unlock()
	_, untold, err = Disable(ctx, m.version, force)
	rerr := m.Reload()
	if err != nil {
		return nil, err
	}
	return untold, rerr
}

// Close closes the tunnel, and every remote window with it.
func (m *Manager) Close() {
	m.reloading.Lock()
	defer m.reloading.Unlock()
	m.mu.Lock()
	c := m.conn
	m.conn = nil
	m.mu.Unlock()
	if c != nil {
		c.Stop()
	}
}

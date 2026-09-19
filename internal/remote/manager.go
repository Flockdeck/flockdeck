package remote

import (
	"context"
	"crypto/ecdh"
	"errors"
	"net"
	"sync"
	"time"
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
	// closed is set by Close, under reloading, and keeps a Reload that comes
	// after it -- a rename or a `flockdeck remote` finishing while the
	// instance shuts down -- from starting a tunnel nothing would serve.
	closed bool

	// e2eMu guards this machine's end-to-end identity and what it has
	// learned of the account's devices' keys (e2ekey.go), separately from mu
	// above so that a slow relay call for either never holds up Status,
	// Client or Reload's own bookkeeping.
	e2eMu          sync.Mutex
	e2ePriv        *ecdh.PrivateKey
	e2eRosterCache map[string]Device
	e2eRosterAt    time.Time
	e2eRegErr      error
	e2eRegAt       time.Time

	// e2eCancel and e2eWG are Reload's background EnsureE2EKey call: cancel
	// aborts whichever relay round trip is in flight, and the group is what
	// Close waits on, so nothing is still touching this machine's identity
	// file, or the account's relay, once Close has returned.
	e2eCancel context.CancelFunc
	e2eWG     sync.WaitGroup
}

// NewManager makes a manager with nothing running. serve answers each tunnel's
// connections; changed is told whenever what the window should show moves.
func NewManager(version string, serve func(net.Listener) error, changed func()) *Manager {
	return &Manager{version: version, serve: serve, changed: changed, load: Load}
}

// Reload reads the enrolment again and makes the tunnel match it.
//
// A tunnel that already matches is left alone, so reloading costs a working
// connection nothing, and so does a new name, which the relay keeps rather
// than the tunnel: renaming this machine must not drop every remote window.
// One that has given up — revoked, or displaced by another instance — is
// started again, because being asked to reload is somebody saying that
// whatever stopped it has been dealt with.
//
// A file that cannot be read is reported and the tunnel left as it was: there
// is no telling what the file meant, and dropping every remote window over a
// half-written file is the worse guess.
func (m *Manager) Reload() error {
	m.reloading.Lock()
	defer m.reloading.Unlock()
	if m.closed {
		return nil
	}

	cfg, err := m.load()
	if err != nil {
		return err
	}
	if cfg != nil {
		// Best-effort, and never blocks bringing the tunnel up: a relay that
		// cannot be reached right now for this leaves terminals unencrypted
		// until the next Reload -- an app restart, or the dialog's "try
		// again" -- succeeds, rather than holding remote access itself up on
		// a key nothing needs to serve a request through the tunnel.
		//
		// Tracked rather than left to run loose: Close cancels it and waits
		// for it, so quitting -- or a test's cleanup removing this
		// machine's state directory -- never races this writing the identity
		// file or reaching the relay after Close has returned.
		ctx, cancel := context.WithTimeout(context.Background(), e2eRegisterTimeout)
		m.mu.Lock()
		if m.e2eCancel != nil {
			m.e2eCancel() // superseded by this reload's own attempt
		}
		m.e2eCancel = cancel
		m.mu.Unlock()
		m.e2eWG.Add(1)
		go func() {
			defer m.e2eWG.Done()
			defer cancel()
			_ = m.EnsureE2EKey(ctx)
		}()
	}

	m.mu.Lock()
	old := m.conn
	if cfg != nil && m.cfg != nil && sameTunnel(*cfg, *m.cfg) && old != nil {
		switch old.Status().State {
		case StateConnecting, StateConnected, StateError:
			renamed := cfg.Name != m.cfg.Name
			m.cfg = cfg
			m.mu.Unlock()
			if renamed {
				old.setName(cfg.Name)
				if m.changed != nil {
					m.changed()
				}
			}
			return nil
		}
	}
	var next *Connector
	if cfg != nil {
		next = NewConnector(*cfg, m.version, m.serve, m.changed)
	}
	m.cfg, m.conn = cfg, next
	m.mu.Unlock()

	// The new tunnel is started before the old is stopped. Stopping the old
	// tells the window to look, and what it reads by then is the new one,
	// which should say connecting rather than, unstarted, not connected.
	// Two at once is harmless: to different relays they do not meet, and to
	// the same one the relay keeps the newer and drops the one going anyway.
	if next != nil {
		next.Start()
	}
	if old != nil {
		old.Stop()
	}
	if m.changed != nil {
		m.changed()
	}
	return nil
}

// sameTunnel reports whether two enrolments are one tunnel: the same relay,
// the same machine and the same token, whatever each calls the machine.
func sameTunnel(a, b Config) bool {
	a.Name, b.Name = "", ""
	return a == b
}

// Rename renames this machine on its relay and saves the name: what
// `flockdeck remote rename` does, for the window. The tunnel carries on as it
// was, and the window is told the new name.
func (m *Manager) Rename(ctx context.Context, name string) error {
	m.enrolling.Lock()
	defer m.enrolling.Unlock()
	if _, err := Rename(ctx, m.version, name); err != nil {
		return err
	}
	return m.Reload()
}

// Reconnect tries the relay again now, for the window's "try again": a
// tunnel waiting out a failure skips the rest of the wait, and one that has
// given up, revoked or stepped aside for another instance, is started again,
// as Reload does. A connected tunnel is left alone.
func (m *Manager) Reconnect() error {
	m.mu.Lock()
	c := m.conn
	m.mu.Unlock()
	if c == nil {
		return ErrNotEnabled
	}
	switch c.Status().State {
	case StateConnected:
		return nil
	case StateConnecting, StateError:
		c.RetryNow()
		return nil
	}
	return m.Reload()
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

// Move enrols this machine with another relay and leaves the one it is on,
// once the new one answers: what `flockdeck remote move` does, for the window.
// The tunnel is moved across as soon as the new enrolment is saved, before
// the old relay is told, so the window shows it connecting to the new relay
// rather than cut off by the old. untold is why the old relay could not be
// told, when the move went ahead regardless.
func (m *Manager) Move(ctx context.Context, req EnableRequest) (untold error, err error) {
	m.enrolling.Lock()
	defer m.enrolling.Unlock()
	var rerr error
	_, untold, err = Move(ctx, m.version, req, func() { rerr = m.Reload() })
	if err != nil {
		return nil, err
	}
	return untold, rerr
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

// Close closes the tunnel, and every remote window with it. It also cancels
// and waits for Reload's background EnsureE2EKey call, if one is in flight,
// so that nothing this instance started is still running once Close has
// returned.
func (m *Manager) Close() {
	m.reloading.Lock()
	defer m.reloading.Unlock()
	m.closed = true
	m.mu.Lock()
	c := m.conn
	m.conn = nil
	cancelE2E := m.e2eCancel
	m.mu.Unlock()
	if c != nil {
		c.Stop()
	}
	if cancelE2E != nil {
		cancelE2E()
	}
	m.e2eWG.Wait()
}

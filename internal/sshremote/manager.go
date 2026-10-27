package sshremote

import (
	"context"
	"errors"
	"sync"
)

// Desktop owns only this process's SSH connections. It starts no work until
// the user connects, and disposing the plugin cancels pending connects too.
var Desktop Manager

type Manager struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	byID   map[string]*Connection
}

func (m *Manager) Enable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil {
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.byID = map[string]*Connection{}
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	old := m.byID
	m.byID = nil
	m.ctx = nil
	m.cancel = nil
	m.mu.Unlock()
	for _, c := range old {
		c.Close()
	}
}
func (m *Manager) Connect(ctx context.Context, h Host) (*Connection, error) {
	m.mu.Lock()
	life := m.ctx
	m.mu.Unlock()
	if life == nil {
		return nil, errors.New("SSH plugin is off")
	}
	call, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(life, cancel)
	defer stop()
	c, err := Connect(call, h)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx != life || life.Err() != nil {
		c.Close()
		return nil, errors.New("SSH plugin is off")
	}
	m.byID[c.ID] = c
	return c, nil
}
func (m *Manager) Get(id string) (*Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.byID[id]
	if c == nil {
		return nil, errors.New("SSH connection expired; reconnect")
	}
	return c, nil
}
func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	c := m.byID[id]
	delete(m.byID, id)
	m.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

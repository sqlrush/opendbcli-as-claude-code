/*-------------------------------------------------------------------------
 *
 * manager.go
 *	  DriverFactory creates a db.Driver from a ConnectionConfig. This
 *	  allows injecting mock drivers in tests.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/connection/manager.go
 *
 *-------------------------------------------------------------------------
 */
package connection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/credential"
	"github.com/sqlrush/opendb/internal/db"
	dberrors "github.com/sqlrush/opendb/internal/errors"
)

const maxRecentConnections = 10

// DriverFactory creates a db.Driver from a ConnectionConfig.
// This allows injecting mock drivers in tests.
type DriverFactory func(cfg db.ConnectionConfig) (db.Driver, error)

// Manager manages database connections, credential resolution, and
// connection history.
type Manager struct {
	mu              sync.RWMutex
	driver          db.Driver
	currentConfig   *db.ConnectionConfig
	currentName     string
	groups          []config.ConnectionGroup
	providers       map[string]credential.Provider
	recentNames     []string
	driverFactory   DriverFactory            // legacy single factory (still works)
	driverFactories map[string]DriverFactory // per-DBType factories
	connectionsDir  string
}

// Option configures a Manager.
type Option func(*Manager)

// WithDriverFactory sets a driver factory (legacy single-factory mode).
func WithDriverFactory(f DriverFactory) Option {
	return func(m *Manager) {
		m.driverFactory = f
	}
}

// WithDriverFactoryFor registers a driver factory for a specific database type.
func WithDriverFactoryFor(dbType string, f DriverFactory) Option {
	return func(m *Manager) {
		if m.driverFactories == nil {
			m.driverFactories = make(map[string]DriverFactory)
		}
		m.driverFactories[dbType] = f
	}
}

// WithProvider registers a credential provider under the given name.
func WithProvider(name string, p credential.Provider) Option {
	return func(m *Manager) {
		m.providers[name] = p
	}
}

// NewManager creates a Manager from the given config. It loads connection
// groups from the configured connections directory. If the directory does not
// exist, the manager starts with an empty connection list.
func NewManager(cfg *config.Config, opts ...Option) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config must not be nil")
	}

	credDir := filepath.Join(filepath.Dir(cfg.ConnectionsDir), "credentials")

	m := &Manager{
		groups:         make([]config.ConnectionGroup, 0),
		providers:      make(map[string]credential.Provider),
		recentNames:    make([]string, 0, maxRecentConnections),
		connectionsDir: cfg.ConnectionsDir,
	}

	// Register all credential providers.
	m.providers["prompt"] = credential.NewPromptProvider()
	encProvider := credential.NewEncryptedProvider(credDir)
	m.providers["save"] = encProvider
	m.providers["encrypted"] = encProvider // alias
	m.providers["wallet"] = credential.NewWalletProvider()
	m.providers["os"] = credential.NewOSAuthProvider()
	m.providers["kerberos"] = credential.NewKerberosProvider()

	for _, opt := range opts {
		opt(m)
	}

	// Priority: inline connections in config.yaml > connections directory
	if len(cfg.Connections) > 0 {
		m.groups = []config.ConnectionGroup{
			{Group: "config", Connections: cfg.Connections},
		}
	} else {
		groups, err := config.LoadConnectionGroups(cfg.ConnectionsDir)
		if err != nil {
			m.groups = make([]config.ConnectionGroup, 0)
		} else {
			m.groups = groups
		}
	}

	return m, nil
}

// Connect finds a connection by name, resolves credentials, creates a driver,
// and stores it as the current connection.
func (m *Manager) Connect(ctx context.Context, connName string) error {
	return m.ConnectWithPassword(ctx, connName, "")
}

// ConnectWithPassword connects using an explicit password. If password is empty,
// credentials are resolved via the configured provider.
func (m *Manager) ConnectWithPassword(ctx context.Context, connName string, password string) error {
	return m.ConnectWithOverride(ctx, connName, "", password)
}

// ConnectWithOverride connects with optional user and password overrides.
// Empty user falls back to the configured user; empty password is resolved via provider.
func (m *Manager) ConnectWithOverride(ctx context.Context, connName, user, password string) error {
	conn, err := m.findConnection(connName)
	if err != nil {
		return err
	}

	if password == "" {
		password, err = m.resolveCredential(conn)
		if err != nil {
			return fmt.Errorf("resolving credentials for %q: %w", connName, err)
		}
	}

	dbUser := conn.User
	if user != "" {
		dbUser = user
	}

	service := conn.Service
	if service == "" {
		service = conn.SID
	}

	dbType := conn.DBType
	if dbType == "" {
		dbType = "oracle" // backward compatible default
	}

	connCfg := db.ConnectionConfig{
		DBType:    dbType,
		Host:      conn.Host,
		Port:      conn.Port,
		Service:   service,
		Database:  conn.Database,
		User:      dbUser,
		Password:  password,
		Privilege: conn.Privilege,
		Options:   make(map[string]string),
	}

	// Apply auth-mode-specific options.
	applyAuthModeOptions(&connCfg, conn)

	driver, err := m.createDriver(connCfg)
	if err != nil {
		return fmt.Errorf("connecting to %q: %w", connName, err)
	}

	// Close existing connection if any.
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.driver != nil {
		_ = m.driver.Close()
	}

	m.driver = driver
	m.currentConfig = &connCfg
	m.currentName = connName
	m.addRecent(connName)

	return nil
}

// ConnectDirect connects with an explicit ConnectionConfig, bypassing saved
// connection lookup. Used for one-time connections from /conn.
func (m *Manager) ConnectDirect(ctx context.Context, cfg db.ConnectionConfig, displayName string) error {
	driver, err := m.createDriver(cfg)
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.driver != nil {
		_ = m.driver.Close()
	}

	m.driver = driver
	m.currentConfig = &cfg
	m.currentName = displayName
	// Don't add to recent — it's a one-time connection.
	return nil
}

// ConnectionNames returns all connection names, with recent connections first,
// then remaining sorted alphabetically.
// Only returns connections whose DBType is supported by a registered driver factory.
func (m *Manager) ConnectionNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Collect all names (filtered to supported DBTypes).
	nameSet := make(map[string]bool)
	for _, g := range m.groups {
		for _, c := range g.Connections {
			if m.isSupportedDBType(c.DBType) {
				nameSet[c.Name] = true
			}
		}
	}

	// Build result: recent first, then remaining sorted.
	seen := make(map[string]bool)
	result := make([]string, 0, len(nameSet))

	for _, name := range m.recentNames {
		if nameSet[name] {
			result = append(result, name)
			seen[name] = true
		}
	}

	remaining := make([]string, 0, len(nameSet)-len(seen))
	for name := range nameSet {
		if !seen[name] {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)
	result = append(result, remaining...)

	return result
}

// FindConnection searches all groups for a connection with the given name.
// This is the public version of findConnection.
func (m *Manager) FindConnection(name string) (config.Connection, bool) {
	conn, err := m.findConnection(name)
	if err != nil {
		return config.Connection{}, false
	}
	return conn, true
}

// SaveConnection persists a connection to the connections directory.
// If groupName matches an existing group file, the connection is appended.
// Otherwise, a new group file is created.
func (m *Manager) SaveConnection(conn config.Connection, groupName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if groupName == "" {
		groupName = "default"
	}

	// Try to find existing group and append.
	for i, g := range m.groups {
		if g.Group == groupName {
			updated := config.ConnectionGroup{
				Group:       g.Group,
				Tags:        g.Tags,
				Connections: append(g.Connections, conn),
			}
			m.groups[i] = updated
			return m.writeGroup(updated)
		}
	}

	// Create new group.
	newGroup := config.ConnectionGroup{
		Group:       groupName,
		Connections: []config.Connection{conn},
	}
	m.groups = append(m.groups, newGroup)
	return m.writeGroup(newGroup)
}

// GetProvider returns the credential provider registered under the given name.
func (m *Manager) GetProvider(name string) (credential.Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[name]
	return p, ok
}

// PromptPassword prompts the user for a password using the prompt provider.
func (m *Manager) PromptPassword(connName string) (string, error) {
	m.mu.RLock()
	provider, ok := m.providers["prompt"]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("prompt provider not available")
	}
	return provider.Resolve(connName)
}

// ReloadGroups reloads connection groups from disk.
func (m *Manager) ReloadGroups() error {
	groups, err := config.LoadConnectionGroups(m.connectionsDir)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.groups = groups
	return nil
}

// writeGroup writes a connection group to its YAML file.
func (m *Manager) writeGroup(group config.ConnectionGroup) error {
	data, err := yaml.Marshal(group)
	if err != nil {
		return fmt.Errorf("marshaling group %q: %w", group.Group, err)
	}
	path := filepath.Join(m.connectionsDir, group.Group+".yaml")
	return os.WriteFile(path, data, 0600)
}

// applyAuthModeOptions sets driver-specific options based on the connection's auth mode.
func applyAuthModeOptions(cfg *db.ConnectionConfig, conn config.Connection) {
	switch conn.AuthMode {
	case "os":
		cfg.Options["AUTH TYPE"] = "OS"
		cfg.User = ""
		cfg.Password = ""
	case "wallet":
		if conn.Credential.WalletDir != "" {
			cfg.Options["WALLET"] = conn.Credential.WalletDir
		}
	case "kerberos":
		cfg.Options["AUTH TYPE"] = "KERBEROS"
	}
}

// Disconnect closes the current driver connection.
func (m *Manager) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.driver == nil {
		return nil
	}

	err := m.driver.Close()
	m.driver = nil
	m.currentConfig = nil
	m.currentName = ""
	return err
}

// Current returns the active driver or a NotConnectedError if no
// connection is established.
func (m *Manager) Current() (db.Driver, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.driver == nil {
		return nil, &dberrors.NotConnectedError{}
	}
	return m.driver, nil
}

// CurrentInfo returns the server info for the active connection, or nil if
// no connection is established.
func (m *Manager) CurrentInfo() *db.ServerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.driver == nil {
		return nil
	}
	info := m.driver.ServerInfo()
	return &info
}

// CurrentName returns the name of the active connection, or an empty string
// if no connection is established.
func (m *Manager) CurrentName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentName
}

// ListConnections returns all available connections across all groups.
// Only returns connections whose DBType is supported by a registered driver factory.
func (m *Manager) ListConnections() []config.Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []config.Connection
	for _, g := range m.groups {
		for _, c := range g.Connections {
			if m.isSupportedDBType(c.DBType) {
				all = append(all, c)
			}
		}
	}
	return all
}

// RecentConnections returns the most recently used connection names,
// ordered from most recent to least recent.
func (m *Manager) RecentConnections() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, len(m.recentNames))
	copy(result, m.recentNames)
	return result
}

// findConnection searches all groups for a connection with the given name.
func (m *Manager) findConnection(name string) (config.Connection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, g := range m.groups {
		for _, c := range g.Connections {
			if c.Name == name {
				return c, nil
			}
		}
	}
	return config.Connection{}, fmt.Errorf("connection %q not found", name)
}

// resolveCredential obtains the password for a connection using the
// appropriate credential provider.
func (m *Manager) resolveCredential(conn config.Connection) (string, error) {
	// Auth mode takes precedence over credential.provider for provider selection.
	providerName := conn.AuthMode
	if providerName == "" {
		providerName = conn.Credential.Provider
	}

	// If the credential has an inline value, use it directly.
	if providerName == "" && conn.Credential.Value != "" {
		return conn.Credential.Value, nil
	}

	// Default to prompt provider.
	if providerName == "" {
		providerName = "prompt"
	}

	// For LDAP and token, create providers with connection-specific config.
	switch providerName {
	case "ldap":
		ldapProvider := credential.NewLDAPProvider(
			conn.Credential.LDAPServer,
			conn.Credential.LDAPBaseDN,
		)
		return ldapProvider.Resolve(conn.Name)
	case "token":
		tokenProvider := credential.NewTokenProvider(
			conn.Credential.TokenURL,
			conn.Credential.TokenScope,
			conn.Credential.ClientID,
			conn.Credential.ClientSecret,
		)
		return tokenProvider.Resolve(conn.Name)
	}

	m.mu.RLock()
	provider, ok := m.providers[providerName]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("unknown credential provider %q", providerName)
	}

	return provider.Resolve(conn.Name)
}

// CurrentDBType returns the database type of the current connection, or ""
// if not connected.
func (m *Manager) CurrentDBType() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.currentConfig != nil {
		return m.currentConfig.DBType
	}
	return ""
}

// CurrentHost returns the host of the active connection, or empty if not connected.
func (m *Manager) CurrentHost() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.currentConfig != nil {
		return m.currentConfig.Host
	}
	return ""
}

// createDriver uses the driver factory to create a new driver, or returns
// an error if no factory is configured. Tries per-DBType factory first,
// then falls back to the legacy single factory.
func (m *Manager) createDriver(cfg db.ConnectionConfig) (db.Driver, error) {
	if m.driverFactories != nil && cfg.DBType != "" {
		if f, ok := m.driverFactories[cfg.DBType]; ok {
			return f(cfg)
		}
		// DBType is set but no factory registered — give a clear error.
		return nil, fmt.Errorf("当前构建不支持 %s 数据库, 请使用 opendb-%s 或 opendb (full) 版本", cfg.DBType, cfg.DBType)
	}
	if m.driverFactory != nil {
		return m.driverFactory(cfg)
	}
	return nil, fmt.Errorf("no driver factory configured for db type %q", cfg.DBType)
}

// isSupportedDBType returns true if a driver factory is registered for the given type.
// Empty DBType defaults to "oracle" (backward compatible) and is always supported
// if any factory is registered.
func (m *Manager) isSupportedDBType(dbType string) bool {
	// No factories registered at all → don't filter (test/dev mode).
	if m.driverFactories == nil && m.driverFactory == nil {
		return true
	}
	if dbType == "" {
		// Legacy oracle-only configs have no DBType field.
		dbType = "oracle"
	}
	if m.driverFactories != nil {
		if _, ok := m.driverFactories[dbType]; ok {
			return true
		}
	}
	// Legacy single factory: only supports oracle (default).
	if m.driverFactory != nil && dbType == "oracle" {
		return true
	}
	return false
}

// addRecent adds a connection name to the front of the recent list.
// Duplicates are removed and the list is capped at maxRecentConnections.
// Must be called with m.mu held.
func (m *Manager) addRecent(name string) {
	// Remove existing occurrence if present.
	filtered := make([]string, 0, len(m.recentNames))
	for _, n := range m.recentNames {
		if n != name {
			filtered = append(filtered, n)
		}
	}

	// Prepend the new name.
	updated := make([]string, 0, maxRecentConnections)
	updated = append(updated, name)
	updated = append(updated, filtered...)

	// Cap at max.
	if len(updated) > maxRecentConnections {
		updated = updated[:maxRecentConnections]
	}

	m.recentNames = updated
}

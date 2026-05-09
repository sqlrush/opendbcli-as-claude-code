/*-------------------------------------------------------------------------
 *
 * wallet.go
 *	  WalletProvider handles Oracle Wallet (auto-login) authentication.
 *	  The wallet itself handles credential resolution transparently. The
 *	  actual wallet directory is set via
 *	  ConnectionConfig.Options["WALLET"] by the connection manager.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/wallet.go
 *
 *-------------------------------------------------------------------------
 */
package credential

// WalletProvider handles Oracle Wallet (auto-login) authentication.
// The wallet itself handles credential resolution transparently.
// The actual wallet directory is set via ConnectionConfig.Options["WALLET"]
// by the connection manager.
type WalletProvider struct{}

// NewWalletProvider creates a WalletProvider.
func NewWalletProvider() *WalletProvider {
	return &WalletProvider{}
}

// Resolve returns empty credentials — Oracle Wallet handles auth transparently.
func (p *WalletProvider) Resolve(_ string) (string, error) {
	return "", nil
}

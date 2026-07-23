package relay

import "time"

// SetProxyHeaderTimeout shortens the PROXY header-detection wait for tests
// (the headerless-connection path otherwise sleeps the full production
// timeout). Returns a restore func.
func SetProxyHeaderTimeout(d time.Duration) (restore func()) {
	old := proxyHeaderTimeout
	proxyHeaderTimeout = d
	return func() { proxyHeaderTimeout = old }
}

package selftest

// VerifyHMACForTest exposes the unexported verifyHMAC for the external test
// package (Go's sanctioned export_test.go hook).
var VerifyHMACForTest = verifyHMAC

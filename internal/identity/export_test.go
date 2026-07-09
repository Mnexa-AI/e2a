package identity

// SetExpiredDeleteBatchForTest overrides the DeleteExpiredMessages batch size for
// the duration of a test and returns a restore func. Lets the external identity_test
// package exercise the multi-batch drain loop cheaply (a few rows, tiny batch) instead
// of seeding >5000 rows. Compiled only under test.
func SetExpiredDeleteBatchForTest(n int64) (restore func()) {
	prev := expiredDeleteBatch
	expiredDeleteBatch = n
	return func() { expiredDeleteBatch = prev }
}

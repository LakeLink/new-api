package constant

import "sync/atomic"

// Setup is process-local state derived from the database setup marker. It is
// atomic because initial setup requests can arrive concurrently.
var Setup atomic.Bool

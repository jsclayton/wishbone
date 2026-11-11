package imagestore

// SetDecodeHook makes every decode call f while it holds a slot. It returns a
// function that removes the hook. Tests that use it must not run in parallel.
func SetDecodeHook(f func()) (restore func()) {
	decodeHook = f
	return func() { decodeHook = nil }
}

// Package unrelated does not import the upstream module, so the
// auto-narrow step should exclude it from the test command.
package unrelated

func Two() int {
	return 2
}

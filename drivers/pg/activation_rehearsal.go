//go:build dawgs_rehearsal

package pg

// activationReady is enabled only in explicitly tagged disposable rehearsal
// binaries. The symbol is unexported and this source is absent from normal
// release builds.
func activationReady() bool { return true }

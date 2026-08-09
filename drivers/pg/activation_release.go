//go:build !dawgs_rehearsal

package pg

// activationReady remains false in every normal build until the parent
// activation review changes the production contract deliberately.
func activationReady() bool { return false }

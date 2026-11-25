//go:build !windows

package serialport

func (p *Port) reconfigure(next, previous parameters) error {
	// On Unix, Reconfigure reasserts the raw termios configuration set by
	// Open without changing modem outputs, descriptor flags or timeouts.
	// It updates its option cache before applying termios, so reapply the old
	// options on error even if the driver rejected the change outright.
	apply := func(mode parameters) error {
		options := mode.options()
		return p.conn.Reconfigure(options[:]...)
	}
	return applyWithRestore(func() error { return apply(next) }, func() error { return apply(previous) })
}

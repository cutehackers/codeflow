package protocol

// modelHostProcessGroupSignal records the two termination targets used by the
// Unix process-group implementation. Keeping the group and direct-child
// results separate lets normal lifecycle cleanup and pre-host cleanup preserve
// their historical EPERM handling after the group is verified post-reap.
type modelHostProcessGroupSignal struct {
	groupKillErr   error
	processKillErr error
}

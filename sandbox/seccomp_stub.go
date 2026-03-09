//go:build !linux
// +build !linux

package sandbox

// Action defines what to do when a rule matches
type Action int

const (
	ActionAllow Action = iota
	ActionKill
	ActionErrno
	ActionLog
)

// Rule defines a syscall rule
type Rule struct {
	Syscall int
	Action  Action
}

// SeccompFilter is a stub for non-Linux platforms
type SeccompFilter struct {
	DefaultAction Action
	Rules         []Rule
}

// DefaultAllowlistFilter returns a stub filter
func DefaultAllowlistFilter() *SeccompFilter {
	return &SeccompFilter{
		DefaultAction: ActionKill,
		Rules:         []Rule{{Syscall: 0, Action: ActionAllow}},
	}
}

// NetworkDenyFilter returns a stub filter
func NetworkDenyFilter() *SeccompFilter {
	return &SeccompFilter{
		DefaultAction: ActionAllow,
		Rules:         []Rule{{Syscall: 41, Action: ActionKill}}, // socket
	}
}

// Apply is a no-op on non-Linux
func (f *SeccompFilter) Apply() error {
	return nil
}

//go:build linux

package worker

// ApplySandboxForTest exposes applySandbox for use in external tests.
func ApplySandboxForTest(config StartConfig, cfg SandboxConfig) (StartConfig, error) {
	return applySandbox(config, cfg)
}

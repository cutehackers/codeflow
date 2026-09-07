//go:build !darwin

package protocol

type modelHostIsolationSetup struct {
	command                       string
	args                          []string
	backend                       string
	enforcement                   string
	networkPolicy                 string
	policyDigest                  string
	runtimePolicyBinding          string
	runtimePolicySharedBaseDigest string
	probeScope                    string
	probePolicyDigest             string
	probeSharedBaseDigest         string
	trustedProbe                  ModelHostIsolationProbe
	runtimeRepositoryWriteAudit   *modelHostRuntimeRepositoryWriteAudit
}

func prepareModelHostIsolation(ModelHostConfig, string) (modelHostIsolationSetup, error) {
	return modelHostIsolationSetup{}, UnsupportedVersionError("model host filesystem sandbox backend is unavailable on this platform")
}

func validateModelHostIsolationBeforeStart(modelHostIsolationSetup) error {
	return UnsupportedVersionError("model host filesystem sandbox backend is unavailable on this platform")
}

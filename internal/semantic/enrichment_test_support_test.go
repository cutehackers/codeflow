package semantic

import "context"

// runSemanticEnrichmentForTest keeps fake-client success coverage on the
// pure semantic validation seam. Production callers must use the public entry
// point, which requires a protocol-owned supervised-host attestation.
func runSemanticEnrichmentForTest(ctx context.Context, req EnrichmentRequest) EnrichmentResult {
	return runSemanticEnrichment(ctx, req, false)
}

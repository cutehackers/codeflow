package detect

import (
	"reflect"
	"testing"
)

func TestRequestedFlowCandidateLanguagesInMixedRepository(t *testing.T) {
	files := map[string]string{
		"go.mod":        "module example.com/mixed",
		"package.json":  `{"name":"mixed"}`,
		"checkout.go":   "package mixed",
		"src/order.ts":  "export function processOrder() {}",
		"lib/cart.dart": "void processCart() {}",
	}
	if got, want := RequestedFlowCandidateLanguages("", files), []string{"dart", "go", "typescript"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("natural request candidate languages = %v, want %v", got, want)
	}
	if got, want := RequestedFlowCandidateLanguages("checkout.go#Service.ProcessCheckout", files), []string{"go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Go entry candidate languages = %v, want %v", got, want)
	}
	if got := RequestedEntryLanguage("src/order.ts#processOrder", files); got != "typescript" {
		t.Fatalf("TypeScript entry language = %q", got)
	}
}

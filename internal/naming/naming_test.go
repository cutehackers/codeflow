package naming_test

import (
	"testing"

	"codeflow/internal/naming"
)

func TestDeriveTitle(t *testing.T) {
	cases := []struct {
		sym  string
		want string
	}{
		{"submitOrder", "주문을 제출한다"},
		{"_onItemAdded", "아이템을 추가한다"},
		{"onCheckoutPressed", "결제를 시작한다"},
		{"handleDeepLink", "딥링크를 처리한다"},
		{"placeOrder", "주문을 접수한다"},
		{"signUpWithEmail", "이메일을 회원가입한다"},
		{"unknownCustomAction123", "Unknown Custom Action 123"},
	}

	for _, tc := range cases {
		got := naming.DeriveTitle(tc.sym)
		if got != tc.want {
			t.Errorf("DeriveTitle(%q) = %q, want %q", tc.sym, got, tc.want)
		}
	}
}

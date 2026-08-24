package subgraph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codeflow/core/internal/codegraph"
)

func TestTokenizeQuery(t *testing.T) {
	tests := []struct {
		query    string
		contains []string
	}{
		{
			query:    "푸시 토큰 등록 및 수신",
			contains: []string{"push", "token", "register"},
		},
		{
			query:    "결제 승인과 장바구니 주문 처리",
			contains: []string{"payment", "order", "cart"},
		},
		{
			query:    "사용자 로그인 및 세션 갱신",
			contains: []string{"login", "auth", "session"},
		},
		{
			query:    "bluetooth device scan and connect",
			contains: []string{"bluetooth", "device", "scan", "connect"},
		},
	}

	for _, tt := range tests {
		tokens := TokenizeQuery(tt.query)
		for _, expected := range tt.contains {
			found := false
			for _, tok := range tokens {
				if tok == expected {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("query %q did not produce expected token %q; got: %v", tt.query, expected, tokens)
			}
		}
	}
}

func TestExtractDomainSubgraph(t *testing.T) {
	repo := t.TempDir()
	libDir := filepath.Join(repo, "lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. UI Entry
	uiContent := `
class PushSettingsPage {
  void onEnablePush() {
    PushService.registerDevice();
  }
}
`
	if err := os.WriteFile(filepath.Join(libDir, "push_settings_page.dart"), []byte(uiContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Service
	serviceContent := `
class PushService {
  static void registerDevice() {
    final token = FirebaseMessaging.getToken();
    DeviceRepository.saveToken(token);
    PushApi.sendToken(token);
    FirebaseMessaging.onMessage.listen(handleNotification);
  }
  static void handleNotification(dynamic msg) {}
}
`
	if err := os.WriteFile(filepath.Join(libDir, "push_service.dart"), []byte(serviceContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Repository
	repoContent := `
class DeviceRepository {
  static void saveToken(String token) {
    SecureStorage.save("fcm_token", token);
  }
}
`
	if err := os.WriteFile(filepath.Join(libDir, "device_repository.dart"), []byte(repoContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 4. API
	apiContent := `
class PushApi {
  static void sendToken(String token) {
    HttpClient.post("/api/v1/push/register", token);
  }
}
`
	if err := os.WriteFile(filepath.Join(libDir, "push_api.dart"), []byte(apiContent), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	graph := codegraph.New("")

	res, err := Extract(ctx, repo, "푸시 토큰 등록", 3, nil, graph)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if len(res.Nodes) == 0 {
		t.Fatal("expected discovered domain nodes, got 0")
	}
	if len(res.Edges) == 0 {
		t.Fatal("expected domain edges, got 0")
	}
	if res.Journey == nil || len(res.Journey.Steps) == 0 {
		t.Fatal("expected synthesized business journey steps")
	}

	// Verify that multi-step phases exist
	foundTrigger := false
	foundExecution := false
	foundMutation := false
	for _, step := range res.Journey.Steps {
		if step.Phase == PhaseTrigger {
			foundTrigger = true
		}
		if step.Phase == PhaseExecution {
			foundExecution = true
		}
		if step.Phase == PhaseStateMutation {
			foundMutation = true
		}
	}

	if !foundTrigger || !foundExecution || !foundMutation {
		t.Fatalf("expected trigger, execution, and mutation steps in journey; got: %#v", res.Journey.Steps)
	}
}

func TestExtractPaymentDomainFlow(t *testing.T) {
	repo := t.TempDir()
	libDir := filepath.Join(repo, "lib")
	_ = os.MkdirAll(libDir, 0755)

	cartUI := `
class CartView {
  void onCheckoutPressed() {
    CheckoutService.processPayment();
  }
}
`
	_ = os.WriteFile(filepath.Join(libDir, "cart_view.dart"), []byte(cartUI), 0644)

	checkoutService := `
class CheckoutService {
  static void processPayment() {
    PaymentGatewayApi.charge();
    OrderRepository.updateOrderStatus("paid");
  }
}
`
	_ = os.WriteFile(filepath.Join(libDir, "checkout_service.dart"), []byte(checkoutService), 0644)

	pgApi := `
class PaymentGatewayApi {
  static void charge() {
    HttpClient.post("/api/pay");
  }
}
`
	_ = os.WriteFile(filepath.Join(libDir, "payment_gateway_api.dart"), []byte(pgApi), 0644)

	orderRepo := `
class OrderRepository {
  static void updateOrderStatus(String status) {
    Database.write("orders", status);
  }
}
`
	_ = os.WriteFile(filepath.Join(libDir, "order_repo.dart"), []byte(orderRepo), 0644)

	res, err := Extract(context.Background(), repo, "장바구니 결제 및 주문 처리", 3, nil, nil)
	if err != nil {
		t.Fatalf("Extract payment failed: %v", err)
	}

	if len(res.Nodes) == 0 {
		t.Fatal("expected payment domain nodes")
	}
	if res.Journey == nil || len(res.Journey.Steps) < 2 {
		t.Fatalf("expected multi-step journey for payment, got: %#v", res.Journey)
	}
}

func TestExtractRejectsEmptyQuery(t *testing.T) {
	repo := t.TempDir()
	_, err := Extract(context.Background(), repo, "   ", 2, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}


import 'dart:async';

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import 'features/auth/email_signup_notifier.dart';

/// Hands cold-start push messages to the messaging pipeline (FCM stub).
Future<void> firebaseMessagingBackgroundHandler(
  Map<String, dynamic> message,
) async {
  await Future<void>.delayed(const Duration(seconds: 1));
}

/// Application shell wiring router, providers and observers together.
class ShopApp extends StatelessWidget {
  const ShopApp({super.key});

  @override
  Widget build(BuildContext context) {
    return const MaterialApp.router(routerConfig: null);
  }
}

/// Observes platform lifecycle so the shop can refresh its session.
class AppLifecycleObserver with WidgetsBindingObserver {
  /// System event: platform lifecycle transitions pause/resume the shop.
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _refreshSession();
    }
  }

  void _refreshSession() {}

  /// System event: universal links land here before routing.
  void handleDeepLink(Uri deepLink) {
    final ref = deepLink.queryParameters['ref'];
    if (ref != null) {
      _router.go(deepLink.path);
    }
  }

  final GoRouter _router = GoRouter(routes: [
    GoRoute(
      path: '/cart',
      builder: (context, state) => const CartPage(),
    ),
  ]);
}

/// Cart screen shell.
class CartPage extends StatelessWidget {
  const CartPage({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(body: ShopHomeScreen(onCheckoutPressed: () {}));
  }
}

/// Home screen exposing the checkout entry button.
class ShopHomeScreen extends StatelessWidget {
  const ShopHomeScreen({super.key, required this.onCheckoutPressed});

  final VoidCallback onCheckoutPressed;

  /// User action: the checkout button routes straight to the payment sheet.
  void onCheckoutPressed(BuildContext context) {
    context.go('/checkout');
  }

  @override
  Widget build(BuildContext context) {
    return ElevatedButton(
      onPressed: () => onCheckoutPressed(context),
      child: const Text('Checkout'),
    );
  }
}

// Referenced by imports elsewhere in the fixture; keeps the signup feature
// reachable from main without adding real wiring.
const useSignupFlow = true;
final _signupProbe = EmailSignupState();

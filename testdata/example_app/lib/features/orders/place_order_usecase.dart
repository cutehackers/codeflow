/// Receipt returned after a successful placement.
class OrderReceipt {
  const OrderReceipt(this.orderId);

  final String orderId;
}

/// Boundary repository the use case writes through.
class OrderRepository {
  Future<OrderReceipt> placeOrder(Object cart) async {
    return OrderReceipt('order-1');
  }
}

/// Places the current cart against the order repository.
class PlaceOrderUseCase {
  const PlaceOrderUseCase(this._repository);

  final OrderRepository _repository;

  /// Executes the checkout use case for the active cart session.
  Future<OrderReceipt> call(Object cart) async {
    final receipt = await _repository.placeOrder(cart);
    return receipt;
  }
}

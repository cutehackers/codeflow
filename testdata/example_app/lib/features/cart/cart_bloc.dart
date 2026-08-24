/// Events and state of the cart bloc.
sealed class CartEvent {
  const CartEvent();
}

/// User picked a product row to add to the cart.
class CartItemAdded extends CartEvent {
  const CartItemAdded(this.sku);

  final String sku;
}

/// Immutable cart contents.
class CartState {
  const CartState({this.items = const [], this.count = 0});

  final List<String> items;
  final int count;

  CartState copyWith({List<String>? items, int? count}) => CartState(
        items: items ?? this.items,
        count: count ?? this.count,
      );
}

/// Bloc owning cart contents; event handlers drive user actions.
class CartBloc extends Bloc<CartEvent, CartState> {
  CartBloc() {
    on<CartItemAdded>(_onItemAdded);
    on<CartCheckedOut>(_onCheckedOut);
  }

  /// User action: a product row was added to the cart.
  Future<void> _onItemAdded(
    CartItemAdded event,
    Emitter<CartState> emit,
  ) async {
    emit(state.copyWith(count: state.count + 1));
  }

  Future<void> _onCheckedOut(Object event, Emitter<CartState> emit) async {
    emit(const CartState());
  }
}

import { useState, useCallback } from 'react';
import { api } from '../lib/api';

export interface CartItem {
  id: string;
  name: string;
  price: number;
  quantity: number;
}

export function useCart() {
  const [cart, setCart] = useState<CartItem[]>([]);

  const addItem = useCallback((item: CartItem) => {
    setCart((prev) => [...prev, item]);
  }, []);

  const removeItem = useCallback((id: string) => {
    setCart((prev) => prev.filter((item) => item.id !== id));
  }, []);

  const clearCart = useCallback(() => {
    setCart([]);
  }, []);

  const calculateTotal = useCallback(() => {
    return cart.reduce((sum, item) => sum + item.price * item.quantity, 0);
  }, [cart]);

  const mutateCart = async (action: string, payload: any) => {
    const syncRes = await api.cart.sync({ action, payload });
    return syncRes;
  };

  return {
    cart,
    addItem,
    removeItem,
    clearCart,
    calculateTotal,
    mutateCart,
  };
}

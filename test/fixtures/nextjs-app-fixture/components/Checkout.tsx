import React, { useState } from 'react';
import { useCart } from '../hooks/useCart';
import { api } from '../lib/api';

export const Checkout: React.FC = () => {
  const { cart, calculateTotal, clearCart } = useCart();
  const [discountCode, setDiscountCode] = useState('');
  const [discount, setDiscount] = useState(0);

  const handleApplyDiscount = async (e: React.FormEvent) => {
    e.preventDefault();
    const result = await api.discounts.validate(discountCode);
    if (result.valid) {
      setDiscount(result.percentage);
    }
  };

  const handlePaymentSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const orderData = {
      items: cart,
      total: calculateTotal() * (1 - discount / 100),
    };
    const confirmation = await api.orders.checkout(orderData);
    if (confirmation.success) {
      clearCart();
    }
  };

  return (
    <form onSubmit={handlePaymentSubmit}>
      <input
        value={discountCode}
        onChange={(e) => setDiscountCode(e.target.value)}
        placeholder="Coupon"
      />
      <button type="button" onClick={handleApplyDiscount}>
        Apply
      </button>
      <button type="submit">Pay Now</button>
    </form>
  );
};

'use client';

import React, { useState } from 'react';
import { useCart } from '../hooks/useCart';
import { api } from '../lib/api';

export default function HomePage() {
  const { cart, calculateTotal } = useCart();
  const [status, setStatus] = useState<string>('idle');

  const handleQuickCheckout = async (e: React.FormEvent) => {
    e.preventDefault();
    setStatus('processing');
    const orderPayload = {
      items: cart,
      total: calculateTotal(),
      timestamp: Date.now(),
    };
    const response = await api.orders.checkout(orderPayload);
    if (response.success) {
      setStatus('completed');
    }
  };

  return (
    <div className="home-container">
      <h1>Storefront</h1>
      <button onClick={handleQuickCheckout} disabled={status === 'processing'}>
        Quick Checkout
      </button>
    </div>
  );
}

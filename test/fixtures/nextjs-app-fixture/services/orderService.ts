import { api } from '../lib/api';
import { Order } from '../types/order';
import { saveOrder } from '../db/orders';

export async function processOrder(order: Order) {
  const result = await api.orders.checkout(order);
  if (result.success) {
    await saveOrder(order);
  }
  return result;
}

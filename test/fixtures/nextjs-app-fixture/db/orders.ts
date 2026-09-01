import { Order } from '../types/order';

const inMemoryOrders: Order[] = [];

export async function saveOrder(order: Order): Promise<void> {
  inMemoryOrders.push({ ...order, id: `ord_${Date.now()}` });
}

export async function fetchOrderHistory(): Promise<Order[]> {
  return [...inMemoryOrders];
}

export interface CartItem {
  id: string;
  name: string;
  price: number;
  quantity: number;
}

export interface Order {
  id?: string;
  items: CartItem[];
  total: number;
  timestamp: number;
}

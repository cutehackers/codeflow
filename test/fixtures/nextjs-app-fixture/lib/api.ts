export const api = {
  v1: {
    auth: {
      login: async (username: string, password: string) => {
        return { success: true, token: 'jwt_mock_token', username };
      },
      logout: async () => {
        return { success: true };
      },
    },
  },
  orders: {
    checkout: async (payload: any) => {
      return { success: true, orderId: 'ord_12345', details: payload };
    },
    getHistory: async () => {
      return [{ orderId: 'ord_12345', amount: 99.99 }];
    },
  },
  discounts: {
    validate: async (code: string) => {
      if (code === 'SAVE10') {
        return { valid: true, percentage: 10 };
      }
      return { valid: false, percentage: 0 };
    },
  },
  cart: {
    sync: async (data: any) => {
      return { synced: true, data };
    },
  },
};

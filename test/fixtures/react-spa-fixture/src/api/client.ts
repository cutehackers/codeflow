export const api = {
  v1: {
    auth: {
      login: async (email: string, pass: string) => {
        return {
          success: true,
          token: 'spa_token_xyz',
          user: { id: 'u_101', name: 'Bob', email },
        };
      },
      register: async (data: any) => ({ success: true, id: 'u_102' }),
    },
    user: {
      getProfile: async () => ({ id: 'u_101', name: 'Bob', email: 'bob@example.com' }),
      updateSettings: async (settings: any) => ({ updated: true, settings }),
    },
  },
};

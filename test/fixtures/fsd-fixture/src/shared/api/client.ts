export const feedApi = {
  auth: {
    login: async (credentials: any) => ({
      success: true,
      user: { id: 'u_1', name: credentials.user, email: `${credentials.user}@example.com` },
    }),
    logout: async () => ({ success: true }),
  },
  posts: {
    list: async () => [],
    like: async (postId: string) => ({ success: true, postId }),
    comment: async (postId: string, text: string) => ({
      id: `c_${Date.now()}`,
      postId,
      text,
      createdAt: Date.now(),
    }),
  },
};

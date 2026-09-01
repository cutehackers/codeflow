import { useState } from 'react';
import { feedApi } from '../../shared/api/client';
import { UserProfile } from '../../entities/user/types';

export function useAuth() {
  const [user, setUser] = useState<UserProfile | null>({ id: 'u1', name: 'Alice', email: 'alice@example.com' });

  const loginAction = async (credentials: { user: string; pass: string }) => {
    const res = await feedApi.auth.login(credentials);
    if (res.user) {
      setUser(res.user);
    }
  };

  const logoutAction = async () => {
    await feedApi.auth.logout();
    setUser(null);
  };

  const mutateSession = (updated: Partial<UserProfile>) => {
    setUser((prev) => (prev ? { ...prev, ...updated } : null));
  };

  return {
    user,
    loginAction,
    logoutAction,
    mutateSession,
  };
}

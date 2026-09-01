import { useState, useCallback } from 'react';
import { authenticateUser } from '../services/authService';
import { UserProfile } from '../types/auth';

export function useAuth() {
  const [user, setUser] = useState<UserProfile | null>(null);

  const login = useCallback(async (email: string, pass: string) => {
    const response = await authenticateUser({ email, password: pass });
    if (response.success && response.user) {
      setUser(response.user);
    }
  }, []);

  const logout = useCallback(() => {
    setUser(null);
  }, []);

  const register = useCallback(async (email: string, pass: string, name: string) => {
    console.log('Registering user:', name, email);
  }, []);

  const updateProfile = useCallback(async (profile: Partial<UserProfile>) => {
    setUser((prev) => (prev ? { ...prev, ...profile } : null));
  }, []);

  return {
    user,
    login,
    logout,
    register,
    updateProfile,
  };
}

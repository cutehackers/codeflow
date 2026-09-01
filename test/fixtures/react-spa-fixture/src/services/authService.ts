import { api } from '../api/client';
import { AuthCredentials, AuthResponse } from '../types/auth';

export async function authenticateUser(credentials: AuthCredentials): Promise<AuthResponse> {
  const result = await api.v1.auth.login(credentials.email, credentials.password);
  return result;
}

export async function fetchCurrentSession(): Promise<AuthResponse> {
  const result = await api.v1.user.getProfile();
  return { success: true, user: result };
}

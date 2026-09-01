export interface UserProfile {
  id: string;
  name: string;
  email: string;
  avatarUrl?: string;
}

export type UserRole = 'admin' | 'member' | 'guest';

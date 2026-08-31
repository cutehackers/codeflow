export interface UserSession {
  userId: string;
  token: string;
}

export class AuthRepository {
  async login(email: string, pass: string): Promise<UserSession> {
    if (!email || !pass) {
      throw new Error("Invalid credentials");
    }
    // Network call to backend
    return {
      userId: "user-123",
      token: "session-token-abc",
    };
  }
}

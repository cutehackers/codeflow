import { AuthRepository, UserSession } from './AuthRepository';

export class LoginUseCase {
  constructor(private readonly authRepository: AuthRepository) {}

  async execute(email: string, pass: string): Promise<UserSession> {
    if (!email.includes('@')) {
      throw new Error("Invalid email address format");
    }
    const session = await this.authRepository.login(email, pass);
    return session;
  }
}

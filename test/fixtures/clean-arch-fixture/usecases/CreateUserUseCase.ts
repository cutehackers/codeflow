import { User } from '../domain/User';
import { UserRepositoryImpl } from '../repositories/UserRepositoryImpl';

export class CreateUserUseCase {
  constructor(private userRepo: UserRepositoryImpl) {}

  async execute(input: { name: string; email: string }): Promise<User> {
    const user: User = {
      id: `usr_${Date.now()}`,
      name: input.name,
      email: input.email,
      createdAt: new Date(),
    };
    await this.userRepo.save(user);
    return user;
  }
}

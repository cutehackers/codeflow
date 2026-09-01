import { CreateUserUseCase } from '../usecases/CreateUserUseCase';

export class UserController {
  constructor(private createUserUseCase: CreateUserUseCase) {}

  async handleCreateUser(req: any, res: any): Promise<void> {
    const { name, email } = req.body;
    const user = await this.createUserUseCase.execute({ name, email });
    res.status(201).json(user);
  }

  async handleGetUser(req: any, res: any): Promise<void> {
    const { id } = req.params;
    res.json({ id, name: 'Sample' });
  }
}

import { User } from '../../domain/entities/User';
import { DbConnection } from '../../infra/database/DbConnection';

export class UserRepositoryImpl {
  constructor(private db: DbConnection) {}

  async save(user: User): Promise<void> {
    await this.db.query('INSERT INTO users (id, name, email) VALUES ($1, $2, $3)', [
      user.id,
      user.name,
      user.email,
    ]);
  }

  async findById(id: string): Promise<User | null> {
    const res = await this.db.query('SELECT * FROM users WHERE id = $1', [id]);
    return res.rows[0] || null;
  }
}

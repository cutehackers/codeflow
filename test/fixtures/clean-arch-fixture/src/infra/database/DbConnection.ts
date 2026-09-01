export class DbConnection {
  async query(sql: string, params: any[] = []): Promise<{ rows: any[] }> {
    console.log('Executing DB query:', sql, params);
    return { rows: [] };
  }
}

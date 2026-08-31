import React, { useState } from 'react';
import { LoginUseCase } from './LoginUseCase';

export interface LoginViewProps {
  loginUseCase: LoginUseCase;
}

export function LoginView({ loginUseCase }: LoginViewProps) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !password) {
      return;
    }
    setLoading(true);
    try {
      const session = await loginUseCase.execute(email, password);
      console.log('Logged in:', session);
    } finally {
      setLoading(false);
    }
  };

  return (
    <form onSubmit={handleSubmit}>
      <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
      <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
      <button type="submit" disabled={loading}>Sign In</button>
    </form>
  );
}

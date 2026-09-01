import React, { useState } from 'react';
import { useAuth } from '../features/auth/useAuth';

export const Header: React.FC = () => {
  const { user, logoutAction } = useAuth();
  const [searchQuery, setSearchQuery] = useState('');

  const onSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    console.log('Searching for:', searchQuery);
  };

  const onLogoutClick = async () => {
    await logoutAction();
  };

  return (
    <header className="app-header">
      <h1>FSD Feed</h1>
      <form onSubmit={onSearchSubmit}>
        <input
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          placeholder="Search posts"
        />
      </form>
      {user ? (
        <div>
          <span>Welcome, {user.name}</span>
          <button onClick={onLogoutClick}>Log out</button>
        </div>
      ) : null}
    </header>
  );
};

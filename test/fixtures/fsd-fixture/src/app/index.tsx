import React from 'react';
import { FeedList } from '../widgets/FeedList';
import { Header } from '../widgets/Header';

export const App: React.FC = () => {
  return (
    <div className="app-container">
      <Header />
      <main>
        <FeedList />
      </main>
    </div>
  );
};

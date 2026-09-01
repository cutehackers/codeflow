import React, { useState } from 'react';
import { useTheme } from '../hooks/useTheme';

export const Dashboard: React.FC = () => {
  const { theme, toggleTheme } = useTheme();
  const [data, setData] = useState<any[]>([]);

  const handleRefresh = async () => {
    console.log('Refreshing dashboard metrics...');
  };

  const onExportData = () => {
    const blob = new Blob([JSON.stringify(data)], { type: 'application/json' });
    console.log('Exported data blob:', blob.size);
  };

  return (
    <div className={`dashboard-root theme-${theme}`}>
      <header>
        <h2>Analytics Dashboard</h2>
        <button onClick={toggleTheme}>Toggle Theme</button>
        <button onClick={handleRefresh}>Refresh</button>
        <button onClick={onExportData}>Export JSON</button>
      </header>
    </div>
  );
};

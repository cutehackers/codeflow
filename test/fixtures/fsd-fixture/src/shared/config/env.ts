export const envConfig = {
  apiBaseUrl: process.env.REACT_APP_API_URL || 'https://api.example.com',
  isProduction: process.env.NODE_ENV === 'production',
};

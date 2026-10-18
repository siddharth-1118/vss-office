import React from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { AuthProvider, LoginForm, RequireAuth } from './auth';
import { DocList } from './DocList';
import { Grid } from './Grid';

export const App: React.FC = () => {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginForm />} />
          <Route
            path="/docs"
            element={
              <RequireAuth>
                <DocList />
              </RequireAuth>
            }
          />
          <Route
            path="/docs/:id"
            element={
              <RequireAuth>
                <Grid />
              </RequireAuth>
            }
          />
          <Route path="*" element={<Navigate to="/docs" replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  );
};

export default App;

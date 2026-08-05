import type { ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import Login from './pages/Login'
import RunList from './pages/RunList'
import RunDetail from './pages/RunDetail'

function RequireAuth({ children }: { children: ReactNode }) {
  const { principal } = useAuth()
  const location = useLocation()

  if (principal === undefined) return <p>Loading…</p>
  if (principal === null) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  return <>{children}</>
}

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route
            path="/"
            element={
              <RequireAuth>
                <RunList />
              </RequireAuth>
            }
          />
          <Route
            path="/runs/:id"
            element={
              <RequireAuth>
                <RunDetail />
              </RequireAuth>
            }
          />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}

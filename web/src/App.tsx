import type { ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import Layout from './Layout'
import Login from './pages/Login'
import RunList from './pages/RunList'
import RunDetail from './pages/RunDetail'
import JobList from './pages/JobList'
import JobDetail from './pages/JobDetail'
import ManifestEditor from './pages/ManifestEditor'
import RuntimeList from './pages/RuntimeList'
import RuntimeDetail from './pages/RuntimeDetail'
import UserList from './pages/UserList'
import UserDetail from './pages/UserDetail'
import TokenList from './pages/TokenList'
import Settings from './pages/Settings'

function Protected({ children }: { children: ReactNode }) {
  const { principal } = useAuth()
  const location = useLocation()

  if (principal === undefined) return <p>Loading…</p>
  if (principal === null) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  return <Layout>{children}</Layout>
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
              <Protected>
                <RunList />
              </Protected>
            }
          />
          <Route
            path="/runs/:id"
            element={
              <Protected>
                <RunDetail />
              </Protected>
            }
          />
          <Route
            path="/jobs"
            element={
              <Protected>
                <JobList />
              </Protected>
            }
          />
          <Route
            path="/jobs/new"
            element={
              <Protected>
                <ManifestEditor mode="create" />
              </Protected>
            }
          />
          <Route
            path="/jobs/:id"
            element={
              <Protected>
                <JobDetail />
              </Protected>
            }
          />
          <Route
            path="/jobs/:id/edit"
            element={
              <Protected>
                <ManifestEditor mode="edit" />
              </Protected>
            }
          />
          <Route
            path="/runtimes"
            element={
              <Protected>
                <RuntimeList />
              </Protected>
            }
          />
          <Route
            path="/runtimes/:id"
            element={
              <Protected>
                <RuntimeDetail />
              </Protected>
            }
          />
          <Route
            path="/users"
            element={
              <Protected>
                <UserList />
              </Protected>
            }
          />
          <Route
            path="/users/:id"
            element={
              <Protected>
                <UserDetail />
              </Protected>
            }
          />
          <Route
            path="/tokens"
            element={
              <Protected>
                <TokenList />
              </Protected>
            }
          />
          <Route
            path="/settings"
            element={
              <Protected>
                <Settings />
              </Protected>
            }
          />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}

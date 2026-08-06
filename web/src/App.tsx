import type { ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation, useParams } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import Layout from './Layout'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import RunList from './pages/RunList'
import RunDetail from './pages/RunDetail'
import JobList from './pages/JobList'
import JobDetail from './pages/JobDetail'
import ManifestEditor from './pages/ManifestEditor'
import RuntimeList from './pages/RuntimeList'
import RuntimeDetail from './pages/RuntimeDetail'
import UserDetail from './pages/UserDetail'
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

// react-router's <Navigate to> is a static string - it can't interpolate a
// route param, so redirecting the old /users/:id path needs this tiny
// wrapper rather than a literal <Navigate>.
function RedirectUserDetail() {
  const { id } = useParams()
  return <Navigate to={`/settings/users/${id}`} replace />
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
                <Dashboard />
              </Protected>
            }
          />
          <Route
            path="/runs"
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
            path="/settings"
            element={
              <Protected>
                <Settings />
              </Protected>
            }
          />
          <Route
            path="/settings/:tab"
            element={
              <Protected>
                <Settings />
              </Protected>
            }
          />
          <Route
            path="/settings/users/:id"
            element={
              <Protected>
                <UserDetail />
              </Protected>
            }
          />
          <Route path="/users" element={<Navigate to="/settings/users" replace />} />
          <Route path="/users/:id" element={<RedirectUserDetail />} />
          <Route path="/tokens" element={<Navigate to="/settings/tokens" replace />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}

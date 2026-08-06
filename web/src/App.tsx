import type { ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useParams } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import Layout from './Layout'
import Login from './pages/Login'
import RolelessScreen from './pages/RolelessScreen'
import Dashboard from './pages/Dashboard'
import RunList from './pages/RunList'
import RunDetail from './pages/RunDetail'
import JobList from './pages/JobList'
import JobDetail from './pages/JobDetail'
import ManifestEditor from './pages/ManifestEditor'
import ScriptEditor from './pages/ScriptEditor'
import RuntimeList from './pages/RuntimeList'
import RuntimeDetail from './pages/RuntimeDetail'
import UserDetail from './pages/UserDetail'
import Settings from './pages/Settings'

// A JIT-provisioned OIDC principal (task 9.6) has no role - and therefore no
// permissions at all - until an admin assigns one via UserDetail's role
// reassignment. Every authenticated route passes through here, so this is
// the one place task 9.11 renders an explanatory screen instead of letting
// that principal wander into a wall of 403s across Dashboard/Runs/Jobs/etc.
function Protected({ children }: { children: ReactNode }) {
  const { principal } = useAuth()

  if (principal === undefined) return <p>Loading…</p>
  if (principal === null) {
    return <Navigate to="/login" replace />
  }
  if (principal.permissions.length === 0) {
    return (
      <Layout>
        <RolelessScreen />
      </Layout>
    )
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
            path="/jobs/:id/script"
            element={
              <Protected>
                <ScriptEditor />
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

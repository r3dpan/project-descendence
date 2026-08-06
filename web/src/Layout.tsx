import type { ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from './auth'

export default function Layout({ children }: { children: ReactNode }) {
  const { principal, logout } = useAuth()
  const navigate = useNavigate()
  // Hidden rather than shown-and-403'd on click, matching the CLI TUI's
  // menu-gating (task 8.9) - users:read is what governs the server side too.
  const canSeeUsers = principal?.permissions.includes('users:read') ?? false

  async function handleLogout() {
    await logout()
    navigate('/login', { replace: true })
  }

  const linkStyle = ({ isActive }: { isActive: boolean }) => ({
    marginRight: '1rem',
    fontWeight: isActive ? 'bold' : 'normal',
  })

  return (
    <div>
      <nav style={{ padding: '0.75rem 1rem', borderBottom: '1px solid #444', display: 'flex', alignItems: 'center' }}>
        <NavLink to="/" end style={linkStyle}>
          Runs
        </NavLink>
        <NavLink to="/jobs" style={linkStyle}>
          Jobs
        </NavLink>
        <NavLink to="/runtimes" style={linkStyle}>
          Runtimes
        </NavLink>
        {canSeeUsers && (
          <>
            <NavLink to="/users" style={linkStyle}>
              Users
            </NavLink>
            <NavLink to="/tokens" style={linkStyle}>
              Tokens
            </NavLink>
          </>
        )}
        <NavLink to="/settings" style={({ isActive }) => ({ ...linkStyle({ isActive }), marginLeft: 'auto' })}>
          Settings
        </NavLink>
        <button type="button" onClick={handleLogout} style={{ marginLeft: '1rem' }}>
          Sign out
        </button>
      </nav>
      {children}
    </div>
  )
}

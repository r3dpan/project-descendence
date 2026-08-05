import type { ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from './auth'

export default function Layout({ children }: { children: ReactNode }) {
  const { logout } = useAuth()
  const navigate = useNavigate()

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
        <button type="button" onClick={handleLogout} style={{ marginLeft: 'auto' }}>
          Sign out
        </button>
      </nav>
      {children}
    </div>
  )
}

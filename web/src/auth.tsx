import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { APIError } from './api/client'
import { login as apiLogin, logout as apiLogout, whoami, type Principal } from './api/auth'

interface AuthState {
  // undefined: still checking; null: checked, not logged in.
  principal: Principal | null | undefined
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [principal, setPrincipal] = useState<Principal | null | undefined>(undefined)

  useEffect(() => {
    whoami()
      .then(setPrincipal)
      .catch((err) => {
        if (err instanceof APIError && err.status === 401) {
          setPrincipal(null)
          return
        }
        throw err
      })
  }, [])

  async function login(username: string, password: string) {
    const p = await apiLogin(username, password)
    setPrincipal(p)
  }

  async function logout() {
    await apiLogout()
    setPrincipal(null)
  }

  return <AuthContext.Provider value={{ principal, login, logout }}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within an AuthProvider')
  return ctx
}

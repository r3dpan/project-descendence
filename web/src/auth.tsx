import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { APIError } from './api/client'
import { whoami, type Principal } from './api/auth'

interface AuthState {
  // undefined: still checking; null: checked, not logged in.
  principal: Principal | null | undefined
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

  return <AuthContext.Provider value={{ principal }}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within an AuthProvider')
  return ctx
}

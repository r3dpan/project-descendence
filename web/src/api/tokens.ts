import { request } from './client'
import type { components } from './schema'

export type Token = components['schemas']['Token']
export type TokenList = components['schemas']['TokenList']
export type TokenCreateResponse = components['schemas']['TokenCreateResponse']

export function listTokens(): Promise<TokenList> {
  return request<TokenList>('/api/v1/tokens')
}

export function getToken(id: number | string): Promise<Token> {
  return request<Token>(`/api/v1/tokens/${id}`)
}

export interface CreateTokenParams {
  name: string
  role: string
  expiresAt?: string
}

// Returns the plaintext token - shown exactly once, never retrievable again.
export function createToken(params: CreateTokenParams): Promise<TokenCreateResponse> {
  return request<TokenCreateResponse>('/api/v1/tokens', { method: 'POST', body: params })
}

export function revokeToken(id: number | string): Promise<void> {
  return request<void>(`/api/v1/tokens/${id}`, { method: 'DELETE' })
}

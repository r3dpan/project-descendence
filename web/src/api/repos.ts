import { request } from './client'
import type { components } from './schema'

export type Repo = components['schemas']['Repo']
export type RepoList = components['schemas']['RepoList']
export type RepoFileGet = components['schemas']['RepoFileGet']
export type RepoFileResult = components['schemas']['RepoFileResult']

export interface ListReposParams {
  cursor?: string
  limit?: number
  [key: string]: string | number | undefined
}

export function listRepos(params: ListReposParams = {}): Promise<RepoList> {
  return request<RepoList>('/api/v1/repos', { query: params })
}

export function getRepo(id: number | string): Promise<Repo> {
  return request<Repo>(`/api/v1/repos/${id}`)
}

// path is repo-root-relative and may contain slashes (a manifest's own
// directory) - encode each segment, not the path wholesale, or a "/" would
// come back double-encoded.
function encodeFilePath(path: string): string {
  return path.split('/').map(encodeURIComponent).join('/')
}

export function getRepoFile(repoId: number | string, path: string): Promise<RepoFileGet> {
  return request<RepoFileGet>(`/api/v1/repos/${repoId}/files/${encodeFilePath(path)}`)
}

export function createRepoFile(
  repoId: number | string,
  body: { path: string; content: string; message?: string },
): Promise<RepoFileResult> {
  return request<RepoFileResult>(`/api/v1/repos/${repoId}/files`, {
    method: 'POST',
    body,
  })
}

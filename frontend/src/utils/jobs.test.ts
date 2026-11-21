import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, pending, waitJobs } from '../api/client'
import type { BatchResult, Job } from '../api/types'
import { t } from '../i18n'
import { errorMessage } from './jobs'

const job = (state: Job['state'], extra: Partial<Job> = {}): Job => ({
  id: 'j1', workspace_id: 'ws', kind: 'trash', paper_id: 'p1', state, phase: 'apply', done: 1, total: 3,
  created_at: '', updated_at: '', ...extra,
})

const result = (jobs: Job[]): BatchResult => ({ paper: null, blocks: null, deleted: null, refs: null, jobs })

function serve(...answers: Job[]) {
  const fetch = vi.fn(async (url: string) => new Response(JSON.stringify(answers.shift() ?? { url }), { headers: { 'Content-Type': 'application/json' } }))
  vi.stubGlobal('fetch', fetch)
  return fetch
}

afterEach(() => vi.unstubAllGlobals())

describe('background jobs', () => {
  it('tells whether a result still has work going on', () => {
    expect(pending(result([job('running')]))).toBe(true)
    expect(pending(result([job('done')]))).toBe(false)
    expect(pending(result([]))).toBe(false)
  })

  it('polls until the job is done and reports progress', async () => {
    const fetch = serve(job('running', { done: 2 }), job('done', { done: 3, result: { trashed: true } }))
    const progress: number[] = []
    const jobs = await waitJobs('ws', result([job('queued', { done: 0 })]), (done) => progress.push(done), 0)
    expect(jobs[0]?.state).toBe('done')
    expect(jobs[0]?.result?.trashed).toBe(true)
    expect(progress).toEqual([0, 2])
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(String(fetch.mock.calls[0]?.[0])).toContain('/workspaces/ws/jobs/j1')
  })

  it('turns a failed job into an error', async () => {
    serve(job('failed', { error: 'not yours', error_code: 'permission_denied' }))
    await expect(waitJobs('ws', result([job('running')]), undefined, 0)).rejects.toMatchObject({ code: 'permission_denied', message: 'not yours' })
  })

  it('does not call the server for finished jobs', async () => {
    const fetch = serve()
    const jobs = await waitJobs('ws', result([job('done')]), undefined, 0)
    expect(jobs).toHaveLength(1)
    expect(fetch).not.toHaveBeenCalled()
  })

  it('explains a paper that is being moved', () => {
    expect(errorMessage(new ApiError(409, 'busy', 'x'), t)).toBe(t('busy'))
    expect(errorMessage(new Error('boom'), t)).toContain('boom')
  })
})
